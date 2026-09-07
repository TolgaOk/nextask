package worker

import (
	"context"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestWorkerWaitsForCompletionRecovery(t *testing.T) {
	pool := setupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `CREATE TABLE completion_gate (blocked boolean);
		INSERT INTO completion_gate VALUES (true);
		CREATE FUNCTION test_block_completion() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF OLD.id = 'blocked-result' AND NEW.status = 'completed' AND
				EXISTS (SELECT FROM completion_gate WHERE blocked) THEN
				RAISE EXCEPTION 'temporary completion outage' USING ERRCODE = '08006';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_completion BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION test_block_completion()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DROP TRIGGER test_completion ON tasks; DROP FUNCTION test_block_completion(); DROP TABLE completion_gate")
	for _, id := range []string{"blocked-result", "next-result"} {
		if err := db.CreateTask(ctx, pool, &db.Task{ID: id, Command: "echo done", Status: db.StatusPending, SourceType: "noop"}); err != nil {
			t.Fatal(err)
		}
	}
	idle := 100 * time.Millisecond
	w, err := New(ctx, Config{
		DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "completion-recovery",
		HeartbeatInterval: 50 * time.Millisecond, ExitIfIdle: &idle,
		BackoffInitial: 100 * time.Millisecond, BackoffMax: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitForTaskStart(t, pool, "blocked-result")
	// Exceed the old 30-second completion deadline while heartbeats still work.
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		done <- err
		t.Fatalf("worker abandoned its result before recovery: %v", err)
	case <-timer.C:
	}
	var next db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = 'next-result'").Scan(&next); err != nil || next != db.StatusPending {
		t.Fatalf("worker claimed another task before saving its result: %s %v", next, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE completion_gate SET blocked = false"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("worker did not recover")
	}
	var completed int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM tasks WHERE status = 'completed' AND exit_code = 0").Scan(&completed); err != nil || completed != 2 {
		t.Fatalf("completion results lost: %d %v", completed, err)
	}
}
