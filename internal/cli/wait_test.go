package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestWaitCLI(t *testing.T) {
	pool := setupTestDB(t)
	binary := buildTestCLI(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		code          int
		notify, ready bool
	}{
		{"existing-success", 0, false, true}, {"existing-failure", 7, false, true},
		{"live-success", 0, true, false}, {"live-failure", 9, true, false},
		{"missed-success", 0, false, false}, {"missed-failure", 11, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, other := tc.name+"-first", tc.name+"-other"
			createWatchTask(t, pool, first, nil)
			createWatchTask(t, pool, other, nil)
			if tc.ready {
				completeWatchTask(t, pool, first, tc.code, false)
			}
			p := startWatchCLI(t, binary, "wait", other, first, first, "--any", "--timeout", "4s")
			if !tc.ready {
				awaitWatchListener(t, pool, "")
				p.stillRunning(t)
				completeWatchTask(t, pool, first, tc.code, tc.notify)
			}
			out := p.result(t, tc.code)
			if strings.Count(out, "task "+first+" ") != 1 {
				t.Fatalf("duplicate or missing completion: %s", out)
			}
			var untouched bool
			if err := pool.QueryRow(ctx, "SELECT status = 'pending' AND cancel_requested_at IS NULL FROM tasks WHERE id = $1", other).Scan(&untouched); err != nil || !untouched {
				t.Fatalf("other task changed: %v", err)
			}
		})
	}
	t.Run("default-all", func(t *testing.T) {
		createWatchTask(t, pool, "all-first", nil)
		createWatchTask(t, pool, "all-second", nil)
		p := startWatchCLI(t, binary, "wait", "all-first", "all-first", "all-second", "--timeout", "5s")
		awaitWatchListener(t, pool, "")
		completeWatchTask(t, pool, "all-first", 13, true)
		p.stillRunning(t)
		completeWatchTask(t, pool, "all-second", 0, false)
		out := p.result(t, 13)
		if strings.Count(out, "task all-first ") != 1 || !strings.Contains(out, "task all-second completed") {
			t.Fatal(out)
		}
	})
	t.Run("notification-is-hint", func(t *testing.T) {
		createWatchTask(t, pool, "hint", nil)
		p := startWatchCLI(t, binary, "wait", "hint", "--any", "--timeout", "5s")
		pid := awaitWatchListener(t, pool, db.FromTaskChannel("hint"))
		if err := db.Notify(ctx, pool, db.FromTaskChannel("hint"), db.TaskStatusEvent{Status: "completed", ExitCode: 0}); err != nil {
			t.Fatal(err)
		}
		p.stillRunning(t)
		if _, err := pool.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			t.Fatal(err)
		}
		completeWatchTask(t, pool, "hint", 17, false)
		p.result(t, 17)
	})
	for _, any := range []bool{false, true} {
		name := "tag-all"
		if any {
			name = "tag-any"
		}
		t.Run(name, func(t *testing.T) {
			tags := map[string]string{"group": name}
			first, late := name+"-first", name+"-late"
			createWatchTask(t, pool, first, tags)
			args := []string{"wait", "--tag", "group=" + name, "--timeout", "6s"}
			if any {
				args = append(args, "--any")
			}
			p := startWatchCLI(t, binary, args...)
			awaitWatchListener(t, pool, db.FromTaskChannel(first))
			// No enqueue hint: periodic discovery must find this task too.
			createWatchTask(t, pool, late, tags)
			awaitWatchListener(t, pool, db.FromTaskChannel(late))
			completeWatchTask(t, pool, late, 0, false)
			if !any {
				p.stillRunning(t)
				completeWatchTask(t, pool, first, 0, true)
			}
			p.result(t, 0)
		})
	}
	t.Run("tag-batch", func(t *testing.T) {
		for i := 0; i < 24; i++ {
			id := fmt.Sprintf("batch-%02d", i)
			createWatchTask(t, pool, id, map[string]string{"group": "batch"})
			completeWatchTask(t, pool, id, 0, false)
		}
		out := startWatchCLI(t, binary, "wait", "--tag", "group=batch", "--timeout", "4s").result(t, 0)
		if strings.Count(out, " completed (exit 0)") != 24 {
			t.Fatalf("batch lost tasks: %s", out)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		createWatchTask(t, pool, "timeout-pending", nil)
		out := startWatchCLI(t, binary, "wait", "timeout-pending", "--timeout", "200ms").result(t, 124)
		if !strings.Contains(out, "timeout-pending still running") {
			t.Fatal(out)
		}
	})
	t.Run("missing", func(t *testing.T) {
		out := startWatchCLI(t, binary, "wait", "missing-task", "--any").result(t, 1)
		if !strings.Contains(out, "task not found: missing-task") {
			t.Fatal(out)
		}
	})
	t.Run("no-tag-matches", func(t *testing.T) {
		out := startWatchCLI(t, binary, "wait", "--tag", "group=missing", "--any").result(t, 1)
		if !strings.Contains(out, "no tasks found matching tags") {
			t.Fatal(out)
		}
	})
	t.Run("becomes-stale", func(t *testing.T) {
		if err := db.RegisterWorker(ctx, pool, "stale-watcher", 1234, "test", "/tmp"); err != nil {
			t.Fatal(err)
		}
		createWatchTask(t, pool, "becomes-stale", nil)
		if _, err := pool.Exec(ctx, "UPDATE tasks SET status = 'running', worker_id = 'stale-watcher' WHERE id = 'becomes-stale'"); err != nil {
			t.Fatal(err)
		}
		p := startWatchCLI(t, binary, "wait", "becomes-stale", "--any", "--timeout", "5s")
		awaitWatchListener(t, pool, db.FromTaskChannel("becomes-stale"))
		if _, err := pool.Exec(ctx, "UPDATE workers SET last_heartbeat = NOW() - INTERVAL '1 hour' WHERE id = 'stale-watcher'"); err != nil {
			t.Fatal(err)
		}
		out := p.result(t, 1)
		if !strings.Contains(out, "worker heartbeat expired") {
			t.Fatal(out)
		}
	})
}
