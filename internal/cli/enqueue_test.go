package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

func enqueueTestCommand(cfg *config.Config, id *string) *cobra.Command {
	cmd := newEnqueueCommand(cfg)
	cmd.SetContext(context.Background())
	if id != nil {
		_ = cmd.Flags().Set("id", *id)
	}
	return cmd
}

func TestEnqueueInvalidID(t *testing.T) {
	for _, id := range []string{"", "../task", strings.Repeat("a", 54)} {
		cmd := enqueueTestCommand(&config.Config{}, &id)
		if err := cmd.Args(cmd, []string{"echo test"}); err == nil {
			t.Errorf("invalid ID %q accepted", id)
		}
	}
	cmd := enqueueTestCommand(&config.Config{}, nil)
	if err := cmd.Args(cmd, []string{"echo test"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueIDs(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	ctx := context.Background()
	id := "export-42"
	cmd := enqueueTestCommand(&cfg, &id)
	if err := cmd.RunE(cmd, []string{"echo original"}); err != nil {
		t.Fatal(err)
	}
	// Duplicate rejection must happen before even attempting a snapshot.
	if err := cmd.Flags().Set("snapshot", "true"); err != nil {
		t.Fatal(err)
	}
	cfg.Source.Remote = "/nonexistent/nextask-test-remote.git"
	t.Chdir(t.TempDir())
	if err := cmd.RunE(cmd, []string{"echo replacement"}); !errors.Is(err, db.ErrTaskExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	task, err := db.GetTask(ctx, pool, id, time.Minute)
	if err != nil || task == nil || task.Command != "echo original" || task.SourceType != "noop" {
		t.Fatalf("duplicate changed original task: %+v, %v", task, err)
	}
	// Failed preparation rolls back the ID; a fresh plain command can reuse it.
	failedID := "failed-snapshot"
	failed := enqueueTestCommand(&cfg, &failedID)
	if err := failed.Flags().Set("snapshot", "true"); err != nil {
		t.Fatal(err)
	}
	if err := failed.RunE(failed, []string{"true"}); err == nil {
		t.Fatal("expected snapshot failure outside a repository")
	}
	task, err = db.GetTask(ctx, pool, failedID, time.Minute)
	if err != nil || task != nil {
		t.Fatalf("failed task was published: %+v, %v", task, err)
	}
	plain := enqueueTestCommand(&cfg, &failedID)
	if err := plain.RunE(plain, []string{"true"}); err != nil {
		t.Fatalf("rolled-back ID cannot be reused: %v", err)
	}
	generated := enqueueTestCommand(&cfg, nil)
	if err := generated.RunE(generated, []string{"echo generated"}); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks(ctx, pool, db.ListFilter{Commands: []string{"echo generated"}})
	if err != nil || len(tasks) != 1 || len(tasks[0].ID) != 8 {
		t.Fatalf("automatic ID generation failed: %+v, %v", tasks, err)
	}
}

func TestEnqueueConcurrentID(t *testing.T) {
	setupTestDB(t)
	cfg := testConfig(t)
	id := "concurrent-task"
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			cmd := enqueueTestCommand(&cfg, &id)
			<-start
			results <- cmd.RunE(cmd, []string{"true"})
		}()
	}
	close(start)
	successes, duplicates := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, db.ErrTaskExists):
			duplicates++
		default:
			t.Errorf("unexpected enqueue error: %v", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("successes=%d, duplicates=%d", successes, duplicates)
	}
}
