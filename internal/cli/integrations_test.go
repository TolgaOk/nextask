package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/worker"
)

func TestEnqueueGitIntegration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	oldCfg, oldSnapshot, oldRemote, oldAttach, oldTags := cfg, snapshot, remote, attach, tags
	t.Cleanup(func() { cfg, snapshot, remote, attach, tags = oldCfg, oldSnapshot, oldRemote, oldAttach, oldTags })
	snapshot, remote, attach, tags = false, "", false, nil
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	if err := os.WriteFile("payload.txt", []byte("queued content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "Init")
	destination := filepath.Join(t.TempDir(), "remote.git")
	git("init", "--bare", "-q", destination)
	git("remote", "add", "snapshots", destination)
	cfg = &config.Config{
		DB:           config.DBConfig{URL: getTestDBURL(t)},
		Enqueue:      config.EnqueueConfig{With: []string{"git"}},
		Integrations: map[string]map[string]string{"git": {"remote": "snapshots"}},
	}
	id := "integrated-task"
	cmd := enqueueTestCommand(&id)
	command := `cat payload.txt; printf '%s\n' "$NEXTASK_TASK_ID"; exit 7`
	if err := enqueueCmd.RunE(cmd, []string{command}); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetTask(context.Background(), pool, id, time.Minute)
	if err != nil || task == nil {
		t.Fatalf("read task: %+v %v", task, err)
	}
	if task.Command != command || task.ExecutionCommand == nil || task.SourceType != "command" {
		t.Fatalf("missing prepared command: %+v", task)
	}
	claimed, err := db.ClaimTask(context.Background(), pool, "integration-worker", nil, nil)
	if err != nil || claimed == nil || claimed.ExecutionCommand == nil {
		t.Fatalf("claim lost prepared command: %+v %v", claimed, err)
	}
	executor := worker.Executor{Pool: pool, DBURL: cfg.DB.URL, Workdir: t.TempDir()}
	result := executor.Execute(context.Background(), claimed)
	if result.Code != 7 {
		t.Fatalf("execution: %+v", result)
	}
	logs, err := db.GetLogs(context.Background(), pool, id, "stdout", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	var output string
	for _, entry := range logs {
		output += entry.Data + "\n"
	}
	if output != "queued content\nintegrated-task\n" {
		t.Fatalf("output: %q", output)
	}
	// Plain tasks can disable project defaults without touching Git.
	plainID := "plain-task"
	plain := enqueueTestCommand(&plainID)
	plain.Flags().StringArray("without", []string{"git"}, "")
	if err := enqueueCmd.RunE(plain, []string{"echo plain"}); err != nil {
		t.Fatal(err)
	}
	plainTask, err := db.GetTask(context.Background(), pool, plainID, time.Minute)
	if err != nil || plainTask.ExecutionCommand != nil || plainTask.SourceType != "noop" {
		t.Fatalf("plain task changed: %+v %v", plainTask, err)
	}
	// Validation errors never publish a task.
	invalidID := "invalid-task"
	invalid := enqueueTestCommand(&invalidID)
	invalid.Flags().StringArray("set", []string{"git.unknown=value"}, "")
	if err := enqueueCmd.RunE(invalid, []string{"true"}); err == nil {
		t.Fatal("invalid option accepted")
	}
	absent, err := db.GetTask(context.Background(), pool, invalidID, time.Minute)
	if err != nil || absent != nil {
		t.Fatal("invalid task was published")
	}
}
