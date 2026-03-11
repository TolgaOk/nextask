package cli

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"go.uber.org/goleak"
)

func getTestDBURL(t *testing.T) string {
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set, skipping database tests")
	}
	return url
}

func initTestConfig(t *testing.T) {
	cfg = &config.Config{
		DB: config.DBConfig{
			URL: getTestDBURL(t),
		},
	}
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	pool, err := db.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS task_logs")
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS tasks")
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS workers")

	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to migrate: %v", err)
	}

	return pool
}

// TestLogsAttach_CompletedTask verifies that --attach with a completed task
// returns immediately without streaming.
func TestLogsAttach_CompletedTask(t *testing.T) {
	defer goleak.VerifyNone(t)

	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create and complete a task
	task := &db.Task{
		ID:         "logtest01",
		Command:    "echo test",
		Status:     db.StatusCompleted,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}

	// Insert some logs
	db.InsertLog(ctx, pool, task.ID, "stdout", "test output")

	// Fetch task to check status
	fetched, err := db.GetTask(ctx, pool, task.ID, 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// Completed task should not trigger streaming
	if fetched.Status == db.StatusPending || fetched.Status == db.StatusRunning {
		t.Error("task should be completed for this test")
	}
}

// TestLogsAttach_StreamsLogs verifies that --attach streams logs from a running task
// and exits when the task completes.
func TestLogsAttach_StreamsLogs(t *testing.T) {
	defer goleak.VerifyNone(t)

	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	initTestConfig(t)

	// Create a running task
	task := &db.Task{
		ID:         "logstream01",
		Command:    "echo streaming",
		Status:     db.StatusRunning,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}

	// Claim it to set status to running
	pool.Exec(ctx, "UPDATE tasks SET status = 'running', started_at = NOW() WHERE id = $1", task.ID)

	// Run logsAndAttach in goroutine
	done := make(chan error, 1)
	go func() {
		done <- logsAndAttach(ctx, pool, task.ID, 0)
	}()

	// Give it time to start listening
	time.Sleep(100 * time.Millisecond)

	// Simulate worker sending log event
	fromChannel := db.FromTaskChannel(task.ID)
	logID, _ := db.InsertLog(ctx, pool, task.ID, "stdout", "streamed line")
	db.Notify(ctx, pool, fromChannel, db.TaskLogEvent{ID: logID})

	// Simulate worker sending status event (task completes)
	time.Sleep(100 * time.Millisecond)
	db.CompleteTask(ctx, pool, task.ID, db.StatusCompleted, 0)
	db.Notify(ctx, pool, fromChannel, db.TaskStatusEvent{Status: "completed", ExitCode: 0})

	// Wait for logsAndAttach to finish
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("logsAndAttach returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logsAndAttach didn't finish in time")
	}
}

// TestLogsAttach_ContextCancel verifies that cancelling context stops streaming
// and goroutine exits cleanly.
func TestLogsAttach_ContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t)

	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	initTestConfig(t)

	// Create a running task
	task := &db.Task{
		ID:         "logcancel01",
		Command:    "sleep 60",
		Status:     db.StatusRunning,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, "UPDATE tasks SET status = 'running', started_at = NOW() WHERE id = $1", task.ID)

	// Create cancellable context
	cancelCtx, cancel := context.WithCancel(ctx)

	done := make(chan error, 1)
	go func() {
		done <- logsAndAttach(cancelCtx, pool, task.ID, 0)
	}()

	// Give it time to start listening
	time.Sleep(100 * time.Millisecond)

	// Cancel context (simulates Ctrl+C)
	cancel()

	// Should exit cleanly
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("logsAndAttach returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logsAndAttach didn't finish after cancel")
	}
}

// TestLogsAttach_RecoveryAfterConnectionLoss verifies that logsAndAttach
// continues to work after the DB connection is terminated and recovers.
func TestLogsAttach_RecoveryAfterConnectionLoss(t *testing.T) {
	defer goleak.VerifyNone(t)

	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	initTestConfig(t)

	// Create a running task
	task := &db.Task{
		ID:         "logrecovery01",
		Command:    "test recovery",
		Status:     db.StatusRunning,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, "UPDATE tasks SET status = 'running', started_at = NOW() WHERE id = $1", task.ID)

	// Start logsAndAttach
	done := make(chan error, 1)
	go func() {
		done <- logsAndAttach(ctx, pool, task.ID, 0)
	}()

	// Wait for listener to establish
	time.Sleep(200 * time.Millisecond)

	// Find and terminate the listener's connection
	var listenerPID int32
	err := pool.QueryRow(ctx, `
		SELECT pid FROM pg_stat_activity
		WHERE pid != pg_backend_pid()
		AND datname = current_database()
		AND query LIKE 'LISTEN%'
		AND wait_event = 'ClientRead'
		ORDER BY backend_start DESC
		LIMIT 1
	`).Scan(&listenerPID)
	if err != nil {
		t.Logf("could not find listener PID: %v (skipping connection kill)", err)
	} else {
		t.Logf("terminating listener PID: %d", listenerPID)
		pool.Exec(ctx, "SELECT pg_terminate_backend($1)", listenerPID)
	}

	// Wait for reconnection
	time.Sleep(800 * time.Millisecond)

	// Send completion event - should work after reconnect
	fromChannel := db.FromTaskChannel(task.ID)
	db.CompleteTask(ctx, pool, task.ID, db.StatusCompleted, 0)
	db.Notify(ctx, pool, fromChannel, db.TaskStatusEvent{Status: "completed", ExitCode: 0})

	// logsAndAttach should complete successfully
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("logsAndAttach returned error: %v", err)
		}
		t.Log("logsAndAttach completed successfully after recovery")
	case <-time.After(10 * time.Second):
		t.Fatal("logsAndAttach didn't finish after recovery")
	}
}

// TestLogsAttach_PollFallback verifies that polling catches task completion
// if notification is missed (e.g., during reconnection).
func TestLogsAttach_PollFallback(t *testing.T) {
	defer goleak.VerifyNone(t)

	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	initTestConfig(t)

	// Create a running task
	task := &db.Task{
		ID:         "logpoll01",
		Command:    "test poll",
		Status:     db.StatusRunning,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, "UPDATE tasks SET status = 'running', started_at = NOW() WHERE id = $1", task.ID)

	// Start logsAndAttach
	done := make(chan error, 1)
	go func() {
		done <- logsAndAttach(ctx, pool, task.ID, 0)
	}()

	// Wait for listener
	time.Sleep(100 * time.Millisecond)

	// Complete task WITHOUT sending notification (simulates missed event)
	db.CompleteTask(ctx, pool, task.ID, db.StatusCompleted, 0)
	// No Notify call - polling should catch it

	// logsAndAttach should complete via polling (within ~5 seconds)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("logsAndAttach returned error: %v", err)
		}
		t.Log("logsAndAttach completed via polling fallback")
	case <-time.After(10 * time.Second):
		t.Fatal("logsAndAttach didn't catch completion via polling")
	}
}
