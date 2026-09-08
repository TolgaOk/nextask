package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestLoggerReportsFileFailure(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	task := &db.Task{ID: "broken-log", Command: "echo data", Status: db.StatusRunning, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	logger, err := NewTaskLogger(pool, task.ID, t.TempDir(), LogConfig{FlushLines: 32, FlushInterval: time.Hour, BufferSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	// Reopen the real output file read-only so writes fail but closing succeeds.
	name := logger.stdout.Name()
	if err := logger.stdout.Close(); err != nil {
		t.Fatal(err)
	}
	logger.stdout, err = os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	logger.Log(ctx, "stdout", "keep the DB copy")
	if err := logger.Close(); err == nil {
		t.Fatal("local write failure was silently ignored")
	}
	var output, diagnostic bool
	if err := pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT FROM task_logs WHERE task_id=$1 AND data='keep the DB copy'),
		EXISTS (SELECT FROM task_logs WHERE task_id=$1 AND stream='nextask' AND data LIKE '%write stdout log%')`, task.ID).Scan(&output, &diagnostic); err != nil || !output || !diagnostic {
		t.Fatalf("missing DB output or diagnostic: %t %t %v", output, diagnostic, err)
	}
}
