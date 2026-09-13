package worker

import (
	"context"
	"fmt"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
	"os"
	"testing"
	"time"
)

func TestConcurrentWorkersWithHeartbeats(t *testing.T) {
	pool := setupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	claims := t.TempDir()
	const count = 24
	for i := range count {
		task := &db.Task{ID: fmt.Sprintf("shard-%02d", i), Command: "mkdir " + integrations.Quote(claims) + `/"$NEXTASK_TASK_ID"; echo claimed`, Status: db.StatusPending, SourceType: "noop"}
		// Fail a duplicate claim rather than allowing a later echo to mask it.
		task.Command = "set -e; " + task.Command
		if err := db.CreateTask(ctx, pool, task); err != nil {
			t.Fatal(err)
		}
	}
	idle := 300 * time.Millisecond
	done := make(chan error, 4)
	for i := range 4 {
		w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: fmt.Sprintf("batch-%d", i), ExitIfIdle: &idle, HeartbeatInterval: 2 * time.Millisecond, LogFlushLines: 16, LogFlushInterval: 10 * time.Millisecond, LogBufferSize: 64})
		if err != nil {
			t.Fatal(err)
		}
		go func() { defer w.Close(); done <- w.Run(ctx) }()
	}
	for range 4 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("workers did not finish")
		}
	}
	var completed, uniqueWorkers, logCount int
	if err := pool.QueryRow(ctx, "SELECT count(*), count(DISTINCT worker_id) FROM tasks WHERE status='completed' AND exit_code=0").Scan(&completed, &uniqueWorkers); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM task_logs WHERE stream='stdout' AND data='claimed'").Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(claims)
	if err != nil {
		t.Fatal(err)
	}
	if completed != count || len(entries) != count || logCount != count || uniqueWorkers < 2 {
		t.Fatalf("completed=%d claims=%d logs=%d workers=%d", completed, len(entries), logCount, uniqueWorkers)
	}
}
