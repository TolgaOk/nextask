package worker

import (
	"context"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestDBLogger_LogsStdout(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log001",
		Command:    "echo hello && echo world",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stdout'", task.ID).Scan(&count)
	if count < 2 {
		t.Errorf("stdout count = %d, want >= 2", count)
	}

	var data string
	pool.QueryRow(ctx, "SELECT data FROM task_logs WHERE task_id = $1 AND stream = 'stdout' ORDER BY id LIMIT 1", task.ID).Scan(&data)
	if data != "hello" {
		t.Errorf("first stdout = %s, want hello", data)
	}
}

func TestDBLogger_LogsStderr(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log002",
		Command:    "echo error_msg >&2",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stderr'", task.ID).Scan(&count)
	if count != 1 {
		t.Errorf("stderr count = %d, want 1", count)
	}

	var data string
	pool.QueryRow(ctx, "SELECT data FROM task_logs WHERE task_id = $1 AND stream = 'stderr'", task.ID).Scan(&data)
	if data != "error_msg" {
		t.Errorf("stderr = %s, want error_msg", data)
	}
}

func TestDBLogger_LogsBothStreams(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log003",
		Command:    "echo out && echo err >&2",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var stdoutCount, stderrCount int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stdout'", task.ID).Scan(&stdoutCount)
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stderr'", task.ID).Scan(&stderrCount)

	if stdoutCount == 0 {
		t.Error("stdout not captured")
	}
	if stderrCount == 0 {
		t.Error("stderr not captured")
	}
}

func TestDBLogger_LogsErrorOnSourceFailure(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log004",
		Command:    "echo test",
		Status:     db.StatusPending,
		SourceType: "unknown",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'nextask' AND data LIKE '%[error]%'", task.ID).Scan(&count)
	if count == 0 {
		t.Error("error log not captured for source failure")
	}
}

