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

type readyLogger struct {
	ready chan struct{}
	once  sync.Once
}

func (l *readyLogger) Log(_ context.Context, _ string, text string) {
	if text == "ready" {
		l.once.Do(func() { close(l.ready) })
	}
}

func TestGitWrapperCancellation(t *testing.T) {
	for _, phase := range []string{"fetch", "payload"} {
		t.Run(phase, func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\nexit 0\n"
			payload := "printf 'ready\\n'; sleep 60"
			if phase == "fetch" {
				script = "#!/bin/sh\ncase \"$*\" in *fetch*) printf 'ready\\n'; sleep 60;; esac\n"
				payload = "exit 99"
			}
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			command, err := (integrations.GitSnapshot{Remote: "fixture", Ref: "refs/heads/fixture/task", Commit: strings.Repeat("a", 40)}).Wrap(payload)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			log := &readyLogger{ready: make(chan struct{})}
			executor := &Executor{}
			task := &db.Task{ID: "cancel-wrapper", Command: command}
			dir := t.TempDir()
			done := make(chan *ExitResult, 1)
			go func() { done <- executor.runCommand(ctx, task, dir, log) }()
			select {
			case <-log.ready:
			case result := <-done:
				t.Fatalf("command exited before ready: %+v", result)
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("wrapper never became ready")
			}
			cancel()
			select {
			case result := <-done:
				if result.Code == 0 || result.Code == 99 {
					t.Fatalf("cancellation was lost: %+v", result)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("cancelled wrapper did not exit")
			}
		})
	}
}
