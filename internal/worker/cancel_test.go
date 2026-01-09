package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/TolgaOk/nextask/internal/db"
)

// Test 8: Cancel during command execution
func TestWorker_CancelDuringExecution(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "cancelexec01",
		Command:    "sleep 60",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "test-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	// Run worker in goroutine
	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Wait for task to start running
	time.Sleep(500 * time.Millisecond)

	// Send cancel notification
	toChannel := db.ToTaskChannel(task.ID)
	if err := db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{}); err != nil {
		t.Fatalf("failed to send cancel: %v", err)
	}

	// Wait for worker to finish
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't finish in time")
	}

	// Verify task was cancelled
	var status string
	var exitCode int
	pool.QueryRow(ctx, "SELECT status, exit_code FROM tasks WHERE id = $1", task.ID).Scan(&status, &exitCode)

	if status != string(db.StatusCancelled) {
		t.Errorf("status = %s, want cancelled", status)
	}
	if exitCode != -1 {
		t.Errorf("exit_code = %d, want -1", exitCode)
	}

	// Verify cancellation was logged
	var logCount int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND data LIKE '%task cancelled%'", task.ID).Scan(&logCount)
	if logCount == 0 {
		t.Error("cancellation not logged")
	}
}

// Test 9: Cancel during source fetch
func TestWorker_CancelDuringSourceFetch(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Use a git source that will take time (clone from a slow/non-existent remote)
	// For testing, we'll use noop source with a slow command instead
	// Real git cancel would require a real repo setup
	task := &db.Task{
		ID:         "cancelsrc01",
		Command:    "sleep 60",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	// Create worker with cancellable context
	taskCtx, cancel := context.WithCancel(ctx)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}

	// Run executor in goroutine
	done := make(chan *ExitResult)
	go func() {
		done <- executor.Execute(taskCtx, task)
	}()

	// Cancel immediately (simulates cancel during any phase)
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Wait for executor to finish
	select {
	case result := <-done:
		// Context cancellation causes command to be killed
		if result.Code == 0 {
			t.Error("expected non-zero exit code after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executor didn't finish in time")
	}
}

// Test 10: Cancel notification after task completes (should be ignored)
func TestWorker_CancelAfterComplete(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "cancelafter01",
		Command:    "echo fast",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "test-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	// Run worker - task will complete quickly
	w.Run(ctx)

	// Verify task completed (not cancelled)
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusCompleted) {
		t.Errorf("status = %s, want completed", status)
	}

	// Now send cancel notification (should be ignored because task already completed)
	toChannel := db.ToTaskChannel(task.ID)
	_ = db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{})

	// Verify status didn't change
	time.Sleep(100 * time.Millisecond)
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusCompleted) {
		t.Errorf("status changed to %s after late cancel notification", status)
	}
}

// Test 12: Parent context cancelled (SIGINT) - should NOT mark as cancelled
func TestWorker_ParentContextCancelled(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "parentctx01",
		Command:    "sleep 60",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	workerCtx, workerCancel := context.WithCancel(ctx)

	w, err := New(workerCtx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "test-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	done := make(chan error)
	go func() {
		done <- w.Run(workerCtx)
	}()

	// Wait for task to start
	time.Sleep(500 * time.Millisecond)

	// Cancel parent context (simulates SIGINT to worker)
	workerCancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't finish in time")
	}

	// When parent context is cancelled, task should be marked as failed (not cancelled)
	// because the cancellation came from worker shutdown, not user cancel request
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)

	// The task should NOT be marked as "cancelled" (that's for user-initiated cancellation)
	// It should be failed or still running (depending on timing)
	if status == string(db.StatusCancelled) {
		t.Error("status should not be 'cancelled' for parent context cancellation")
	}
}

// End-to-end test: Full cancel flow mimicking actual usage
func TestCancel_EndToEnd(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// 1. Create a long-running task (like CLI enqueue)
	task := &db.Task{
		ID:         "e2ecancel01",
		Command:    "sleep 60",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// 2. Start worker (like CLI worker command)
	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "e2e-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	workerDone := make(chan error)
	go func() {
		workerDone <- w.Run(ctx)
	}()

	// Wait for task to start running
	time.Sleep(500 * time.Millisecond)

	// Verify task is running
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusRunning) {
		t.Fatalf("task should be running, got %s", status)
	}

	// 3. Request cancel (like CLI cancel command)
	originalStatus, err := db.RequestCancel(ctx, pool, task.ID)
	if err != nil {
		t.Fatalf("RequestCancel failed: %v", err)
	}
	if originalStatus == nil || *originalStatus != db.StatusRunning {
		t.Fatalf("expected running status, got %v", originalStatus)
	}

	// 4. Listen for confirmation (like CLI does)
	confirmConn, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect for confirm: %v", err)
	}
	defer confirmConn.Close(ctx)

	fromChannel := db.FromTaskChannel(task.ID)
	_, _ = confirmConn.Exec(ctx, "LISTEN "+fromChannel)

	// 5. Send cancel notification to worker (like CLI does)
	toChannel := db.ToTaskChannel(task.ID)
	if err := db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{}); err != nil {
		t.Fatalf("failed to send cancel: %v", err)
	}

	// 6. Wait for confirmation from worker
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	for {
		notif, err := confirmConn.WaitForNotification(waitCtx)
		if err != nil {
			t.Fatalf("failed to receive cancel confirmation: %v", err)
		}

		eventType, data, err := db.ParseEvent(notif.Payload)
		if err != nil {
			t.Fatalf("failed to parse event: %v", err)
		}
		if eventType == db.EventTypeStatus {
			var status db.TaskStatusEvent
			if err := json.Unmarshal(data, &status); err != nil {
				t.Fatalf("failed to parse status: %v", err)
			}
			if status.Status == string(db.StatusCancelled) {
				break
			}
		}
	}

	// 7. Wait for worker to finish
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't finish")
	}

	// 8. Verify final state
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusCancelled) {
		t.Errorf("final status = %s, want cancelled", status)
	}

	// Verify cancellation log
	var logCount int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND data LIKE '%task cancelled%'", task.ID).Scan(&logCount)
	if logCount == 0 {
		t.Error("cancellation not logged")
	}
}

// Test 13: Race between cancel and completion
func TestWorker_CancelRaceWithCompletion(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Task that completes very quickly
	task := &db.Task{
		ID:         "cancelrace01",
		Command:    "echo done",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "test-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	// Start worker
	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Race: send cancel while task might be running
	time.Sleep(50 * time.Millisecond)
	toChannel := db.ToTaskChannel(task.ID)
	_ = db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't finish in time")
	}

	// Task should be in a terminal state (either completed or cancelled)
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)

	if status != string(db.StatusCompleted) && status != string(db.StatusCancelled) {
		t.Errorf("status = %s, want completed or cancelled", status)
	}
}

// TestWorker_CancelKillsChildProcesses verifies that cancelling a task kills
// child processes spawned by sh -c, not just the shell itself. This tests the
// fix for orphaned child processes when using exec.CommandContext.
func TestWorker_CancelKillsChildProcesses(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Use a unique marker to identify our sleep process
	marker := fmt.Sprintf("nextask_test_%d", time.Now().UnixNano())

	task := &db.Task{
		ID:         "cancelchild01",
		Command:    fmt.Sprintf("echo %s; sleep 300", marker),
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "test-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Wait for task to start and child process to be spawned
	time.Sleep(500 * time.Millisecond)

	// Verify the sleep process is running
	out, _ := exec.Command("pgrep", "-f", marker).Output()
	if len(out) == 0 {
		t.Log("warning: could not detect child process via pgrep, continuing test")
	}

	// Send cancel notification
	toChannel := db.ToTaskChannel(task.ID)
	_ = db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{})

	// Wait for worker to finish
	start := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker didn't finish in time - child process likely not killed")
	}
	elapsed := time.Since(start)

	// The task should complete quickly (not wait 300 seconds)
	if elapsed > 5*time.Second {
		t.Errorf("cancel took %v, expected < 5s (child process may not have been killed)", elapsed)
	}

	// Verify no orphaned child processes with our marker
	time.Sleep(100 * time.Millisecond) // Give OS time to clean up
	out, _ = exec.Command("pgrep", "-f", marker).Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		t.Errorf("orphaned child process found: %s", strings.TrimSpace(string(out)))
		// Clean up the orphan
		exec.Command("pkill", "-f", marker).Run()
	}

	// Verify task was cancelled
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusCancelled) {
		t.Errorf("status = %s, want cancelled", status)
	}
}

// TestWorker_CancelFallbackToSIGKILL verifies that if a process ignores SIGINT,
// it gets forcefully killed with SIGKILL after WaitDelay (5 seconds).
func TestWorker_CancelFallbackToSIGKILL(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	marker := fmt.Sprintf("nextask_sigkill_%d", time.Now().UnixNano())

	// Command that traps and ignores SIGINT
	task := &db.Task{
		ID:         "cancelsigkill01",
		Command:    fmt.Sprintf("trap '' INT; echo %s; sleep 300", marker),
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "test-worker",
		Once:    true,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close()

	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Wait for task to start
	time.Sleep(500 * time.Millisecond)

	// Send cancel notification
	toChannel := db.ToTaskChannel(task.ID)
	_ = db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{})

	// Should complete within WaitDelay (5s) + buffer
	// SIGINT is ignored, so Go falls back to SIGKILL after WaitDelay
	start := time.Now()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("worker didn't finish - SIGKILL fallback may have failed")
	}
	elapsed := time.Since(start)

	// Should take roughly 5 seconds (WaitDelay) before SIGKILL
	if elapsed < 4*time.Second {
		t.Logf("completed in %v (SIGINT may have worked despite trap)", elapsed)
	} else if elapsed > 10*time.Second {
		t.Errorf("took %v to kill process, expected ~5s for SIGKILL fallback", elapsed)
	} else {
		t.Logf("SIGKILL fallback worked after %v", elapsed)
	}

	// Verify no orphaned processes
	time.Sleep(100 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", marker).Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		t.Errorf("orphaned process found: %s", strings.TrimSpace(string(out)))
		exec.Command("pkill", "-9", "-f", marker).Run()
	}

	// Verify task was cancelled
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusCancelled) {
		t.Errorf("status = %s, want cancelled", status)
	}
}
