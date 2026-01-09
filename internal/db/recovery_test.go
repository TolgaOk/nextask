package db

import (
	"context"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"
)

// terminateBackend kills a PostgreSQL backend connection by PID.
func terminateBackend(ctx context.Context, pool *pgxpool.Pool, pid int32) error {
	_, err := pool.Exec(ctx, "SELECT pg_terminate_backend($1)", pid)
	return err
}

// findListenerPID finds the PID of a connection listening on a specific channel.
func findListenerPID(ctx context.Context, pool *pgxpool.Pool, channel string) (int32, error) {
	var pid int32
	err := pool.QueryRow(ctx, `
		SELECT pid FROM pg_stat_activity
		WHERE pid != pg_backend_pid()
		AND datname = current_database()
		AND query LIKE 'LISTEN%'
		AND wait_event = 'ClientRead'
		ORDER BY backend_start DESC
		LIMIT 1
	`).Scan(&pid)
	return pid, err
}

// === Listener Recovery Tests ===

func TestListener_ReconnectAfterBackendTerminate(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	// Control pool to terminate connections and send notifications
	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to create control pool: %v", err)
	}
	defer controlPool.Close()

	// Create listener with fast backoff for test
	listener, err := Listen(ctx, dbURL, NewBackOff(50*time.Millisecond, 500*time.Millisecond), "test_reconnect")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close(context.Background())

	// Wait for listener to establish connection
	time.Sleep(50 * time.Millisecond)

	// Find listener's PID via pg_stat_activity
	listenerPID, err := findListenerPID(ctx, controlPool, "test_reconnect")
	if err != nil {
		t.Fatalf("failed to find listener PID: %v", err)
	}
	t.Logf("listener PID: %d", listenerPID)

	// Kill the listener's connection
	if err := terminateBackend(ctx, controlPool, listenerPID); err != nil {
		t.Fatalf("failed to terminate backend: %v", err)
	}
	t.Log("terminated listener backend")

	// Wait for reconnection
	time.Sleep(500 * time.Millisecond)

	// Send notification - listener should receive it after reconnecting
	_, err = controlPool.Exec(ctx, "NOTIFY test_reconnect, 'after_reconnect'")
	if err != nil {
		t.Fatalf("NOTIFY failed: %v", err)
	}

	select {
	case notif := <-listener.C:
		if notif == nil {
			t.Fatal("received nil notification (channel closed unexpectedly)")
		}
		if notif.Payload != "after_reconnect" {
			t.Errorf("expected payload 'after_reconnect', got %q", notif.Payload)
		}
		t.Log("received notification after reconnect")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for notification after reconnect")
	}
}

func TestListener_ReconnectMultipleTerminations(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to create control pool: %v", err)
	}
	defer controlPool.Close()

	listener, err := Listen(ctx, dbURL, NewBackOff(50*time.Millisecond, 500*time.Millisecond), "test_multi_term")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close(context.Background())

	for i := 0; i < 3; i++ {
		// Wait for connection to be established
		time.Sleep(100 * time.Millisecond)

		// Find listener PID via pg_stat_activity
		pid, err := findListenerPID(ctx, controlPool, "test_multi_term")
		if err != nil {
			t.Fatalf("iteration %d: failed to find PID: %v", i, err)
		}
		t.Logf("iteration %d: PID=%d", i, pid)

		// Terminate backend
		if err := terminateBackend(ctx, controlPool, pid); err != nil {
			t.Fatalf("iteration %d: failed to terminate: %v", i, err)
		}

		// Wait for reconnection
		time.Sleep(500 * time.Millisecond)

		// Verify we can receive notifications
		payload := "reconnect_" + string(rune('0'+i))
		_, err = controlPool.Exec(ctx, "SELECT pg_notify('test_multi_term', $1)", payload)
		if err != nil {
			t.Fatalf("iteration %d: NOTIFY failed: %v", i, err)
		}

		select {
		case notif := <-listener.C:
			if notif == nil {
				t.Fatalf("iteration %d: channel closed unexpectedly", i)
			}
			if notif.Payload != payload {
				t.Errorf("iteration %d: expected %q, got %q", i, payload, notif.Payload)
			}
			t.Logf("iteration %d: received %q", i, notif.Payload)
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: timeout", i)
		}
	}
}

func TestListener_ContinuesAfterBriefOutage(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to create control pool: %v", err)
	}
	defer controlPool.Close()

	listener, err := Listen(ctx, dbURL, NewBackOff(50*time.Millisecond, 500*time.Millisecond), "test_outage")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close(context.Background())

	// Send notification before outage
	controlPool.Exec(ctx, "NOTIFY test_outage, 'before'")
	select {
	case notif := <-listener.C:
		if notif.Payload != "before" {
			t.Errorf("expected 'before', got %q", notif.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for 'before'")
	}

	// Wait for listener to be back in waiting state
	time.Sleep(50 * time.Millisecond)

	// Simulate outage by terminating connection
	pid, err := findListenerPID(ctx, controlPool, "test_outage")
	if err != nil {
		t.Fatalf("failed to find listener PID: %v", err)
	}
	terminateBackend(ctx, controlPool, pid)
	t.Log("simulated outage")

	// Wait for recovery
	time.Sleep(500 * time.Millisecond)

	// Send notification after recovery
	controlPool.Exec(ctx, "NOTIFY test_outage, 'after'")
	select {
	case notif := <-listener.C:
		if notif == nil {
			t.Fatal("channel closed")
		}
		if notif.Payload != "after" {
			t.Errorf("expected 'after', got %q", notif.Payload)
		}
		t.Log("received 'after' successfully")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for 'after'")
	}
}

func TestListener_CloseDuringReconnect(t *testing.T) {
	defer goleak.VerifyNone(t)

	dbURL := getTestDBURL(t)
	ctx, cancel := context.WithCancel(context.Background())

	controlPool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("failed to create control pool: %v", err)
	}
	defer controlPool.Close()

	// Use slow backoff to ensure we're in reconnect state when we cancel
	listener, err := Listen(ctx, dbURL, NewBackOff(2*time.Second, 10*time.Second), "test_close_reconnect")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Get PID and terminate
	pid, err := findListenerPID(context.Background(), controlPool, "test_close_reconnect")
	if err != nil {
		t.Fatalf("failed to find listener PID: %v", err)
	}
	terminateBackend(context.Background(), controlPool, pid)
	t.Log("terminated, now waiting briefly before cancel")

	// Wait for reconnect attempt to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context - should interrupt reconnect
	cancel()

	// Close should complete without hanging
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()

	if err := listener.Close(closeCtx); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	t.Log("listener closed successfully during reconnect")
}

// === Retry with Real DB Operations ===

func TestRetry_CompleteTaskAfterPoolRecovery(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	pool := setupTestDB(t)
	defer pool.Close()

	// Create and claim task
	task := &Task{ID: "retry_complete", Command: "test", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimTask(ctx, pool, "w1", &WorkerInfo{Hostname: "test"}, nil); err != nil {
		t.Fatal(err)
	}

	// Create a separate connection that we'll terminate
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var connPID int32
	conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&connPID)
	conn.Release()

	// Create control pool
	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer controlPool.Close()

	// Terminate one backend to force pool to get new connection
	terminateBackend(ctx, controlPool, connPID)

	// CompleteTask with retry should work (pool will get new connection)
	err = Retry(ctx, func() error {
		return CompleteTask(ctx, pool, "retry_complete", StatusCompleted, 0)
	}, backoff.WithBackOff(NewBackOff(50*time.Millisecond, 500*time.Millisecond)))

	if err != nil {
		t.Fatalf("CompleteTask with retry failed: %v", err)
	}

	// Verify completion
	updatedTask, _ := GetTask(ctx, pool, "retry_complete")
	if updatedTask.Status != StatusCompleted {
		t.Errorf("status = %s, want completed", updatedTask.Status)
	}
}

func TestRetry_HeartbeatRecovery(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	pool := setupTestDB(t)
	defer pool.Close()

	// Register worker
	if err := RegisterWorker(ctx, pool, "hb_retry", 1234, "testhost", "/tmp"); err != nil {
		t.Fatal(err)
	}

	// Get initial heartbeat
	workers, _ := ListWorkers(ctx, pool, nil)
	if len(workers) == 0 {
		t.Fatal("worker not found")
	}
	initialHeartbeat := workers[0].LastHeartbeat

	// Create control pool
	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer controlPool.Close()

	// Terminate a connection in the pool
	conn, _ := pool.Acquire(ctx)
	var connPID int32
	conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&connPID)
	conn.Release()
	terminateBackend(ctx, controlPool, connPID)

	// Wait briefly
	time.Sleep(50 * time.Millisecond)

	// UpdateHeartbeat with retry should recover
	err = Retry(ctx, func() error {
		return UpdateHeartbeat(ctx, pool, "hb_retry")
	}, backoff.WithBackOff(NewBackOff(50*time.Millisecond, 500*time.Millisecond)))

	if err != nil {
		t.Fatalf("UpdateHeartbeat with retry failed: %v", err)
	}

	// Verify heartbeat was updated
	workers, _ = ListWorkers(ctx, pool, nil)
	if !workers[0].LastHeartbeat.After(initialHeartbeat) {
		t.Error("heartbeat was not updated after recovery")
	}
}

func TestRetry_ClaimTaskRecovery(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	pool := setupTestDB(t)
	defer pool.Close()

	// Create task
	task := &Task{ID: "claim_retry", Command: "test", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}

	// Control pool
	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer controlPool.Close()

	// Terminate a connection
	conn, _ := pool.Acquire(ctx)
	var connPID int32
	conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&connPID)
	conn.Release()
	terminateBackend(ctx, controlPool, connPID)

	// ClaimTask with retry should recover
	var claimed *Task
	err = Retry(ctx, func() error {
		var claimErr error
		claimed, claimErr = ClaimTask(ctx, pool, "w1", &WorkerInfo{Hostname: "test"}, nil)
		return claimErr
	}, backoff.WithBackOff(NewBackOff(50*time.Millisecond, 500*time.Millisecond)))

	if err != nil {
		t.Fatalf("ClaimTask with retry failed: %v", err)
	}
	if claimed == nil {
		t.Fatal("no task claimed")
	}
	if claimed.ID != "claim_retry" {
		t.Errorf("claimed wrong task: %s", claimed.ID)
	}
}

// === Pool Recovery Tests ===

func TestPool_RecoverFromMassTermination(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	pool := setupTestDB(t)
	defer pool.Close()

	// Control pool
	controlPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer controlPool.Close()

	// Terminate all connections from our pool
	_, err = controlPool.Exec(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE pid != pg_backend_pid()
		AND datname = current_database()
		AND application_name != ''
	`)
	if err != nil {
		t.Logf("mass termination: %v", err)
	}

	// Wait briefly
	time.Sleep(100 * time.Millisecond)

	// Pool should recover - create a task
	task := &Task{ID: "mass_term", Command: "test", Status: StatusPending, Tags: map[string]string{}}
	err = Retry(ctx, func() error {
		return CreateTask(ctx, pool, task)
	}, backoff.WithBackOff(NewBackOff(50*time.Millisecond, 500*time.Millisecond)))

	if err != nil {
		t.Fatalf("CreateTask after mass termination failed: %v", err)
	}

	// Verify task was created
	created, _ := GetTask(ctx, pool, "mass_term")
	if created == nil {
		t.Error("task not found after recovery")
	}
}
