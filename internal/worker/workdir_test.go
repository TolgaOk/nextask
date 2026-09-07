package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestExecutorPreservesExistingWorkdir(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	for _, directory := range []bool{true, false} {
		for _, remove := range []bool{false, true} {
			t.Run(fmt.Sprintf("directory=%t/remove=%t", directory, remove), func(t *testing.T) {
				task := &db.Task{ID: fmt.Sprintf("existing-%t-%t", directory, remove), Command: "echo contaminated > payload-started", Status: db.StatusPending, SourceType: "noop"}
				if err := db.CreateTask(ctx, pool, task); err != nil {
					t.Fatal(err)
				}
				root := t.TempDir()
				taskDir := filepath.Join(root, task.ID)
				retained := taskDir
				if directory {
					if err := os.Mkdir(taskDir, 0755); err != nil {
						t.Fatal(err)
					}
					retained = filepath.Join(taskDir, "old-artifact")
				}
				if err := os.WriteFile(retained, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
				executor := &Executor{Pool: pool, Workdir: root, RemoveWorkdir: remove}
				result := executor.Execute(ctx, task)
				if result.Code != 1 || result.Err == nil || !strings.Contains(result.Err.Error(), "task directory already exists") {
					t.Fatalf("unsafe reuse: %+v", result)
				}
				data, err := os.ReadFile(retained)
				if err != nil || string(data) != "retained" {
					t.Fatal("existing artifact changed or removed")
				}
				if _, err := os.Stat(filepath.Join(taskDir, "payload-started")); err == nil {
					t.Fatal("payload started in old directory")
				}
				var logged bool
				if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM task_logs WHERE task_id=$1 AND data LIKE '%task directory already exists%')", task.ID).Scan(&logged); err != nil || !logged {
					t.Fatalf("missing directory diagnostic: %v", err)
				}
			})
		}
	}
}

func TestExecutorRemovesOwnedWorkdir(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	task := &db.Task{ID: "owned-dir", Command: "echo result > output; echo done", Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	executor := &Executor{Pool: pool, Workdir: root, RemoveWorkdir: true, LogFlushLines: 16, LogFlushInterval: time.Millisecond, LogBufferSize: 16}
	if result := executor.Execute(ctx, task); result.Code != 0 {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(root, task.ID)); !os.IsNotExist(err) {
		t.Fatalf("owned directory was not removed: %v", err)
	}
	logs, err := db.GetLogsSince(ctx, pool, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range logs {
		found = found || line.Stream == "stdout" && line.Data == "done"
	}
	if !found {
		t.Fatal("cleanup ran before logs were flushed")
	}
}
