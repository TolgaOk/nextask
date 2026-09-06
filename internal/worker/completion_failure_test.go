package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestWorkerStopsOnPermanentCompletionFailure(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_completion_denied() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = 'completed' THEN
				RAISE EXCEPTION 'completion denied' USING ERRCODE = '42501';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_denied BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION test_completion_denied()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DROP TRIGGER test_denied ON tasks; DROP FUNCTION test_completion_denied()")
	for _, id := range []string{"denied-result", "still-pending"} {
		if err := db.CreateTask(ctx, pool, &db.Task{ID: id, Command: "true", Status: db.StatusPending, SourceType: "noop"}); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := db.Listen(ctx, getTestDBURL(t), db.NewBackOff(time.Millisecond, time.Second), db.FromTaskChannel("denied-result"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close(context.Background())
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "permanent-failure"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Run(ctx); err == nil || !strings.Contains(err.Error(), "result could not be saved") {
		t.Fatalf("completion failure was swallowed: %v", err)
	}
	if names, err := w.journal.pending(); err != nil || len(names) != 1 {
		t.Fatalf("unsaved result missing from journal: %v %v", names, err)
	}
	var status db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id='still-pending'").Scan(&status); err != nil || status != db.StatusPending {
		t.Fatalf("worker continued after permanent failure: %s %v", status, err)
	}
	for {
		select {
		case notif := <-listener.C:
			if notif == nil {
				return
			}
			kind, _, err := db.ParseEvent(notif.Payload)
			if err == nil && kind == db.EventTypeStatus {
				t.Fatal("worker published an uncommitted terminal status")
			}
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestWorkerStopDuringCompletion(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_hold_completion() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = 'completed' THEN PERFORM pg_advisory_xact_lock(7654321); END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_hold BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION test_hold_completion()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DROP TRIGGER test_hold ON tasks; DROP FUNCTION test_hold_completion()")
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock(7654321)"); err != nil {
		t.Fatal(err)
	}
	task := &db.Task{ID: "stop-completion", Command: "true", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "stop-completion-worker"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	task, err = db.ClaimTask(ctx, pool, w.ID, w.Info, nil)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	channel := db.ToWorkerChannel(w.ID)
	notifier, err := db.NewNotifier(ctx, getTestDBURL(t), db.NewBackOff(time.Millisecond, time.Second), []string{channel})
	if err != nil {
		t.Fatal(err)
	}
	defer notifier.Close(context.Background())
	control := watchWorker(ctx, cancel, notifier.C, channel)
	defer func() { cancel(); <-control.done }()
	done := make(chan error, 1)
	go func() { done <- w.processTask(ctx, notifier, control.events, task) }()
	defer func() { cancel(); gate.Rollback(context.Background()); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE wait_event = 'advisory' AND query LIKE '%UPDATE tasks%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not reach the database gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := db.Notify(ctx, pool, channel, db.WorkerWakeEvent{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker-stop notification was ignored during completion")
	}
	if err := gate.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not save its pending result after recovery")
	}
	var status db.TaskStatus
	if err := pool.QueryRow(context.Background(), "SELECT status FROM tasks WHERE id=$1", task.ID).Scan(&status); err != nil || status != db.StatusCompleted {
		t.Fatalf("finished result lost during shutdown: %s %v", status, err)
	}
}

func TestCompletionShutdownDeadline(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_completion_unavailable() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = 'completed' THEN RAISE EXCEPTION 'completion unavailable' USING ERRCODE = '08006'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_unavailable BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION test_completion_unavailable()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DROP TRIGGER test_unavailable ON tasks; DROP FUNCTION test_completion_unavailable()")
	task := &db.Task{ID: "shutdown-deadline", Command: "true", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	task, err = db.ClaimTask(ctx, pool, w.ID, w.Info, nil)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	stopping, stop := context.WithCancel(ctx)
	stop()
	started := time.Now()
	err = w.finishTask(stopping, task, taskExecution{result: &ExitResult{Code: 0}}, false)
	if err == nil || !strings.Contains(err.Error(), "result could not be saved") {
		t.Fatalf("shutdown silently discarded the result: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 29*time.Second || elapsed > 35*time.Second {
		t.Fatalf("shutdown exceeded or skipped its completion grace: %v", elapsed)
	}
	var status db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", task.ID).Scan(&status); err != nil || status != db.StatusRunning {
		t.Fatalf("failed write changed task state: %s %v", status, err)
	}
	names, err := w.journal.pending()
	if err != nil || len(names) != 1 {
		t.Fatalf("shutdown lost its journal: %v %v", names, err)
	}
	recorded, err := w.journal.read(names[0])
	if err != nil || recorded.TaskID != task.ID || recorded.Status != db.StatusCompleted || recorded.ExitCode != 0 {
		t.Fatalf("shutdown journal lost the outcome: %+v %v", recorded, err)
	}
}
