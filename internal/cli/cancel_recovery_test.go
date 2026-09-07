package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestCancelConfirmationWithoutNotification(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	cfg := testConfig(t)
	ctx := context.Background()
	for _, immediate := range []bool{true, false} {
		name := "before-listen"
		if !immediate {
			name = "without-notify"
		}
		t.Run(name, func(t *testing.T) {
			status := db.StatusRunning
			if immediate {
				status = db.StatusCancelled
			}
			if err := db.CreateTask(ctx, pool, &db.Task{ID: name, Command: "sleep 60", Status: status, SourceType: "noop"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- waitForCancel(ctx, cfg, pool, name, 3*time.Second) }()
			if !immediate {
				time.Sleep(100 * time.Millisecond)
				if err := db.CompleteTask(ctx, pool, name, db.StatusCancelled, -1); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCancelConfirmationTimeout(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	cfg := testConfig(t)
	ctx := context.Background()
	if err := db.CreateTask(ctx, pool, &db.Task{ID: "no-confirmation", Command: "sleep 60", Status: db.StatusRunning, SourceType: "noop"}); err != nil {
		t.Fatal(err)
	}
	err := waitForCancel(ctx, cfg, pool, "no-confirmation", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "worker did not confirm") {
		t.Fatalf("missing bounded cancellation diagnostic: %v", err)
	}
}
