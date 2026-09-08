package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type watchProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	output string
}

func startWatchCLI(t *testing.T, binary string, args ...string) *watchProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	p := &watchProcess{cmd: exec.CommandContext(ctx, binary, args...), done: make(chan struct{})}
	p.cmd.Dir = t.TempDir()
	p.cmd.Env = append(isolatedCLIEnv(t), "NEXTASK_DB_URL="+getTestDBURL(t), "GORACE=atexit_sleep_ms=0")
	p.output = filepath.Join(p.cmd.Dir, "output")
	f, err := os.Create(p.output)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	p.cmd.Stdout, p.cmd.Stderr = f, f
	if err := p.cmd.Start(); err != nil {
		cancel()
		f.Close()
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { cancel(); <-p.done; f.Close() })
	return p
}

func (p *watchProcess) result(t *testing.T, code int) string {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		out, _ := os.ReadFile(p.output)
		t.Fatalf("CLI did not finish: %s", out)
	}
	actual := 0
	if p.err != nil {
		var exit *exec.ExitError
		if !errors.As(p.err, &exit) {
			t.Fatal(p.err)
		}
		actual = exit.ExitCode()
	}
	out, err := os.ReadFile(p.output)
	if err != nil {
		t.Fatal(err)
	}
	if actual != code {
		t.Fatalf("exit = %d, want %d: %s", actual, code, out)
	}
	return string(out)
}

func (p *watchProcess) stillRunning(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		out, _ := os.ReadFile(p.output)
		t.Fatalf("CLI exited early: %v: %s", p.err, out)
	case <-time.After(150 * time.Millisecond):
	}
}

func awaitWatchListener(t *testing.T, pool *pgxpool.Pool, channel string) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	query := ""
	if channel != "" {
		query = `LISTEN "` + channel + `"`
	}
	for ctx.Err() == nil {
		var pid int32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity
			WHERE pid != pg_backend_pid() AND datname = current_database()
			AND (query = $1 OR ($1 = '' AND query LIKE 'LISTEN%')) AND wait_event = 'ClientRead' ORDER BY backend_start DESC LIMIT 1`,
			query).Scan(&pid)
		if err == nil {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener not ready: %s", channel)
	return 0
}

func createWatchTask(t *testing.T, pool *pgxpool.Pool, id string, tags map[string]string) {
	t.Helper()
	if err := db.CreateTask(context.Background(), pool, &db.Task{ID: id, Command: "sleep 60", Status: db.StatusPending, SourceType: "noop", Tags: tags}); err != nil {
		t.Fatal(err)
	}
}

func completeWatchTask(t *testing.T, pool *pgxpool.Pool, id string, code int, notify bool) {
	t.Helper()
	status := db.StatusCompleted
	if code != 0 {
		status = db.StatusFailed
	}
	ctx := context.Background()
	if err := db.CompleteTask(ctx, pool, id, status, code); err != nil {
		t.Fatal(err)
	}
	if notify {
		if err := db.Notify(ctx, pool, db.FromTaskChannel(id), db.TaskStatusEvent{Status: string(status), ExitCode: code}); err != nil {
			t.Fatal(err)
		}
	}
}
