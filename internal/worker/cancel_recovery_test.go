package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestWorkerRecoversMissedCancellation(t *testing.T) {
	pool := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := &db.Task{ID: "missed-cancel", Command: "echo ready; sleep 60", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "cancel-recovery", Once: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitForTaskStart(t, pool, task.ID)
	// Persist the request without a notification, as during a listener disconnect.
	if _, err := db.RequestCancel(ctx, pool, task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker ignored the stored cancellation request")
	}
	var status db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", task.ID).Scan(&status); err != nil || status != db.StatusCancelled {
		t.Fatalf("cancellation not persisted: %s %v", status, err)
	}
}

func TestWorkerChecksCancellationBeforeExecution(t *testing.T) {
	pool := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := &db.Task{ID: "cancel-before-exec", Command: "touch payload-started", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Name: "pre-cancel-worker"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	task, err = db.ClaimTask(ctx, pool, w.ID, w.Info, nil)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := db.RequestCancel(ctx, pool, task.ID); err != nil {
		t.Fatal(err)
	}
	channel := db.ToWorkerChannel(w.ID)
	notifier, err := db.NewNotifier(ctx, getTestDBURL(t), db.NewBackOff(time.Millisecond, time.Second), []string{channel}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer notifier.Close(context.Background())
	control := watchWorker(ctx, cancel, notifier.C, channel, nil)
	defer func() { cancel(); <-control.done }()
	if err := w.processTask(ctx, notifier, control.events, task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, task.ID)); !os.IsNotExist(err) {
		t.Fatalf("cancelled task created its workdir: %v", err)
	}
	var status db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", task.ID).Scan(&status); err != nil || status != db.StatusCancelled {
		t.Fatalf("early cancellation was lost: %s %v", status, err)
	}
}
