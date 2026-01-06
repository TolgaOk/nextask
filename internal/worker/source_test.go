package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/source"
)

func TestGetSource_Noop(t *testing.T) {
	src, err := GetSource("noop")
	if err != nil {
		t.Fatalf("GetSource(noop) error = %v", err)
	}
	if src.Type() != "noop" {
		t.Errorf("Type() = %s, want noop", src.Type())
	}
}

func TestGetSource_Git(t *testing.T) {
	src, err := GetSource("git")
	if err != nil {
		t.Fatalf("GetSource(git) error = %v", err)
	}
	if src.Type() != "git" {
		t.Errorf("Type() = %s, want git", src.Type())
	}
}

func TestGetSource_Unknown(t *testing.T) {
	_, err := GetSource("unknown")
	if err == nil {
		t.Error("GetSource(unknown) expected error, got nil")
	}
}

func TestNoopSource_Fetch(t *testing.T) {
	tmpDir := t.TempDir()
	taskDir := filepath.Join(tmpDir, "task1")

	src := NoopSource{}
	log := &testLogger{}

	err := src.Fetch(context.Background(), nil, taskDir, log)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(taskDir); os.IsNotExist(err) {
		t.Error("taskDir was not created")
	}
}

func TestGitSource_Fetch_InvalidConfig(t *testing.T) {
	src := GitSource{}
	log := &testLogger{}

	err := src.Fetch(context.Background(), json.RawMessage(`invalid`), "/tmp/test", log)
	if err == nil {
		t.Error("Fetch() with invalid JSON expected error, got nil")
	}
}

func TestGitSource_Fetch_MissingRemote(t *testing.T) {
	src := GitSource{}
	log := &testLogger{}

	cfg := json.RawMessage(`{"ref":"refs/nextask/test"}`)
	err := src.Fetch(context.Background(), cfg, "/tmp/test", log)
	if err == nil {
		t.Error("Fetch() with missing remote expected error, got nil")
	}
}

func TestGitSource_Fetch_MissingRef(t *testing.T) {
	src := GitSource{}
	log := &testLogger{}

	cfg := json.RawMessage(`{"remote":"origin"}`)
	err := src.Fetch(context.Background(), cfg, "/tmp/test", log)
	if err == nil {
		t.Error("Fetch() with missing ref expected error, got nil")
	}
}

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

func TestExecutor_GitSource_Integration(t *testing.T) {
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

	result, err := source.CreateSnapshot(sourceRepo, "test123")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if err := source.PushSnapshot(sourceRepo, bareRepo, result); err != nil {
		t.Fatalf("PushSnapshot() error = %v", err)
	}

	sourceConfig, _ := json.Marshal(GitSourceConfig{
		Remote: bareRepo,
		Ref:    result.Ref,
		Commit: result.Commit,
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
