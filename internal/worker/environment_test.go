package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestTaskEnvironment(t *testing.T) {
	t.Setenv("NEXTASK_TASK_ID", "inherited-task")
	t.Setenv("NEXTASK_DB_URL", "postgres://inherited/nextask")
	t.Setenv("NEXTASK_TEST_VALUE", "preserved")
	task := &db.Task{
		ID:      "task1234",
		Command: `printf '%s\n' "$NEXTASK_TASK_ID" "$NEXTASK_DB_URL" "$NEXTASK_TEST_VALUE"`,
	}
	executor := &Executor{DBURL: "postgres://localhost/nextask"}
	log := &testLogger{}
	result := executor.runCommand(context.Background(), task, t.TempDir(), log)
	if result.Code != 0 || result.Err != nil {
		t.Fatalf("command failed: %+v", result)
	}
	want := "stdout: task1234\nstdout: postgres://localhost/nextask\nstdout: preserved"
	if got := strings.Join(log.logs, "\n"); got != want {
		t.Fatalf("task environment = %q, want %q", got, want)
	}
}
