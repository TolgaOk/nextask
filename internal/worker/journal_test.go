package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func journalFixture() db.TaskCompletion {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return db.TaskCompletion{TaskID: "journal-fixture", WorkerID: "original-worker", CreatedAt: now.Add(-time.Minute),
		StartedAt: now.Add(-time.Second), FinishedAt: now, Status: db.StatusFailed, ExitCode: 17}
}

func TestCompletionJournalConcurrentSave(t *testing.T) {
	journal := newCompletionJournal(filepath.Join(t.TempDir(), "new", "worker"))
	result := journalFixture()
	var wg sync.WaitGroup
	outcomes := make(chan db.TaskCompletion, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := result
			candidate.FinishedAt = candidate.FinishedAt.Add(time.Duration(i) * time.Microsecond)
			saved, err := journal.save(candidate)
			if err != nil {
				t.Errorf("save: %v", err)
				return
			}
			outcomes <- saved
		}()
	}
	wg.Wait()
	close(outcomes)
	loaded, err := journal.read(completionName(result))
	if err != nil {
		t.Fatal(err)
	}
	for saved := range outcomes {
		if !reflect.DeepEqual(saved, loaded) {
			t.Fatal("concurrent writer replaced the first durable outcome")
		}
	}
	info, err := os.Stat(filepath.Join(journal.dir, completionName(result)))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("record permissions: %v %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(journal.dir, ".pending-interrupted"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	names, err := journal.pending()
	if err != nil || len(names) != 1 || names[0] != completionName(result) {
		t.Fatalf("incomplete writes exposed: %v %v", names, err)
	}
	for range 2 {
		if err := journal.acknowledge(loaded); err != nil {
			t.Fatal(err)
		}
	}
	if names, err := journal.pending(); err != nil || len(names) != 0 {
		t.Fatalf("acknowledgement left pending results: %v %v", names, err)
	}
}

func TestCompletionJournalRejectsBadRecords(t *testing.T) {
	for _, mode := range []string{"partial", "version", "claim", "trailing", "symlink", "fifo"} {
		t.Run(mode, func(t *testing.T) {
			journal := newCompletionJournal(t.TempDir())
			result := journalFixture()
			if _, err := journal.save(result); err != nil {
				t.Fatal(err)
			}
			name := completionName(result)
			file := filepath.Join(journal.dir, name)
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			record := completionRecord{Version: journalVersion, TaskCompletion: result}
			if mode == "version" {
				record.Version++
			} else if mode == "claim" {
				record.WorkerID = "different-claim"
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "partial":
				data = []byte("{")
			case "trailing":
				data = append(data, []byte(" {}")...)
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, file); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(file, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "symlink" && mode != "fifo" {
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := journal.read(name); err == nil {
				t.Fatal("invalid record accepted")
			}
			if _, err := os.Lstat(file); err != nil {
				t.Fatal("bad record was discarded")
			}
		})
	}
}
