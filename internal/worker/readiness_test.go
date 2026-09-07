package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestWorkerReadinessFailure(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task := &db.Task{ID: "not-ready", Command: "true", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("parent disconnected")
	called := false
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "not-ready", Once: true, Ready: func() error {
		called = true
		var status db.WorkerStatus
		if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id='not-ready'").Scan(&status); err != nil || status != db.WorkerStatusRunning {
			t.Errorf("readiness preceded registration: %s %v", status, err)
		}
		return failure
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Run(ctx); !called || !errors.Is(err, failure) {
		t.Fatalf("readiness error lost: %v", err)
	}
	stored, err := db.GetTask(ctx, pool, task.ID, time.Minute)
	if err != nil || stored == nil || stored.Status != db.StatusPending {
		t.Fatalf("task claimed before readiness: %+v %v", stored, err)
	}
	var status db.WorkerStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id='not-ready'").Scan(&status); err != nil || status != db.WorkerStatusStopped {
		t.Fatalf("failed readiness left worker active: %s %v", status, err)
	}
}
