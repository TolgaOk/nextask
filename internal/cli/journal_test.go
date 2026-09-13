package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
)

func TestWorkerJournalSurvivesSIGKILL(t *testing.T) {
	_ = getTestDBURL(t)
	binary := buildTestCLI(t)
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove=%t", remove), func(t *testing.T) {
			pool := setupTestDB(t)
			ctx := context.Background()
			_, err := pool.Exec(ctx, `CREATE FUNCTION test_crash_completion() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					IF NEW.status = 'completed' THEN PERFORM pg_advisory_xact_lock(7654330); END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER test_crash BEFORE UPDATE ON tasks
				FOR EACH ROW EXECUTE FUNCTION test_crash_completion()`)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Exec(ctx, "DROP TRIGGER test_crash ON tasks; DROP FUNCTION test_crash_completion()")
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(ctx)
			if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock(7654330)"); err != nil {
				t.Fatal(err)
			}
			dir, workdir := t.TempDir(), t.TempDir()
			env := append(isolatedCLIEnv(t), "NEXTASK_DB_URL="+getTestDBURL(t), "GORACE=atexit_sleep_ms=0")
			run := func(args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("CLI %v: %v\n%s", args, err, out)
				}
				return string(out)
			}
			counter := filepath.Join(dir, "executions")
			run("enqueue", "--id", "crash-result", "echo executed >> "+integrations.Quote(counter))
			run("enqueue", "--id", "next-task", "echo after-recovery")
			args := []string{"worker", "--once", "--workdir", workdir, "--_id", "crash-writer"}
			if remove {
				args = append(args, "--rm")
			}
			worker := exec.Command(binary, args...)
			worker.Dir, worker.Env = dir, env
			logPath := filepath.Join(dir, "worker.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			worker.Stdout, worker.Stderr = log, log
			if err := worker.Start(); err != nil {
				log.Close()
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { _ = worker.Wait(); close(done) }()
			t.Cleanup(func() { _ = worker.Process.Kill(); <-done; log.Close() })
			journalDir := filepath.Join(workdir, ".nextask", "completions")
			var files []string
			var blockedPID int
			deadline := time.Now().Add(10 * time.Second)
			for {
				if err := pool.QueryRow(ctx, "SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE wait_event='advisory' AND query LIKE '%UPDATE tasks%' LIMIT 1), 0)").Scan(&blockedPID); err != nil {
					t.Fatal(err)
				}
				files, err = filepath.Glob(filepath.Join(journalDir, "*.json"))
				if err != nil {
					t.Fatal(err)
				}
				if blockedPID != 0 && len(files) == 1 {
					break
				}
				if time.Now().After(deadline) {
					out, _ := os.ReadFile(logPath)
					t.Fatalf("worker did not journal before its DB write:\n%s", out)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if remove {
				if _, err := os.Stat(filepath.Join(workdir, "crash-result")); !os.IsNotExist(err) {
					t.Fatalf("--rm did not remove the owned directory: %v", err)
				}
			}
			if err := worker.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			<-done
			// Abort the blocked server-side write too: it must not commit when the
			// gate opens if PostgreSQL has not detected the dead client yet.
			if _, err := pool.Exec(ctx, "SELECT pg_terminate_backend($1)", blockedPID); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var recorded db.TaskCompletion
			if err := json.Unmarshal(data, &recorded); err != nil {
				t.Fatal(err)
			}
			var status db.TaskStatus
			if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id='crash-result'").Scan(&status); err != nil || status != db.StatusRunning {
				t.Fatalf("crash did not precede DB completion: %s %v", status, err)
			}
			if err := gate.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			out := run("worker", "--once", "--workdir", workdir, "--_id", "crash-reader")
			if !strings.Contains(out, "Recovered result for task crash-result") {
				t.Fatalf("startup did not recover the journal:\n%s", out)
			}
			stored, err := db.GetTask(ctx, pool, "crash-result", time.Minute)
			if err != nil || stored == nil || stored.Status != db.StatusCompleted || stored.ExitCode == nil || *stored.ExitCode != 0 || stored.FinishedAt == nil || !stored.FinishedAt.Equal(recorded.FinishedAt) {
				t.Fatalf("recovered result differs: %+v %v", stored, err)
			}
			if data, err := os.ReadFile(counter); err != nil || string(data) != "executed\n" {
				t.Fatalf("payload was rerun during recovery: %q %v", data, err)
			}
			if pending, err := filepath.Glob(filepath.Join(journalDir, "*.json")); err != nil || len(pending) != 0 {
				t.Fatalf("acknowledged journal remains: %v %v", pending, err)
			}
			stored, err = db.GetTask(ctx, pool, "next-task", time.Minute)
			if err != nil || stored == nil || stored.Status != db.StatusCompleted {
				t.Fatalf("new work did not follow recovery: %+v %v", stored, err)
			}
		})
	}
}
