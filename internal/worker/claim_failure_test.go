package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWorkerClaimFailures(t *testing.T) {
	for _, mode := range []string{"permanent", "transient", "stop-backoff"} {
		t.Run(mode, func(t *testing.T) {
			pool := setupTestDB(t)
			defer pool.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			code, attempts := "08006", 1000
			if mode == "permanent" {
				code = "42501"
			} else if mode == "transient" {
				attempts = 2
			}
			// Sequence increments survive rollback and expose failed claim attempts.
			_, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE test_claim_attempts;
				CREATE FUNCTION test_claim_failure() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.status = 'running' AND nextval('test_claim_attempts') <= %d THEN
						RAISE EXCEPTION 'claim unavailable' USING ERRCODE = '%s';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER test_claim BEFORE UPDATE ON tasks
				FOR EACH ROW EXECUTE FUNCTION test_claim_failure()`, attempts, code))
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Exec(context.Background(), "DROP TRIGGER test_claim ON tasks; DROP FUNCTION test_claim_failure(); DROP SEQUENCE test_claim_attempts")
			task := &db.Task{ID: "claim-result", Command: "echo executed >> executions", Status: db.StatusPending, SourceType: "noop"}
			if err := db.CreateTask(ctx, pool, task); err != nil {
				t.Fatal(err)
			}
			interval := 10 * time.Millisecond
			if mode == "stop-backoff" {
				interval = 30 * time.Second
			}
			root := t.TempDir()
			w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Name: "claim-failure-worker", Once: true,
				BackoffInitial: interval, BackoffMax: interval})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			var runErr error
			done := make(chan struct{})
			go func() { runErr = w.Run(ctx); close(done) }()
			defer func() { cancel(); <-done }()
			if mode == "stop-backoff" {
				deadline := time.Now().Add(3 * time.Second)
				for {
					var called bool
					if err := pool.QueryRow(ctx, "SELECT is_called FROM test_claim_attempts").Scan(&called); err != nil {
						t.Fatal(err)
					}
					if called && w.Pool.Stat().AcquiredConns() == 0 {
						break // Failed claim has returned; the worker is in its retry delay.
					}
					if time.Now().After(deadline) {
						t.Fatal("claim did not enter backoff")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := db.Notify(ctx, pool, db.ToWorkerChannel(w.ID), db.WorkerWakeEvent{}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("worker kept retrying or ignored stop")
			}
			var pgErr *pgconn.PgError
			if mode == "permanent" {
				if !errors.As(runErr, &pgErr) || pgErr.Code != "42501" {
					t.Fatalf("permanent claim error was lost: %v", runErr)
				}
			} else if runErr != nil {
				t.Fatal(runErr)
			}
			var count int
			if err := pool.QueryRow(ctx, "SELECT last_value FROM test_claim_attempts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			wantCount, wantStatus := 1, db.StatusPending
			if mode == "transient" {
				wantCount, wantStatus = 3, db.StatusCompleted
				data, err := os.ReadFile(filepath.Join(root, task.ID, "executions"))
				if err != nil || string(data) != "executed\n" {
					t.Fatalf("unexpected payload executions: %q %v", data, err)
				}
			} else if _, err := os.Stat(filepath.Join(root, task.ID)); !os.IsNotExist(err) {
				t.Fatalf("failed claim started execution: %v", err)
			}
			stored, err := db.GetTask(ctx, pool, task.ID, time.Minute)
			if err != nil || count != wantCount || stored == nil || stored.Status != wantStatus {
				t.Fatalf("attempts=%d task=%+v error=%v", count, stored, err)
			}
			var status db.WorkerStatus
			if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id=$1", w.ID).Scan(&status); err != nil || status != db.WorkerStatusStopped {
				t.Fatalf("worker registration was not cleaned up: %s %v", status, err)
			}
		})
	}
}

func TestWorkerStopDuringBlockedClaim(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_block_claim() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = 'running' THEN PERFORM pg_advisory_xact_lock(7654340); END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_block BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION test_block_claim()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DROP TRIGGER test_block ON tasks; DROP FUNCTION test_block_claim()")
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock(7654340)"); err != nil {
		t.Fatal(err)
	}
	task := &db.Task{ID: "blocked-claim", Command: "touch must-not-run", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Name: "blocked-claim-worker", Once: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var runErr error
	done := make(chan struct{})
	go func() { runErr = w.Run(ctx); close(done) }()
	defer func() { cancel(); gate.Rollback(context.Background()); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE wait_event='advisory' AND query LIKE '%UPDATE tasks%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claim did not reach database gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := db.Notify(ctx, pool, db.ToWorkerChannel(w.ID), db.WorkerWakeEvent{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not interrupt the blocked claim")
	}
	stored, err := db.GetTask(ctx, pool, task.ID, time.Minute)
	if err != nil || stored == nil || stored.Status != db.StatusPending {
		t.Fatalf("interrupted claim changed task state: %+v %v", stored, err)
	}
	if _, err := os.Stat(filepath.Join(root, task.ID)); !os.IsNotExist(err) {
		t.Fatalf("stopped worker started a payload: %v", err)
	}
}
