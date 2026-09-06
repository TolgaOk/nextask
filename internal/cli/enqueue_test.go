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

func enqueueTestCommand(id *string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().String("id", "", "")
	if id != nil {
		_ = cmd.Flags().Set("id", *id)
	}
	return cmd
}

func TestEnqueueInvalidID(t *testing.T) {
	for _, id := range []string{"", "../task", strings.Repeat("a", 54)} {
		cmd := enqueueTestCommand(&id)
		if err := enqueueCmd.Args(cmd, []string{"echo test"}); err == nil {
			t.Errorf("invalid ID %q accepted", id)
		}
	}
	if err := enqueueCmd.Args(enqueueTestCommand(nil), []string{"echo test"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueIDs(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	oldCfg, oldSnapshot, oldRemote, oldAttach, oldTags := cfg, snapshot, remote, attach, tags
	t.Cleanup(func() { cfg, snapshot, remote, attach, tags = oldCfg, oldSnapshot, oldRemote, oldAttach, oldTags })
	cfg = &config.Config{DB: config.DBConfig{URL: getTestDBURL(t)}}
	snapshot, remote, attach, tags = false, "", false, nil
	ctx := context.Background()
	id := "export-42"
	cmd := enqueueTestCommand(&id)
	if err := enqueueCmd.RunE(cmd, []string{"echo original"}); err != nil {
		t.Fatal(err)
	}
	// Duplicate rejection must happen before even attempting a snapshot.
	snapshot = true
	cfg.Source.Remote = "/nonexistent/nextask-test-remote.git"
	t.Chdir(t.TempDir())
	if err := enqueueCmd.RunE(cmd, []string{"echo replacement"}); !errors.Is(err, db.ErrTaskExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	task, err := db.GetTask(ctx, pool, id, time.Minute)
	if err != nil || task == nil || task.Command != "echo original" || task.SourceType != "noop" {
		t.Fatalf("duplicate changed original task: %+v, %v", task, err)
	}
	// Failed snapshot preparation rolls back the reserved ID.
	failedID := "failed-snapshot"
	if err := enqueueCmd.RunE(enqueueTestCommand(&failedID), []string{"true"}); err == nil {
		t.Fatal("expected snapshot failure outside a repository")
	}
	task, err = db.GetTask(ctx, pool, failedID, time.Minute)
	if err != nil || task != nil {
		t.Fatalf("failed task was published: %+v, %v", task, err)
	}
	snapshot = false
	if err := enqueueCmd.RunE(enqueueTestCommand(&failedID), []string{"true"}); err != nil {
		t.Fatalf("rolled-back ID cannot be reused: %v", err)
	}
	if err := enqueueCmd.RunE(enqueueTestCommand(nil), []string{"echo generated"}); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks(ctx, pool, db.ListFilter{Commands: []string{"echo generated"}})
	if err != nil || len(tasks) != 1 || len(tasks[0].ID) != 8 {
		t.Fatalf("automatic ID generation failed: %+v, %v", tasks, err)
	}
}

func TestEnqueueConcurrentID(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = &config.Config{DB: config.DBConfig{URL: getTestDBURL(t)}}
	id := "concurrent-task"
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- enqueueCmd.RunE(enqueueTestCommand(&id), []string{"true"})
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
