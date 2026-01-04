package worker

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestDBLogger_LogsStdout(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log001",
		Command:    "echo hello && echo world",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stdout'", task.ID).Scan(&count)
	if count < 2 {
		t.Errorf("stdout count = %d, want >= 2", count)
	}

	var data string
	pool.QueryRow(ctx, "SELECT data FROM task_logs WHERE task_id = $1 AND stream = 'stdout' ORDER BY id LIMIT 1", task.ID).Scan(&data)
	if data != "hello" {
		t.Errorf("first stdout = %s, want hello", data)
	}
}

func TestDBLogger_LogsStderr(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log002",
		Command:    "echo error_msg >&2",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stderr'", task.ID).Scan(&count)
	if count != 1 {
		t.Errorf("stderr count = %d, want 1", count)
	}

	var data string
	pool.QueryRow(ctx, "SELECT data FROM task_logs WHERE task_id = $1 AND stream = 'stderr'", task.ID).Scan(&data)
	if data != "error_msg" {
		t.Errorf("stderr = %s, want error_msg", data)
	}
}

func TestDBLogger_LogsBothStreams(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log003",
		Command:    "echo out && echo err >&2",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var stdoutCount, stderrCount int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stdout'", task.ID).Scan(&stdoutCount)
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stderr'", task.ID).Scan(&stderrCount)

	if stdoutCount == 0 {
		t.Error("stdout not captured")
	}
	if stderrCount == 0 {
		t.Error("stderr not captured")
	}
}

func TestDBLogger_LogsFromInit(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	workdir := t.TempDir()
	taskDir := workdir + "/log004"
	createDir(taskDir)
	writeFile(taskDir+"/setup.sh", "echo init_output")

	initConfig := marshalJSON(BashInitConfig{Script: "setup.sh"})
	task := &db.Task{
		ID:         "log004",
		Command:    "echo cmd_output",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "bash",
		InitConfig: initConfig,
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: workdir}
	executor.Execute(ctx, task)

	// Check logs from both init and command
	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'stdout'", task.ID).Scan(&count)
	if count < 2 {
		t.Errorf("stdout count = %d, want >= 2 (init + cmd)", count)
	}

	// Verify init output was logged
	var found bool
	rows, _ := pool.Query(ctx, "SELECT data FROM task_logs WHERE task_id = $1 AND stream = 'stdout'", task.ID)
	for rows.Next() {
		var data string
		rows.Scan(&data)
		if data == "init_output" {
			found = true
			break
		}
	}
	rows.Close()
	if !found {
		t.Error("init output not found in logs")
	}
}

func TestDBLogger_LogsErrorOnSourceFailure(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log005",
		Command:    "echo test",
		Status:     db.StatusPending,
		SourceType: "unknown",
		InitType:   "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'error'", task.ID).Scan(&count)
	if count == 0 {
		t.Error("error log not captured for source failure")
	}
}

func TestDBLogger_LogsErrorOnInitFailure(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "log006",
		Command:    "echo test",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "unknown",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	executor.Execute(ctx, task)

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1 AND stream = 'error'", task.ID).Scan(&count)
	if count == 0 {
		t.Error("error log not captured for init failure")
	}
}

// Helper functions

func createDir(path string) {
	os.MkdirAll(path, 0755)
}

func writeFile(path, content string) {
	os.WriteFile(path, []byte(content), 0755)
}

func marshalJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
