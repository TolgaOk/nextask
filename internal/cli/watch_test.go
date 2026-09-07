package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWatchingCLI(t *testing.T) {
	pool := setupTestDB(t)
	binary := buildTestCLI(t)
	ctx := context.Background()
	for _, mode := range []string{"wait", "log", "enqueue-pending", "enqueue-running"} {
		t.Run("interrupt-"+mode, func(t *testing.T) {
			id := "interrupt-" + mode
			var args []string
			switch mode {
			case "wait":
				createWatchTask(t, pool, id, nil)
				args = []string{"wait", id}
			case "log":
				createWatchTask(t, pool, id, nil)
				args = []string{"log", id, "--attach"}
			default:
				args = []string{"enqueue", "sleep 60", "--id", id, "--attach"}
			}
			p := startWatchCLI(t, binary, args...)
			awaitWatchListener(t, pool, db.FromTaskChannel(id))
			if mode == "enqueue-running" {
				if _, err := pool.Exec(ctx, "UPDATE tasks SET status = 'running' WHERE id = $1", id); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			if mode == "enqueue-running" {
				deadline := time.Now().Add(3 * time.Second)
				requested := false
				for time.Now().Before(deadline) {
					if err := pool.QueryRow(ctx, "SELECT cancel_requested_at IS NOT NULL FROM tasks WHERE id = $1", id).Scan(&requested); err != nil {
						t.Fatal(err)
					}
					if requested {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !requested {
					t.Fatal("interrupt did not persist cancellation")
				}
				p.stillRunning(t)
				if err := db.CompleteTask(ctx, pool, id, db.StatusCancelled, 23); err != nil {
					t.Fatal(err)
				}
				p.result(t, 23)
			} else {
				p.result(t, 0)
			}
			task, err := db.GetTask(ctx, pool, id, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			want := db.StatusPending
			if strings.HasPrefix(mode, "enqueue") {
				want = db.StatusCancelled
			}
			if task.Status != want {
				t.Fatalf("status = %s, want %s", task.Status, want)
			}
		})
	}
	for _, mode := range []string{"enqueue", "log"} {
		t.Run(mode+"-missed-failure", func(t *testing.T) {
			id := mode + "-missed-failure"
			args := []string{"enqueue", "sleep 60", "--id", id, "--attach"}
			code := 19
			if mode == "log" {
				createWatchTask(t, pool, id, nil)
				args = []string{"log", id, "--attach", "--stream", "stdout"}
				code = 0 // Viewing logs does not propagate the task's failure.
			}
			p := startWatchCLI(t, binary, args...)
			awaitWatchListener(t, pool, db.FromTaskChannel(id))
			p.stillRunning(t)
			if _, err := db.InsertLog(ctx, pool, id, "stdout", "final-output"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.InsertLog(ctx, pool, id, "stderr", "filtered-error"); err != nil {
				t.Fatal(err)
			}
			completeWatchTask(t, pool, id, 19, false)
			out := p.result(t, code)
			if strings.Count(out, "final-output") != 1 || !strings.Contains(out, "Task failed (exit 19)") {
				t.Fatal(out)
			}
			if mode == "log" && strings.Contains(out, "filtered-error") {
				t.Fatalf("stream filter lost during attach: %s", out)
			}
		})
	}
	t.Run("worker-stop", func(t *testing.T) {
		// The requested worker is older than the default list limit.
		if err := db.RegisterWorker(ctx, pool, "old-worker", 1234, "test", "/tmp"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO workers (id, pid, hostname, workdir, status)
			SELECT 'new-' || i, 1234, 'test', '/tmp', 'stopped' FROM generate_series(1,1001) i`); err != nil {
			t.Fatal(err)
		}
		p := startWatchCLI(t, binary, "worker", "stop", "old-worker", "--timeout", "5s")
		awaitWatchListener(t, pool, db.FromWorkerChannel("old-worker"))
		// A notification alone is not confirmation.
		if _, err := pool.Exec(ctx, "SELECT pg_notify($1, 'stopped')", db.FromWorkerChannel("old-worker")); err != nil {
			t.Fatal(err)
		}
		p.stillRunning(t)
		if err := db.UnregisterWorker(ctx, pool, "old-worker"); err != nil {
			t.Fatal(err)
		}
		out := p.result(t, 0)
		if !strings.Contains(out, "Worker old-worker stopped") {
			t.Fatal(out)
		}
		out = startWatchCLI(t, binary, "worker", "stop", "old-worker").result(t, 0)
		if !strings.Contains(out, "already stopped") {
			t.Fatal(out)
		}
	})
	t.Run("worker-stop-timeout", func(t *testing.T) {
		if err := db.RegisterWorker(ctx, pool, "unresponsive", 1234, "test", "/tmp"); err != nil {
			t.Fatal(err)
		}
		out := startWatchCLI(t, binary, "worker", "stop", "unresponsive", "--timeout", "200ms").result(t, 1)
		if !strings.Contains(out, "worker did not confirm") {
			t.Fatal(out)
		}
	})
	for _, mode := range []string{"timeout", "interrupt"} {
		t.Run("worker-"+mode, func(t *testing.T) {
			id := "worker-" + mode
			args := []string{"worker", "--_id", id, "--workdir", t.TempDir(), "--filter", "group=unused"}
			if mode == "timeout" {
				args = append(args, "--timeout", "500ms")
			}
			p := startWatchCLI(t, binary, args...)
			if mode == "interrupt" {
				awaitWatchListener(t, pool, "")
				p.stillRunning(t)
				if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			p.result(t, 0)
			var status string
			if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id = $1", id).Scan(&status); err != nil || status != "stopped" {
				t.Fatalf("worker did not clean up: %s, %v", status, err)
			}
		})
	}

}

func TestStateWatcherLifecycle(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	cfg.Retry.InitialInterval = 10 * time.Millisecond
	cfg.Retry.MaxInterval = 20 * time.Millisecond
	t.Run("cancel-active-query", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		watch, err := newStateWatcher(ctx, cfg, io.Discard, "blocked-query")
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Close()
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- watch.Run(ctx, func(ctx context.Context) (bool, error) {
				close(started)
				_, err := pool.Exec(ctx, "SELECT pg_sleep(60)")
				return false, err
			})
		}()
		<-started
		// Wait until PostgreSQL is actually executing the read before cancelling.
		deadline := time.Now().Add(3 * time.Second)
		active := false
		for time.Now().Before(deadline) {
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_stat_activity
				WHERE query = 'SELECT pg_sleep(60)' AND state = 'active')`).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if active {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !active {
			t.Fatal("test query did not start")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not interrupt active query")
		}
	})
	t.Run("subscribe-under-notification-load", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		watch, err := newStateWatcher(ctx, cfg, io.Discard, "busy-watch")
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Close()
		for i := 0; i < 64; i++ {
			if _, err := pool.Exec(ctx, "SELECT pg_notify('busy-watch', $1)", fmt.Sprint(i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := watch.notifier.Add(ctx, "added-watch"); err != nil {
			t.Fatalf("notification queue blocked subscription: %v", err)
		}
	})
	for _, transient := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry-%t", transient), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			watch, err := newStateWatcher(ctx, cfg, io.Discard, "retry-watch")
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			code := "42501"
			if transient {
				code = "40001"
			}
			failure := &pgconn.PgError{Code: code, Message: "test query error"}
			calls := 0
			err = watch.Run(ctx, func(context.Context) (bool, error) {
				calls++
				if calls == 1 {
					return false, failure
				}
				return true, nil
			})
			if transient {
				if err != nil || calls != 2 {
					t.Fatalf("transient read was not retried: %d, %v", calls, err)
				}
			} else if !errors.Is(err, failure) || calls != 1 {
				t.Fatalf("permanent error was hidden: %d, %v", calls, err)
			}
		})
	}
	t.Run("closed-listener", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		watch, err := newStateWatcher(ctx, cfg, io.Discard, "closed-watch")
		if err != nil {
			t.Fatal(err)
		}
		watch.Close()
		err = watch.Run(ctx, func(context.Context) (bool, error) { return false, nil })
		if err == nil || !strings.Contains(err.Error(), "listener closed") {
			t.Fatalf("closed listener treated as completion: %v", err)
		}
	})
}
