package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/TolgaOk/nextask/internal/taskexec"
)

// Run waits for the command and its children, then runs a bounded final pass.
// Upload cancellation is separate from command cancellation so finalization gets
// its own deadline even after the task's execution context has been cancelled.
func Run(ctx context.Context, c Config, store Store, id string, command taskexec.Command) *taskexec.Result {
	if command.Dir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return &taskexec.Result{Code: 1, Err: err}
		}
		command.Dir = dir
	}
	if command.Stderr == nil {
		command.Stderr = io.Discard
	}
	var mu sync.Mutex
	command.Stderr = &lockedWriter{writer: command.Stderr, mu: &mu}
	log := func(format string, args ...any) {
		fmt.Fprintf(command.Stderr, "[s3] "+format+"\n", args...)
	}
	syncer := &Syncer{Config: c, Store: store, TaskID: id, TaskDir: command.Dir, Log: log}
	periodic, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if c.Interval == 0 || len(c.Include) == 0 {
			return
		}
		timer := time.NewTimer(c.Interval)
		defer timer.Stop()
		for {
			select {
			case <-periodic.Done():
				return
			case <-timer.C:
			}
			if err := syncer.Sync(periodic, false); err != nil && periodic.Err() == nil {
				log("periodic upload failed: %v", err)
			}
			timer.Reset(c.Interval)
		}
	}()
	result := taskexec.Run(ctx, command)
	stop()
	<-done
	if c.FinalSync {
		final, cancel := context.WithTimeout(context.Background(), c.FinalTimeout)
		err := syncer.Sync(final, true)
		cancel()
		if err != nil {
			log("final upload failed: %v", err)
			if result.Code == 0 && c.OnFinalError == "fail" {
				result = &taskexec.Result{Code: 1, Err: fmt.Errorf("final S3 upload failed")}
			}
		}
	}
	return result
}

// lockedWriter allows command output and uploader diagnostics to share a writer.
type lockedWriter struct {
	writer io.Writer
	mu     *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
