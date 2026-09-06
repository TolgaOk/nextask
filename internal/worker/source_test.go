package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
)

// Integration tests with DB

func TestExecutor_NoopSource_Integration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	workdir := t.TempDir()
	task := &db.Task{
		ID:         "src001",
		Command:    "echo hello",
		Status:     db.StatusPending,
		SourceType: "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: workdir}
	result := executor.Execute(ctx, task)

	if result.Code != 0 {
		t.Errorf("exitCode = %d, want 0", result.Code)
	}

	if _, err := os.Stat(filepath.Join(workdir, task.ID)); os.IsNotExist(err) {
		t.Error("task directory not created")
	}
}

func TestExecutor_LegacyGitTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create source repo with a file
	sourceRepo := t.TempDir()
	exec.Command("git", "init", sourceRepo).Run()
	exec.Command("git", "-C", sourceRepo, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", sourceRepo, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(sourceRepo, "hello.txt"), []byte("hello from git"), 0644)
	exec.Command("git", "-C", sourceRepo, "add", ".").Run()
	exec.Command("git", "-C", sourceRepo, "commit", "-m", "init").Run()

	// Create snapshot and push to bare repo
	bareRepo := t.TempDir()
	exec.Command("git", "init", "--bare", bareRepo).Run()

	ref := "refs/heads/project/test123"
	if out, err := exec.Command("git", "-C", sourceRepo, "push", bareRepo, "HEAD:"+ref).CombinedOutput(); err != nil {
		t.Fatalf("push: %v: %s", err, out)
	}
	head, err := exec.Command("git", "-C", sourceRepo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	sourceConfig, _ := json.Marshal(integrations.GitSnapshot{
		Remote: bareRepo,
		Ref:    ref,
		Commit: strings.TrimSpace(string(head)),
	})
	task := &db.Task{
		ID:           "git001",
		Command:      "cat hello.txt",
		Status:       db.StatusPending,
		SourceType:   "git",
		SourceConfig: sourceConfig,
		Tags:         map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	workdir := t.TempDir()
	executor := &Executor{Pool: pool, Workdir: workdir}
	execResult := executor.Execute(ctx, task)

	if execResult.Code != 0 {
		t.Errorf("exitCode = %d, want 0", execResult.Code)
	}

	content, err := os.ReadFile(filepath.Join(workdir, task.ID, "hello.txt"))
	if err != nil {
		t.Fatalf("failed to read hello.txt: %v", err)
	}
	if string(content) != "hello from git" {
		t.Errorf("content = %s, want 'hello from git'", content)
	}
}

func TestExecutor_UnknownSourceType_Integration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "unknsrc",
		Command:    "echo test",
		Status:     db.StatusPending,
		SourceType: "unknown",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	result := executor.Execute(ctx, task)

	if result.Code != 1 {
		t.Errorf("exitCode = %d, want 1", result.Code)
	}
}
