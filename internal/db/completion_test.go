package db

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCompleteClaim(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	task := &Task{ID: "journal-result", Command: "exit 17", Status: StatusPending, SourceType: "noop"}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimTask(ctx, pool, "original-worker", nil, nil)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	result := TaskCompletion{TaskID: claim.ID, WorkerID: *claim.WorkerID, CreatedAt: claim.CreatedAt,
		StartedAt: *claim.StartedAt, FinishedAt: time.Now().UTC().Truncate(time.Microsecond), Status: StatusFailed, ExitCode: 17}
	for _, change := range []func(*TaskCompletion){
		func(c *TaskCompletion) { c.WorkerID = "another-worker" },
		func(c *TaskCompletion) { c.StartedAt = c.StartedAt.Add(time.Microsecond) },
		func(c *TaskCompletion) { c.CreatedAt = c.CreatedAt.Add(time.Microsecond) },
	} {
		wrong := result
		change(&wrong)
		if ok, err := CompleteClaim(ctx, pool, wrong); err != nil || ok {
			t.Fatalf("incorrect claim accepted: %t %v", ok, err)
		}
	}
	// Concurrent recovery must confirm one identical outcome without overwriting it.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := CompleteClaim(ctx, pool, result); err != nil || !ok {
				t.Errorf("completion: %t %v", ok, err)
			}
		}()
	}
	wg.Wait()
	if ok, err := CompleteClaim(ctx, pool, result); err != nil || !ok {
		t.Fatalf("committed outcome not recognized: %t %v", ok, err)
	}
	changed := result
	changed.ExitCode = 18
	if ok, err := CompleteClaim(ctx, pool, changed); err != nil || ok {
		t.Fatalf("terminal outcome overwritten: %t %v", ok, err)
	}
	var status TaskStatus
	var code int
	var finished time.Time
	if err := pool.QueryRow(ctx, "SELECT status, exit_code, finished_at FROM tasks WHERE id=$1", task.ID).Scan(&status, &code, &finished); err != nil {
		t.Fatal(err)
	}
	if status != StatusFailed || code != 17 || !finished.Equal(result.FinishedAt) {
		t.Fatalf("result changed: %s %d %s", status, code, finished)
	}
	if _, err := DeleteTask(ctx, pool, task.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := CompleteClaim(ctx, pool, result); err != nil || ok {
		t.Fatalf("deleted task restored: %t %v", ok, err)
	}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimTask(ctx, pool, "original-worker", nil, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := CompleteClaim(ctx, pool, result); err != nil || ok {
		t.Fatalf("reused task ID overwritten: %t %v", ok, err)
	}
}
