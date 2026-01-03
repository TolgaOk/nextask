package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	pool, err := Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to migrate: %v", err)
	}

	_, err = pool.Exec(ctx, "DELETE FROM tasks")
	if err != nil {
		pool.Close()
		t.Fatalf("failed to clean tasks: %v", err)
	}

	return pool
}

func TestConnect(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer pool.Close()

	var result int
	err = pool.QueryRow(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}
}

func TestCreateTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{
		ID:      "test1234",
		Command: "echo hello",
		Status:  StatusPending,
		Tags:    map[string]string{"env": "test"},
	}

	err := CreateTask(ctx, pool, task)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	// Verify task was created
	var id, command, status string
	err = pool.QueryRow(ctx, "SELECT id, command, status FROM tasks WHERE id = $1", task.ID).
		Scan(&id, &command, &status)
	if err != nil {
		t.Fatalf("failed to query task: %v", err)
	}

	if id != task.ID {
		t.Errorf("id = %v, want %v", id, task.ID)
	}
	if command != task.Command {
		t.Errorf("command = %v, want %v", command, task.Command)
	}
	if status != string(task.Status) {
		t.Errorf("status = %v, want %v", status, task.Status)
	}
}

func TestCreateTask_WithSourceFields(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	remote := "origin"
	ref := "refs/nextask/test5678"
	commit := "abc123def456"

	task := &Task{
		ID:           "test5678",
		Command:      "python train.py",
		Status:       StatusPending,
		Tags:         map[string]string{},
		SourceRemote: &remote,
		SourceRef:    &ref,
		SourceCommit: &commit,
	}

	err := CreateTask(ctx, pool, task)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	// Verify source fields
	var sourceRemote, sourceRef, sourceCommit *string
	err = pool.QueryRow(ctx,
		"SELECT source_remote, source_ref, source_commit FROM tasks WHERE id = $1",
		task.ID).Scan(&sourceRemote, &sourceRef, &sourceCommit)
	if err != nil {
		t.Fatalf("failed to query task: %v", err)
	}

	if sourceRemote == nil || *sourceRemote != remote {
		t.Errorf("source_remote = %v, want %v", sourceRemote, remote)
	}
	if sourceRef == nil || *sourceRef != ref {
		t.Errorf("source_ref = %v, want %v", sourceRef, ref)
	}
	if sourceCommit == nil || *sourceCommit != commit {
		t.Errorf("source_commit = %v, want %v", sourceCommit, commit)
	}
}

func TestListTasks(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create test tasks
	tasks := []*Task{
		{ID: "list0001", Command: "echo one", Status: StatusPending, Tags: map[string]string{"env": "dev"}},
		{ID: "list0002", Command: "echo two", Status: StatusRunning, Tags: map[string]string{"env": "prod"}},
		{ID: "list0003", Command: "python train.py", Status: StatusCompleted, Tags: map[string]string{"env": "dev"}},
	}
	for _, task := range tasks {
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	// Test: list all
	result, err := ListTasks(ctx, pool, ListFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 3 {
		t.Errorf("len(result) = %d, want 3", len(result))
	}
}

func TestListTasks_FilterByStatus(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	tasks := []*Task{
		{ID: "stat0001", Command: "cmd1", Status: StatusPending, Tags: map[string]string{}},
		{ID: "stat0002", Command: "cmd2", Status: StatusPending, Tags: map[string]string{}},
		{ID: "stat0003", Command: "cmd3", Status: StatusRunning, Tags: map[string]string{}},
	}
	for _, task := range tasks {
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	result, err := ListTasks(ctx, pool, ListFilter{Statuses: []string{"pending"}})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}
}

func TestListTasks_FilterByTags(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	tasks := []*Task{
		{ID: "tags0001", Command: "cmd1", Status: StatusPending, Tags: map[string]string{"env": "dev", "team": "ml"}},
		{ID: "tags0002", Command: "cmd2", Status: StatusPending, Tags: map[string]string{"env": "prod"}},
		{ID: "tags0003", Command: "cmd3", Status: StatusPending, Tags: map[string]string{"env": "dev"}},
	}
	for _, task := range tasks {
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	result, err := ListTasks(ctx, pool, ListFilter{Tags: map[string]string{"env": "dev"}})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}
}

func TestListTasks_Limit(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	tasks := []*Task{
		{ID: "lim00001", Command: "cmd1", Status: StatusPending, Tags: map[string]string{}},
		{ID: "lim00002", Command: "cmd2", Status: StatusPending, Tags: map[string]string{}},
		{ID: "lim00003", Command: "cmd3", Status: StatusPending, Tags: map[string]string{}},
	}
	for _, task := range tasks {
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	result, err := ListTasks(ctx, pool, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}
}

func TestListTasks_FilterByCommand(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	tasks := []*Task{
		{ID: "cmd00001", Command: "python train.py", Status: StatusPending, Tags: map[string]string{}},
		{ID: "cmd00002", Command: "python eval.py", Status: StatusPending, Tags: map[string]string{}},
		{ID: "cmd00003", Command: "bash run.sh", Status: StatusPending, Tags: map[string]string{}},
	}
	for _, task := range tasks {
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	result, err := ListTasks(ctx, pool, ListFilter{Commands: []string{"python"}})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("len(result) = %d, want 2", len(result))
	}
}

func TestListTasks_FilterBySince(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create a task
	task := &Task{
		ID:      "time0001",
		Command: "echo test",
		Status:  StatusPending,
		Tags:    map[string]string{},
	}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	// Filter with time in past - should find task
	pastTime := time.Now().Add(-1 * time.Hour)
	result, err := ListTasks(ctx, pool, ListFilter{Since: pastTime})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 1 {
		t.Errorf("len(result) = %d, want 1", len(result))
	}

	// Filter with time in future - should find nothing
	futureTime := time.Now().Add(1 * time.Hour)
	result, err = ListTasks(ctx, pool, ListFilter{Since: futureTime})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}
