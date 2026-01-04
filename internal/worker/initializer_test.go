package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nextask/nextask/internal/db"
)

func getTestDBURL(t *testing.T) string {
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set, skipping database tests")
	}
	return url
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	pool, err := db.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	pool.Exec(ctx, "DROP TABLE IF EXISTS task_logs")
	pool.Exec(ctx, "DROP TABLE IF EXISTS tasks")

	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to migrate: %v", err)
	}

	return pool
}

func TestGetInitializer_Noop(t *testing.T) {
	init, err := GetInitializer("noop")
	if err != nil {
		t.Fatalf("GetInitializer(noop) error = %v", err)
	}
	if init.Type() != "noop" {
		t.Errorf("Type() = %s, want noop", init.Type())
	}
}

func TestGetInitializer_Bash(t *testing.T) {
	init, err := GetInitializer("bash")
	if err != nil {
		t.Fatalf("GetInitializer(bash) error = %v", err)
	}
	if init.Type() != "bash" {
		t.Errorf("Type() = %s, want bash", init.Type())
	}
}

func TestGetInitializer_Unknown(t *testing.T) {
	_, err := GetInitializer("unknown")
	if err == nil {
		t.Error("GetInitializer(unknown) expected error, got nil")
	}
}

func TestNoopInitializer_Run(t *testing.T) {
	init := NoopInitializer{}
	log := &testLogger{}

	err := init.Run(context.Background(), nil, "/tmp/test", log)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestBashInitializer_Run_InvalidConfig(t *testing.T) {
	init := BashInitializer{}
	log := &testLogger{}

	err := init.Run(context.Background(), json.RawMessage(`invalid`), "/tmp/test", log)
	if err == nil {
		t.Error("Run() with invalid JSON expected error, got nil")
	}
}

func TestBashInitializer_Run_MissingScript(t *testing.T) {
	init := BashInitializer{}
	log := &testLogger{}

	cfg := json.RawMessage(`{}`)
	err := init.Run(context.Background(), cfg, "/tmp/test", log)
	if err == nil {
		t.Error("Run() with missing script expected error, got nil")
	}
}

func TestBashInitializer_Run_ExecutesScript(t *testing.T) {
	taskDir := t.TempDir()

	scriptContent := "echo 'init running'\necho 'marker' > marker.txt"
	os.WriteFile(filepath.Join(taskDir, "setup.sh"), []byte(scriptContent), 0755)

	init := BashInitializer{}
	log := &testLogger{}
	cfg, _ := json.Marshal(BashInitConfig{Script: "setup.sh"})

	err := init.Run(context.Background(), cfg, taskDir, log)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(taskDir, "marker.txt")); os.IsNotExist(err) {
		t.Error("setup.sh did not run - marker.txt not found")
	}
}

func TestBashInitializer_Run_ScriptFailure(t *testing.T) {
	taskDir := t.TempDir()
	os.WriteFile(filepath.Join(taskDir, "fail.sh"), []byte("exit 1"), 0755)

	init := BashInitializer{}
	log := &testLogger{}
	cfg, _ := json.Marshal(BashInitConfig{Script: "fail.sh"})

	err := init.Run(context.Background(), cfg, taskDir, log)
	if err == nil {
		t.Error("Run() with failing script expected error, got nil")
	}
}

// Integration tests with DB

func TestExecutor_BashInit_Integration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	workdir := t.TempDir()
	taskDir := filepath.Join(workdir, "init001")
	os.MkdirAll(taskDir, 0755)
	os.WriteFile(filepath.Join(taskDir, "setup.sh"), []byte("echo setup > setup_ran.txt"), 0755)

	initConfig, _ := json.Marshal(BashInitConfig{Script: "setup.sh"})
	task := &db.Task{
		ID:         "init001",
		Command:    "cat setup_ran.txt",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "bash",
		InitConfig: initConfig,
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: workdir}
	exitCode := executor.Execute(ctx, task)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}

	if _, err := os.Stat(filepath.Join(taskDir, "setup_ran.txt")); os.IsNotExist(err) {
		t.Error("init did not run")
	}
}

func TestExecutor_NoopInit_Integration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	workdir := t.TempDir()
	task := &db.Task{
		ID:         "noop001",
		Command:    "echo hello",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "noop",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: workdir}
	exitCode := executor.Execute(ctx, task)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1", task.ID).Scan(&count)
	if count == 0 {
		t.Error("no logs captured")
	}
}

func TestExecutor_UnknownInitType_Integration(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &db.Task{
		ID:         "unkn001",
		Command:    "echo test",
		Status:     db.StatusPending,
		SourceType: "noop",
		InitType:   "unknown",
		Tags:       map[string]string{},
	}
	db.CreateTask(ctx, pool, task)

	executor := &Executor{Pool: pool, Workdir: t.TempDir()}
	exitCode := executor.Execute(ctx, task)

	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
}

type testLogger struct {
	logs []string
}

func (l *testLogger) Log(stream, data string) {
	l.logs = append(l.logs, stream+": "+data)
}
