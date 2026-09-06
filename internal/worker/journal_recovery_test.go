package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
)

func TestRecoverCompletionJournal(t *testing.T) {
	for _, mode := range []string{"pending-write", "committed", "cancelled", "deleted", "reused"} {
		t.Run(mode, func(t *testing.T) {
			pool := setupTestDB(t)
			defer pool.Close()
			ctx := context.Background()
			root := t.TempDir()
			task := &db.Task{ID: "recover-result", Command: "touch must-not-run", Status: db.StatusPending, SourceType: "noop"}
			if err := db.CreateTask(ctx, pool, task); err != nil {
				t.Fatal(err)
			}
			claim, err := db.ClaimTask(ctx, pool, "old-worker", nil, nil)
			if err != nil || claim == nil {
				t.Fatalf("claim: %v", err)
			}
			result := db.TaskCompletion{TaskID: claim.ID, WorkerID: *claim.WorkerID, CreatedAt: claim.CreatedAt,
				StartedAt: *claim.StartedAt, FinishedAt: time.Now().UTC().Truncate(time.Microsecond), Status: db.StatusCompleted, ExitCode: 0}
			if mode == "cancelled" {
				result.Status, result.ExitCode = db.StatusCancelled, -1
			}
			journal := newCompletionJournal(root)
			if _, err := journal.save(result); err != nil {
				t.Fatal(err)
			}
			if mode == "committed" {
				if ok, err := db.CompleteClaim(ctx, pool, result); err != nil || !ok {
					t.Fatalf("commit: %t %v", ok, err)
				}
			}
			if mode == "deleted" || mode == "reused" {
				if _, err := db.DeleteTask(ctx, pool, task.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reused" {
				if err := db.CreateTask(ctx, pool, task); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ClaimTask(ctx, pool, "old-worker", nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			// Replay must not delete a directory it did not create, even with --rm.
			directory := filepath.Join(root, task.ID)
			if err := os.Mkdir(directory, 0755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(directory, "preserved")
			if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
			// The next payload may start only after pending journal records are acknowledged.
			next := &db.Task{ID: "after-recovery", Command: "test ! -e " + integrations.Quote(filepath.Join(journal.dir, completionName(result))), Status: db.StatusPending, SourceType: "noop"}
			if err := db.CreateTask(ctx, pool, next); err != nil {
				t.Fatal(err)
			}
			w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Name: "replacement-worker", Once: true, Rm: true})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			if err := w.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if names, err := journal.pending(); err != nil || len(names) != 0 {
				t.Fatalf("journal not acknowledged: %v %v", names, err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "retained" {
				t.Fatal("recovery removed unrelated task files")
			}
			stored, err := db.GetTask(ctx, pool, task.ID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "deleted":
				if stored != nil {
					t.Fatal("deleted task resurrected")
				}
			case "reused":
				if stored == nil || stored.Status != db.StatusRunning || stored.FinishedAt != nil {
					t.Fatal("reused task ID overwritten")
				}
			default:
				if stored == nil || stored.Status != result.Status || stored.ExitCode == nil || *stored.ExitCode != result.ExitCode || stored.FinishedAt == nil || !stored.FinishedAt.Equal(result.FinishedAt) {
					t.Fatal("original result or timestamp lost")
				}
			}
			next, err = db.GetTask(ctx, pool, next.ID, time.Minute)
			if err != nil || next == nil || next.Status != db.StatusCompleted {
				t.Fatalf("new work started before recovery: %+v %v", next, err)
			}
		})
	}
}

func TestConcurrentJournalRecovery(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	root := t.TempDir()
	if err := db.CreateTask(ctx, pool, &db.Task{ID: "shared-result", Command: "exit 99", Status: db.StatusPending, SourceType: "noop"}); err != nil {
		t.Fatal(err)
	}
	task, err := db.ClaimTask(ctx, pool, "original", nil, nil)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	journal := newCompletionJournal(root)
	if _, err := journal.save(db.TaskCompletion{TaskID: task.ID, WorkerID: *task.WorkerID, CreatedAt: task.CreatedAt,
		StartedAt: *task.StartedAt, FinishedAt: time.Now().UTC().Truncate(time.Microsecond), Status: db.StatusFailed, ExitCode: 17}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, name := range []string{"recovery-a", "recovery-b"} {
		w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Name: name, Once: true})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer w.Close()
			if err := w.Run(ctx); err != nil {
				t.Errorf("concurrent recovery: %v", err)
			}
		}()
	}
	wg.Wait()
	stored, err := db.GetTask(ctx, pool, task.ID, time.Minute)
	if err != nil || stored == nil || stored.ExitCode == nil || *stored.ExitCode != 17 {
		t.Fatalf("shared recovery lost the original exit code: %+v %v", stored, err)
	}
}

func TestJournalFailurePreservesTaskFiles(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	root := t.TempDir()
	journal := newCompletionJournal(root)
	command := "rmdir " + integrations.Quote(journal.dir) + "; echo blocked > " + integrations.Quote(journal.dir) + "; echo retained > artifact"
	task := &db.Task{ID: "journal-failure", Command: command, Status: db.StatusPending, SourceType: "noop"}
	if err := db.CreateTask(ctx, pool, task); err != nil {
		t.Fatal(err)
	}
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Rm: true, Once: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Run(ctx); err == nil || !strings.Contains(err.Error(), "task files preserved") {
		t.Fatalf("journal failure ignored: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, task.ID, "artifact")); err != nil || string(data) != "retained\n" {
		t.Fatalf("--rm removed files without a durable result: %q %v", data, err)
	}
	var status db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", task.ID).Scan(&status); err != nil || status != db.StatusRunning {
		t.Fatalf("unjournaled result was published: %s %v", status, err)
	}
}

func TestBadJournalPreventsClaims(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	root := t.TempDir()
	journal := newCompletionJournal(root)
	if err := journal.init(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(journal.dir, "bad.json")
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(ctx, pool, &db.Task{ID: "leave-pending", Command: "true", Status: db.StatusPending, SourceType: "noop"}); err != nil {
		t.Fatal(err)
	}
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: root, Once: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Run(ctx); err == nil || !strings.Contains(err.Error(), file) {
		t.Fatalf("missing corrupt journal diagnostic: %v", err)
	}
	var status db.TaskStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id='leave-pending'").Scan(&status); err != nil || status != db.StatusPending {
		t.Fatalf("claimed work with corrupt journal: %s %v", status, err)
	}
}
