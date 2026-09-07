package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestExecutorFileLimit(t *testing.T) {
	if os.Getenv("NEXTASK_TEST_FILE_LIMIT") != "1" {
		_ = getTestDBURL(t)
		cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorFileLimit$")
		cmd.Env = append(os.Environ(), "NEXTASK_TEST_FILE_LIMIT=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("file-limit subprocess: %v\n%s", err, out)
		}
		return
	}
	pool := setupTestDB(t)
	// Limit real file writes in this subprocess. Task stdout uses a pipe, so
	// the failure occurs in the worker's local log file, not in the payload.
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)
	limit := original
	limit.Cur = 1024
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	defer syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original)
	for _, code := range []int{0, 17} {
		task := &db.Task{ID: fmt.Sprintf("file-limit-%d", code), Command: fmt.Sprintf("printf '%%4096s\\n' data; exit %d", code), Status: db.StatusRunning, SourceType: "noop"}
		if err := db.CreateTask(context.Background(), pool, task); err != nil {
			t.Fatal(err)
		}
		executor := &Executor{Pool: pool, Workdir: t.TempDir()}
		result := executor.Execute(context.Background(), task)
		want := code
		if want == 0 {
			want = 1
		}
		if result.Code != want || result.Err == nil || !strings.Contains(result.Err.Error(), "capture task logs") {
			t.Fatalf("file failure not reflected in result: code=%d error=%v", result.Code, result.Err)
		}
	}
}
