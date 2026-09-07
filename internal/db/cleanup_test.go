package db

import (
	"context"
	"testing"
	"time"
)

func TestCleanupMigrationPreservesTasks(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	if err := CreateTask(ctx, pool, &Task{ID: "legacy-task", Command: "echo kept", Status: StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE tasks DROP COLUMN cleanup_timeout_ms"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	task, err := GetTask(ctx, pool, "legacy-task", time.Minute)
	if err != nil || task == nil || task.Command != "echo kept" || task.CleanupTimeoutMS != 0 {
		t.Fatalf("migration lost task: %+v %v", task, err)
	}
}
