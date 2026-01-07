package worker

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/TolgaOk/nextask/internal/db"
)

// TestWorker_StopWhileIdle tests that a worker responds to stop notification
// when it's idle (not processing a task).
func TestWorker_StopWhileIdle(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "stop-idle-worker",
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close(ctx)

	// Run worker in goroutine
	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Wait for worker to start and begin listening
	time.Sleep(200 * time.Millisecond)

	// Verify worker is registered
	workers, err := db.ListWorkers(ctx, pool, nil)
	if err != nil {
		t.Fatalf("failed to list workers: %v", err)
	}
	var found bool
	for _, wr := range workers {
		if wr.ID == "stop-idle-worker" && wr.Status == db.WorkerStatusRunning {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("worker not registered as running")
	}

	// Send stop notification via pg_notify
	toWorkerCh := db.ToWorkerChannel(w.ID)
	_, err = pool.Exec(ctx, "SELECT pg_notify($1, '')", toWorkerCh)
	if err != nil {
		t.Fatalf("failed to send stop notification: %v", err)
	}

	// Wait for worker to exit
	select {
	case <-done:
		// Worker exited successfully
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't stop in time")
	}

	// Verify worker unregistered (status = stopped)
	time.Sleep(100 * time.Millisecond)
	workers, _ = db.ListWorkers(ctx, pool, nil)
	for _, wr := range workers {
		if wr.ID == "stop-idle-worker" {
			if wr.Status != db.WorkerStatusStopped {
				t.Errorf("worker status = %s, want stopped", wr.Status)
			}
			return
		}
	}
	t.Error("worker record not found after stop")
}

// TestWorker_StopDuringTaskExecution tests that stopping a worker during task
// execution cancels the running task first, then exits gracefully.
func TestWorker_StopDuringTaskExecution(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create a long-running task
	task := &db.Task{
		ID:         "stopexec01",
		Command:    "sleep 60",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "stop-busy-worker",
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close(ctx)

	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Wait for task to start running
	time.Sleep(500 * time.Millisecond)

	// Verify task is running
	var status string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status != string(db.StatusRunning) {
		t.Fatalf("task should be running, got %s", status)
	}

	// Send stop notification to worker
	toWorkerCh := db.ToWorkerChannel(w.ID)
	_, err = pool.Exec(ctx, "SELECT pg_notify($1, '')", toWorkerCh)
	if err != nil {
		t.Fatalf("failed to send stop notification: %v", err)
	}

	// Wait for worker to exit
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker didn't stop in time")
	}

	// Task should be failed (not cancelled, since it wasn't user-initiated cancel)
	// The parent context was cancelled, which kills the task
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status)
	if status == string(db.StatusRunning) || status == string(db.StatusPending) {
		t.Errorf("task should be in terminal state, got %s", status)
	}

	// Verify worker unregistered
	workers, _ := db.ListWorkers(ctx, pool, nil)
	for _, wr := range workers {
		if wr.ID == "stop-busy-worker" {
			if wr.Status != db.WorkerStatusStopped {
				t.Errorf("worker status = %s, want stopped", wr.Status)
			}
			return
		}
	}
}

// TestWorker_StopEndToEnd tests the full stop flow as it would be used by CLI.
func TestWorker_StopEndToEnd(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// 1. Start a worker
	w, err := New(ctx, Config{
		DBURL:   getTestDBURL(t),
		Workdir: t.TempDir(),
		Name:    "e2e-stop-worker",
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	defer w.Close(ctx)

	done := make(chan error)
	go func() {
		done <- w.Run(ctx)
	}()

	// Wait for worker to start
	time.Sleep(200 * time.Millisecond)

	// 2. Verify worker is visible via ListWorkers (like CLI worker list)
	status := db.WorkerStatusRunning
	workers, _ := db.ListWorkers(ctx, pool, &status)
	var found bool
	for _, wr := range workers {
		if wr.ID == "e2e-stop-worker" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("worker not visible in list")
	}

	// 3. Set up listener for stop confirmation (like CLI does)
	listenConn, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect for listen: %v", err)
	}
	defer listenConn.Close(ctx)

	fromWorkerCh := db.FromWorkerChannel("e2e-stop-worker")
	if _, err := listenConn.Exec(ctx, `LISTEN "`+fromWorkerCh+`"`); err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	// 4. Send stop (like CLI worker stop would do)
	toWorkerCh := db.ToWorkerChannel("e2e-stop-worker")
	_, err = pool.Exec(ctx, "SELECT pg_notify($1, '')", toWorkerCh)
	if err != nil {
		t.Fatalf("failed to send stop: %v", err)
	}

	// 5. Wait for stop confirmation notification
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	notif, err := listenConn.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("failed to receive stop confirmation: %v", err)
	}
	if notif.Payload != "stopped" {
		t.Errorf("expected payload 'stopped', got '%s'", notif.Payload)
	}

	// 6. Wait for worker to exit
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't stop")
	}

	// 7. Verify worker is now stopped in DB
	time.Sleep(100 * time.Millisecond)
	stoppedStatus := db.WorkerStatusStopped
	workers, _ = db.ListWorkers(ctx, pool, &stoppedStatus)
	found = false
	var workerPID int
	for _, wr := range workers {
		if wr.ID == "e2e-stop-worker" {
			found = true
			workerPID = wr.PID
			break
		}
	}
	if !found {
		t.Error("worker not in stopped list after stop")
	}

	// 8. Verify worker process is no longer running
	// Note: In tests, the worker runs in a goroutine (same process), not a separate process.
	// This check would be meaningful for daemon workers.
	_ = workerPID // PID is the test process itself in this case
}
