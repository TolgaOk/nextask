package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

	// Drop tables to ensure fresh schema
	pool.Exec(ctx, "DROP TABLE IF EXISTS task_logs")
	pool.Exec(ctx, "DROP TABLE IF EXISTS tasks")
	pool.Exec(ctx, "DROP TABLE IF EXISTS workers")

	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to migrate: %v", err)
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

func TestCreateTask_WithSourceConfig(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{
		ID:           "test5678",
		Command:      "python train.py",
		Status:       StatusPending,
		Tags:         map[string]string{},
		SourceType:   "git",
		SourceConfig: json.RawMessage(`{"remote":"origin","ref":"refs/nextask/test5678","commit":"abc123"}`),
	}

	err := CreateTask(ctx, pool, task)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	// Verify source fields
	var sourceType string
	var sourceConfig []byte
	err = pool.QueryRow(ctx,
		"SELECT source_type, source_config FROM tasks WHERE id = $1",
		task.ID).Scan(&sourceType, &sourceConfig)
	if err != nil {
		t.Fatalf("failed to query task: %v", err)
	}

	if sourceType != "git" {
		t.Errorf("source_type = %v, want git", sourceType)
	}

	var cfg map[string]string
	json.Unmarshal(sourceConfig, &cfg)
	if cfg["remote"] != "origin" || cfg["ref"] != "refs/nextask/test5678" || cfg["commit"] != "abc123" {
		t.Errorf("source_config = %v", cfg)
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

func TestClaimTask_Basic(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "claim001", Command: "echo hello", Status: StatusPending, Tags: map[string]string{"env": "test"}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	workerInfo := &WorkerInfo{Hostname: "test-host", OS: "linux", PID: 12345}
	claimed, err := ClaimTask(ctx, pool, "worker-1", workerInfo, nil)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimTask() returned nil")
	}
	if claimed.ID != "claim001" || claimed.Status != StatusRunning {
		t.Errorf("got ID=%s Status=%s, want claim001/running", claimed.ID, claimed.Status)
	}
	if claimed.WorkerID == nil || *claimed.WorkerID != "worker-1" {
		t.Errorf("WorkerID = %v, want worker-1", claimed.WorkerID)
	}
	if claimed.WorkerInfo == nil || claimed.WorkerInfo.Hostname != "test-host" {
		t.Error("WorkerInfo not set correctly")
	}
}

func TestClaimTask_WithSourceConfig(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{
		ID:           "claimcfg",
		Command:      "python train.py",
		Status:       StatusPending,
		Tags:         map[string]string{},
		SourceType:   "git",
		SourceConfig: json.RawMessage(`{"remote":"origin","ref":"refs/nextask/test"}`),
	}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	claimed, err := ClaimTask(ctx, pool, "worker-1", &WorkerInfo{Hostname: "test"}, nil)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimTask() returned nil")
	}

	if claimed.SourceType != "git" {
		t.Errorf("SourceType = %s, want git", claimed.SourceType)
	}

	var srcCfg map[string]string
	json.Unmarshal(claimed.SourceConfig, &srcCfg)
	if srcCfg["remote"] != "origin" || srcCfg["ref"] != "refs/nextask/test" {
		t.Errorf("SourceConfig = %v", srcCfg)
	}
}

func TestClaimTask_NoTasks(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	claimed, err := ClaimTask(ctx, pool, "worker-1", &WorkerInfo{Hostname: "test"}, nil)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}
	if claimed != nil {
		t.Errorf("ClaimTask() = %v, want nil", claimed)
	}
}

func TestClaimTask_SkipsNonPending(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	for _, s := range []TaskStatus{StatusRunning, StatusCompleted, StatusFailed} {
		task := &Task{ID: "skip" + string(s), Command: "cmd", Status: s, Tags: map[string]string{}}
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
	}

	claimed, err := ClaimTask(ctx, pool, "worker-1", &WorkerInfo{Hostname: "test"}, nil)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}
	if claimed != nil {
		t.Errorf("ClaimTask() = %v, want nil", claimed)
	}
}

func TestClaimTask_FIFO(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "fifo01", Command: "first", Status: StatusPending, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := CreateTask(ctx, pool, &Task{ID: "fifo02", Command: "second", Status: StatusPending, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	wi := &WorkerInfo{Hostname: "test"}
	c1, _ := ClaimTask(ctx, pool, "w1", wi, nil)
	c2, _ := ClaimTask(ctx, pool, "w2", wi, nil)

	if c1 == nil || c1.ID != "fifo01" {
		t.Errorf("first claim got %v, want fifo01", c1)
	}
	if c2 == nil || c2.ID != "fifo02" {
		t.Errorf("second claim got %v, want fifo02", c2)
	}
}

func TestClaimTask_Concurrent(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create single task
	if err := CreateTask(ctx, pool, &Task{ID: "race0001", Command: "test", Status: StatusPending, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	// Race 10 workers to claim it
	numWorkers := 10
	results := make(chan *Task, numWorkers)
	errors := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			wi := &WorkerInfo{Hostname: "test"}
			task, err := ClaimTask(ctx, pool, fmt.Sprintf("worker-%d", workerID), wi, nil)
			if err != nil {
				errors <- err
				return
			}
			results <- task
		}(i)
	}

	// Collect results
	var claimed []*Task
	for i := 0; i < numWorkers; i++ {
		select {
		case task := <-results:
			if task != nil {
				claimed = append(claimed, task)
			}
		case err := <-errors:
			t.Errorf("worker error: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for workers")
		}
	}

	// Exactly one worker should have claimed the task
	if len(claimed) != 1 {
		t.Errorf("expected 1 claim, got %d", len(claimed))
	}
	if len(claimed) > 0 && claimed[0].ID != "race0001" {
		t.Errorf("claimed wrong task: %s", claimed[0].ID)
	}
}

func TestClaimTask_TagFilter(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create tasks with different tags
	if err := CreateTask(ctx, pool, &Task{ID: "tag01", Command: "gpu-job", Status: StatusPending, Tags: map[string]string{"gpu": "a100"}}); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(ctx, pool, &Task{ID: "tag02", Command: "cpu-job", Status: StatusPending, Tags: map[string]string{"gpu": "cpu"}}); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(ctx, pool, &Task{ID: "tag03", Command: "any-job", Status: StatusPending, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	wi := &WorkerInfo{Hostname: "test"}

	// Worker with gpu=a100 filter should only get tag01
	task, err := ClaimTask(ctx, pool, "w1", wi, map[string]string{"gpu": "a100"})
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.ID != "tag01" {
		t.Errorf("expected tag01, got %v", task)
	}

	// Worker with gpu=cpu filter should only get tag02
	task, err = ClaimTask(ctx, pool, "w2", wi, map[string]string{"gpu": "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.ID != "tag02" {
		t.Errorf("expected tag02, got %v", task)
	}

	// Worker with no filter should get tag03 (remaining pending task)
	task, err = ClaimTask(ctx, pool, "w3", wi, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.ID != "tag03" {
		t.Errorf("expected tag03, got %v", task)
	}

	// Worker with non-matching filter should get nothing
	task, err = ClaimTask(ctx, pool, "w4", wi, map[string]string{"gpu": "h100"})
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Errorf("expected nil, got %v", task)
	}
}

func TestCompleteTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "comp01", Command: "test", Status: StatusPending, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimTask(ctx, pool, "w1", &WorkerInfo{Hostname: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := CompleteTask(ctx, pool, "comp01", StatusCompleted, 0); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}

	tasks, _ := ListTasks(ctx, pool, ListFilter{Statuses: []string{string(StatusCompleted)}})
	if len(tasks) != 1 || tasks[0].ID != "comp01" {
		t.Errorf("expected completed comp01, got %v", tasks)
	}
}

func TestInsertLog(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "log001", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	if _, err := InsertLog(ctx, pool, "log001", "stdout", "hello"); err != nil {
		t.Fatalf("InsertLog(stdout) error = %v", err)
	}
	if _, err := InsertLog(ctx, pool, "log001", "stderr", "world"); err != nil {
		t.Fatalf("InsertLog(stderr) error = %v", err)
	}

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1", "log001").Scan(&count)
	if count != 2 {
		t.Errorf("log count = %d, want 2", count)
	}
}

func TestNotifyNewTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Separate connection for LISTEN
	listenConn, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect for listen: %v", err)
	}
	defer listenConn.Close(ctx)

	if _, err := listenConn.Exec(ctx, "LISTEN "+ToWorkersChannel); err != nil {
		t.Fatalf("LISTEN failed: %v", err)
	}

	// Send NOTIFY
	if err := Notify(ctx, pool, ToWorkersChannel, WorkerWakeEvent{}); err != nil {
		t.Fatalf("NOTIFY failed: %v", err)
	}

	// Wait for notification with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	notification, err := listenConn.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("WaitForNotification failed: %v", err)
	}

	if notification.Channel != ToWorkersChannel {
		t.Errorf("channel = %s, want %s", notification.Channel, ToWorkersChannel)
	}
}

func TestNotifyMultipleListeners(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create two listener connections
	conn1, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect conn1: %v", err)
	}
	defer conn1.Close(ctx)

	conn2, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect conn2: %v", err)
	}
	defer conn2.Close(ctx)

	conn1.Exec(ctx, "LISTEN "+ToWorkersChannel)
	conn2.Exec(ctx, "LISTEN "+ToWorkersChannel)

	// Send NOTIFY
	Notify(ctx, pool, ToWorkersChannel, WorkerWakeEvent{})

	// Both should receive
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	n1, err1 := conn1.WaitForNotification(waitCtx)
	n2, err2 := conn2.WaitForNotification(waitCtx)

	if err1 != nil {
		t.Errorf("conn1 failed to receive: %v", err1)
	}
	if err2 != nil {
		t.Errorf("conn2 failed to receive: %v", err2)
	}
	if n1 != nil && n1.Channel != ToWorkersChannel {
		t.Errorf("conn1 channel = %s, want %s", n1.Channel, ToWorkersChannel)
	}
	if n2 != nil && n2.Channel != ToWorkersChannel {
		t.Errorf("conn2 channel = %s, want %s", n2.Channel, ToWorkersChannel)
	}
}

func TestGetLogs_All(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog01", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	InsertLog(ctx, pool, "getlog01", "stdout", "line1")
	InsertLog(ctx, pool, "getlog01", "stderr", "line2")
	InsertLog(ctx, pool, "getlog01", "stdout", "line3")

	logs, err := GetLogs(ctx, pool, "getlog01", "", 0, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("len(logs) = %d, want 3", len(logs))
	}
	if logs[0].Data != "line1" || logs[1].Data != "line2" || logs[2].Data != "line3" {
		t.Errorf("logs not in chronological order: %v", logs)
	}
}

func TestGetLogs_FilterByStream(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog02", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	InsertLog(ctx, pool, "getlog02", "stdout", "out1")
	InsertLog(ctx, pool, "getlog02", "stderr", "err1")
	InsertLog(ctx, pool, "getlog02", "stdout", "out2")

	logs, err := GetLogs(ctx, pool, "getlog02", "stdout", 0, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("len(logs) = %d, want 2", len(logs))
	}
	for _, log := range logs {
		if log.Stream != "stdout" {
			t.Errorf("unexpected stream: %s", log.Stream)
		}
	}
}

func TestGetLogs_NonExistentStream(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog03", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	InsertLog(ctx, pool, "getlog03", "stdout", "line1")

	logs, err := GetLogs(ctx, pool, "getlog03", "nonexistent", 0, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("len(logs) = %d, want 0", len(logs))
	}
}

func TestGetLogs_Head(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog04", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 5; i++ {
		InsertLog(ctx, pool, "getlog04", "stdout", fmt.Sprintf("line%d", i))
	}

	logs, err := GetLogs(ctx, pool, "getlog04", "", 3, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("len(logs) = %d, want 3", len(logs))
	}
	if logs[0].Data != "line1" || logs[2].Data != "line3" {
		t.Errorf("head returned wrong logs: %v", logs)
	}
}

func TestGetLogs_Tail(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog05", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 5; i++ {
		InsertLog(ctx, pool, "getlog05", "stdout", fmt.Sprintf("line%d", i))
	}

	logs, err := GetLogs(ctx, pool, "getlog05", "", 3, true)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("len(logs) = %d, want 3", len(logs))
	}
	if logs[0].Data != "line3" || logs[2].Data != "line5" {
		t.Errorf("tail returned wrong logs: %v", logs)
	}
}

func TestGetLogs_StreamWithHead(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog06", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	InsertLog(ctx, pool, "getlog06", "stdout", "out1")
	InsertLog(ctx, pool, "getlog06", "stderr", "err1")
	InsertLog(ctx, pool, "getlog06", "stdout", "out2")
	InsertLog(ctx, pool, "getlog06", "stdout", "out3")

	logs, err := GetLogs(ctx, pool, "getlog06", "stdout", 2, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("len(logs) = %d, want 2", len(logs))
	}
	if logs[0].Data != "out1" || logs[1].Data != "out2" {
		t.Errorf("stream+head returned wrong logs: %v", logs)
	}
}

func TestGetLogs_StreamWithTail(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog07", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	InsertLog(ctx, pool, "getlog07", "stdout", "out1")
	InsertLog(ctx, pool, "getlog07", "stderr", "err1")
	InsertLog(ctx, pool, "getlog07", "stdout", "out2")
	InsertLog(ctx, pool, "getlog07", "stdout", "out3")

	logs, err := GetLogs(ctx, pool, "getlog07", "stdout", 2, true)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("len(logs) = %d, want 2", len(logs))
	}
	if logs[0].Data != "out2" || logs[1].Data != "out3" {
		t.Errorf("stream+tail returned wrong logs: %v", logs)
	}
}

func TestGetLogs_NonExistentTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	logs, err := GetLogs(ctx, pool, "nonexistent", "", 0, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("len(logs) = %d, want 0", len(logs))
	}
}

func TestGetLogs_EmptyLogs(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := CreateTask(ctx, pool, &Task{ID: "getlog08", Command: "test", Status: StatusRunning, Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	logs, err := GetLogs(ctx, pool, "getlog08", "", 0, false)
	if err != nil {
		t.Fatalf("GetLogs() error = %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("len(logs) = %d, want 0", len(logs))
	}
}

// === Cancel Tests ===

func TestRequestCancel_NonExistentTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	status, err := RequestCancel(ctx, pool, "nonexistent")
	if err != nil {
		t.Fatalf("RequestCancel() error = %v", err)
	}
	if status != nil {
		t.Errorf("expected nil status for non-existent task, got %v", *status)
	}
}

func TestRequestCancel_PendingTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "cancel01", Command: "echo test", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}

	status, err := RequestCancel(ctx, pool, "cancel01")
	if err != nil {
		t.Fatalf("RequestCancel() error = %v", err)
	}
	if status == nil || *status != StatusPending {
		t.Errorf("expected original status 'pending', got %v", status)
	}

	// Verify task is now cancelled
	var newStatus string
	var finishedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT status, finished_at FROM tasks WHERE id = $1", "cancel01").Scan(&newStatus, &finishedAt)
	if err != nil {
		t.Fatal(err)
	}
	if newStatus != string(StatusCancelled) {
		t.Errorf("status = %s, want cancelled", newStatus)
	}
	if finishedAt == nil {
		t.Error("finished_at should be set")
	}
}

func TestRequestCancel_RunningTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "cancel02", Command: "sleep 100", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	// Claim to make it running
	if _, err := ClaimTask(ctx, pool, "worker1", &WorkerInfo{Hostname: "test"}, nil); err != nil {
		t.Fatal(err)
	}

	status, err := RequestCancel(ctx, pool, "cancel02")
	if err != nil {
		t.Fatalf("RequestCancel() error = %v", err)
	}
	if status == nil || *status != StatusRunning {
		t.Errorf("expected original status 'running', got %v", status)
	}

	// Verify cancel_requested_at is set but status still running
	var newStatus string
	var cancelRequestedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT status, cancel_requested_at FROM tasks WHERE id = $1", "cancel02").Scan(&newStatus, &cancelRequestedAt)
	if err != nil {
		t.Fatal(err)
	}
	if newStatus != string(StatusRunning) {
		t.Errorf("status = %s, want running (worker should change it)", newStatus)
	}
	if cancelRequestedAt == nil {
		t.Error("cancel_requested_at should be set")
	}
}

func TestRequestCancel_CompletedTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "cancel03", Command: "echo done", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	ClaimTask(ctx, pool, "worker1", &WorkerInfo{Hostname: "test"}, nil)
	CompleteTask(ctx, pool, "cancel03", StatusCompleted, 0)

	status, err := RequestCancel(ctx, pool, "cancel03")
	if err != nil {
		t.Fatalf("RequestCancel() error = %v", err)
	}
	if status == nil || *status != StatusCompleted {
		t.Errorf("expected original status 'completed', got %v", status)
	}

	// Verify task is still completed (not changed)
	var newStatus string
	pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", "cancel03").Scan(&newStatus)
	if newStatus != string(StatusCompleted) {
		t.Errorf("status should remain completed, got %s", newStatus)
	}
}

func TestRequestCancel_FailedTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "cancel04", Command: "exit 1", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	ClaimTask(ctx, pool, "worker1", &WorkerInfo{Hostname: "test"}, nil)
	CompleteTask(ctx, pool, "cancel04", StatusFailed, 1)

	status, err := RequestCancel(ctx, pool, "cancel04")
	if err != nil {
		t.Fatalf("RequestCancel() error = %v", err)
	}
	if status == nil || *status != StatusFailed {
		t.Errorf("expected original status 'failed', got %v", status)
	}
}

func TestRequestCancel_AlreadyCancelledTask(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "cancel05", Command: "echo test", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	// Cancel it first
	RequestCancel(ctx, pool, "cancel05")

	// Try to cancel again
	status, err := RequestCancel(ctx, pool, "cancel05")
	if err != nil {
		t.Fatalf("RequestCancel() error = %v", err)
	}
	if status == nil || *status != StatusCancelled {
		t.Errorf("expected original status 'cancelled', got %v", status)
	}
}

func TestRequestCancel_RunningTwice(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "cancel06", Command: "sleep 100", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	ClaimTask(ctx, pool, "worker1", &WorkerInfo{Hostname: "test"}, nil)

	// First cancel
	status1, _ := RequestCancel(ctx, pool, "cancel06")
	if status1 == nil || *status1 != StatusRunning {
		t.Errorf("first cancel: expected 'running', got %v", status1)
	}

	// Get the cancel_requested_at time
	var firstCancelTime time.Time
	pool.QueryRow(ctx, "SELECT cancel_requested_at FROM tasks WHERE id = $1", "cancel06").Scan(&firstCancelTime)

	time.Sleep(10 * time.Millisecond)

	// Second cancel
	status2, _ := RequestCancel(ctx, pool, "cancel06")
	if status2 == nil || *status2 != StatusRunning {
		t.Errorf("second cancel: expected 'running', got %v", status2)
	}

	// Verify cancel_requested_at didn't change (second cancel was no-op)
	var secondCancelTime time.Time
	pool.QueryRow(ctx, "SELECT cancel_requested_at FROM tasks WHERE id = $1", "cancel06").Scan(&secondCancelTime)

	if !firstCancelTime.Equal(secondCancelTime) {
		t.Errorf("cancel_requested_at changed on second cancel: %v -> %v", firstCancelTime, secondCancelTime)
	}
}

// === Cancel Integration/NOTIFY Tests ===

// Test 14: Task-specific cancel channel
func TestCancel_TaskSpecificChannel(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create two listener connections for different task channels
	conn1, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect conn1: %v", err)
	}
	defer conn1.Close(ctx)

	conn2, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect conn2: %v", err)
	}
	defer conn2.Close(ctx)

	// Listen on different task-specific channels
	conn1.Exec(ctx, "LISTEN task_cancel_task001")
	conn2.Exec(ctx, "LISTEN task_cancel_task002")

	// Send notification for task001
	pool.Exec(ctx, "NOTIFY task_cancel_task001")

	// conn1 should receive notification
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	n1, err1 := conn1.WaitForNotification(waitCtx)
	if err1 != nil {
		t.Errorf("conn1 failed to receive: %v", err1)
	}
	if n1 != nil && n1.Channel != "task_cancel_task001" {
		t.Errorf("conn1 channel = %s, want task_cancel_task001", n1.Channel)
	}

	// conn2 should NOT receive (different channel) - use short timeout
	waitCtx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()

	_, err2 := conn2.WaitForNotification(waitCtx2)
	if err2 == nil {
		t.Error("conn2 should not have received notification for task001")
	}
}

// Test 15: Cancel confirmation sent by worker
func TestCancel_ConfirmationSent(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	taskID := "confirmtest01"

	// Listen for confirmation
	conn, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(ctx)

	confirmChannel := fmt.Sprintf("task_cancelled_%s", taskID)
	conn.Exec(ctx, "LISTEN "+confirmChannel)

	// Simulate worker sending confirmation
	pool.Exec(ctx, fmt.Sprintf("NOTIFY %s", confirmChannel))

	// Should receive confirmation
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	notif, err := conn.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("failed to receive confirmation: %v", err)
	}
	if notif.Channel != confirmChannel {
		t.Errorf("channel = %s, want %s", notif.Channel, confirmChannel)
	}
}

// Test 16: Cancel timeout when worker unresponsive
func TestCancel_Timeout(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	taskID := "timeouttest01"

	// Listen for confirmation (but no one will send it)
	conn, err := pgx.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(ctx)

	confirmChannel := fmt.Sprintf("task_cancelled_%s", taskID)
	conn.Exec(ctx, "LISTEN "+confirmChannel)

	// Send cancel request (no worker to respond)
	pool.Exec(ctx, fmt.Sprintf("NOTIFY task_cancel_%s", taskID))

	// Wait for confirmation with short timeout
	waitCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	_, err = conn.WaitForNotification(waitCtx)

	// Should timeout (no confirmation received)
	if err == nil {
		t.Error("expected timeout, got notification")
	}
	if waitCtx.Err() != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", waitCtx.Err())
	}
}

// === Delete Task Tests ===

func TestDeleteTask_Exists(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "del001", Command: "echo test", Status: StatusPending, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}

	deleted, err := DeleteTask(ctx, pool, "del001")
	if err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if !deleted {
		t.Error("DeleteTask() returned false, want true")
	}

	// Verify task is gone
	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM tasks WHERE id = $1", "del001").Scan(&count)
	if count != 0 {
		t.Errorf("task still exists after delete")
	}
}

func TestDeleteTask_NotExists(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	deleted, err := DeleteTask(ctx, pool, "nonexistent")
	if err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if deleted {
		t.Error("DeleteTask() returned true for non-existent task")
	}
}

func TestDeleteTask_CascadesLogs(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	task := &Task{ID: "del002", Command: "echo test", Status: StatusRunning, Tags: map[string]string{}}
	if err := CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}

	// Insert some logs
	InsertLog(ctx, pool, "del002", "stdout", "line1")
	InsertLog(ctx, pool, "del002", "stdout", "line2")
	InsertLog(ctx, pool, "del002", "stderr", "error1")

	// Verify logs exist
	var logCount int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1", "del002").Scan(&logCount)
	if logCount != 3 {
		t.Fatalf("expected 3 logs, got %d", logCount)
	}

	// Delete task
	deleted, err := DeleteTask(ctx, pool, "del002")
	if err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if !deleted {
		t.Error("DeleteTask() returned false")
	}

	// Verify logs are also deleted (CASCADE)
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM task_logs WHERE task_id = $1", "del002").Scan(&logCount)
	if logCount != 0 {
		t.Errorf("logs still exist after task delete, count = %d", logCount)
	}
}

func TestDeleteTask_DifferentStatuses(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	statuses := []TaskStatus{StatusPending, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled}

	for _, status := range statuses {
		taskID := "delstat" + string(status)
		task := &Task{ID: taskID, Command: "echo test", Status: status, Tags: map[string]string{}}
		if err := CreateTask(ctx, pool, task); err != nil {
			t.Fatal(err)
		}

		deleted, err := DeleteTask(ctx, pool, taskID)
		if err != nil {
			t.Fatalf("DeleteTask(%s) error = %v", status, err)
		}
		if !deleted {
			t.Errorf("DeleteTask(%s) returned false", status)
		}
	}
}

func TestRegisterWorker(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	err := RegisterWorker(ctx, pool, "worker01", 1234, "testhost", "/tmp/workdir")
	if err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}

	// Verify worker was registered
	workers, err := ListWorkers(ctx, pool, nil)
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 1 {
		t.Fatalf("len(workers) = %d, want 1", len(workers))
	}
	if workers[0].ID != "worker01" {
		t.Errorf("worker.ID = %s, want worker01", workers[0].ID)
	}
	if workers[0].PID != 1234 {
		t.Errorf("worker.PID = %d, want 1234", workers[0].PID)
	}
	if workers[0].Hostname != "testhost" {
		t.Errorf("worker.Hostname = %s, want testhost", workers[0].Hostname)
	}
	if workers[0].Status != WorkerStatusRunning {
		t.Errorf("worker.Status = %s, want running", workers[0].Status)
	}
}

func TestUnregisterWorker(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Register worker
	if err := RegisterWorker(ctx, pool, "worker02", 5678, "testhost", "/tmp/workdir"); err != nil {
		t.Fatal(err)
	}

	// Unregister worker
	if err := UnregisterWorker(ctx, pool, "worker02"); err != nil {
		t.Fatalf("UnregisterWorker() error = %v", err)
	}

	// Verify worker was marked as stopped
	workers, err := ListWorkers(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 {
		t.Fatal("expected 1 worker")
	}
	if workers[0].Status != WorkerStatusStopped {
		t.Errorf("worker.Status = %s, want stopped", workers[0].Status)
	}
	if workers[0].StoppedAt == nil {
		t.Error("worker.StoppedAt should not be nil")
	}
}

func TestUpdateHeartbeat(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Register worker
	if err := RegisterWorker(ctx, pool, "worker03", 9999, "testhost", "/tmp/workdir"); err != nil {
		t.Fatal(err)
	}

	// Get initial heartbeat
	workers, _ := ListWorkers(ctx, pool, nil)
	initialHeartbeat := workers[0].LastHeartbeat

	// Wait a moment and update heartbeat
	time.Sleep(10 * time.Millisecond)
	if err := UpdateHeartbeat(ctx, pool, "worker03"); err != nil {
		t.Fatalf("UpdateHeartbeat() error = %v", err)
	}

	// Verify heartbeat was updated
	workers, _ = ListWorkers(ctx, pool, nil)
	if !workers[0].LastHeartbeat.After(initialHeartbeat) {
		t.Error("heartbeat was not updated")
	}
}

func TestListWorkers_FilterByStatus(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Register two workers, unregister one
	RegisterWorker(ctx, pool, "worker04", 1111, "host1", "/tmp/1")
	RegisterWorker(ctx, pool, "worker05", 2222, "host2", "/tmp/2")
	UnregisterWorker(ctx, pool, "worker05")

	// List running workers only
	running := WorkerStatusRunning
	workers, err := ListWorkers(ctx, pool, &running)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 {
		t.Errorf("len(running workers) = %d, want 1", len(workers))
	}
	if workers[0].ID != "worker04" {
		t.Errorf("worker.ID = %s, want worker04", workers[0].ID)
	}

	// List stopped workers only
	stopped := WorkerStatusStopped
	workers, err = ListWorkers(ctx, pool, &stopped)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 {
		t.Errorf("len(stopped workers) = %d, want 1", len(workers))
	}
	if workers[0].ID != "worker05" {
		t.Errorf("worker.ID = %s, want worker05", workers[0].ID)
	}
}
