package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecutorLogsCancellationCleanup(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	task := &db.Task{ID: "cleanup-logs", Command: `trap 'echo cleanup-out; echo cleanup-err >&2; exit 0' INT; echo ready > ready; while :; do sleep 1; done`, Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	executor := &Executor{Pool: pool, Workdir: root, LogFlushLines: 16, LogFlushInterval: 10 * time.Millisecond, LogBufferSize: 64}
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan *ExitResult, 1)
	go func() { done <- executor.Execute(taskCtx, task) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, task.ID, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("payload did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case result := <-done:
		if result.Code == 0 {
			t.Fatal("cancelled execution reported success")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancellation hung")
	}
	lines, err := db.GetLogsSince(ctx, pool, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, line := range lines {
		found[line.Stream+":"+line.Data] = true
	}
	for _, value := range []string{"stdout:cleanup-out", "stderr:cleanup-err"} {
		if !found[value] {
			t.Errorf("missing %s", value)
		}
	}
	for file, want := range map[string]string{"out.txt": "cleanup-out", "err.txt": "cleanup-err"} {
		data, err := os.ReadFile(filepath.Join(root, task.ID, ".nextask", "log", file))
		if err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("missing local cleanup output: %v", err)
		}
	}
}

func TestExecutorLogsInvalidUTF8(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	task := &db.Task{ID: "binary-logs", Command: `printf 'bad\377\000\n'; printf 'valid-after-binary\n'`, Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	executor := &Executor{Pool: pool, Workdir: root, LogFlushLines: 16, LogFlushInterval: time.Second, LogBufferSize: 64}
	if result := executor.Execute(ctx, task); result.Code != 0 {
		t.Fatal(result)
	}
	lines, err := db.GetLogsSince(ctx, pool, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var output []string
	for _, line := range lines {
		if line.Stream == "stdout" {
			output = append(output, line.Data)
		}
	}
	if strings.Join(output, "\n") != "bad\uFFFD\nvalid-after-binary" {
		t.Fatalf("invalid log conversion: %q", output)
	}
	raw, err := os.ReadFile(filepath.Join(root, task.ID, ".nextask", "log", "out.txt"))
	if err != nil || string(raw) != "bad\xff\x00\nvalid-after-binary\n" {
		t.Fatalf("local raw bytes changed: %q %v", raw, err)
	}
}

func TestLoggerCloseRecoversDatabaseConnection(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	task := &db.Task{ID: "outage-logs", Command: "echo final", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(getTestDBURL(t))
	if err != nil {
		t.Fatal(err)
	}
	var blocked atomic.Bool
	blocked.Store(true)
	attempted := make(chan struct{}, 1)
	cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		if blocked.Load() {
			select {
			case attempted <- struct{}{}:
			default:
			}
			return errors.New("connection refused")
		}
		return nil
	}
	outagePool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer outagePool.Close()
	logger, err := NewTaskLogger(outagePool, task.ID, t.TempDir(), LogConfig{FlushLines: 32, FlushInterval: time.Hour, BufferSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	logger.Log(ctx, "stdout", "last line during outage")
	done := make(chan error, 1)
	go func() { done <- logger.Close() }()
	select {
	case <-attempted:
	case <-time.After(3 * time.Second):
		t.Fatal("final flush did not attempt a connection")
	}
	blocked.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown flush did not recover")
	}
	lines, err := db.GetLogsSince(ctx, pool, task.ID, 0)
	if err != nil || len(lines) != 1 || lines[0].Data != "last line during outage" {
		t.Fatalf("shutdown logs lost: %+v %v", lines, err)
	}
}
