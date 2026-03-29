package worker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor handles task execution including source fetching and command running.
type Executor struct {
	Pool             *pgxpool.Pool
	Workdir          string
	LogFlushLines    int
	LogFlushInterval time.Duration
	LogBufferSize    int
}

// ExitResult contains information about how a command exited.
type ExitResult struct {
	Code   int
	Signal os.Signal
	Err    error
}

func (r *ExitResult) String() string {
	if r.Signal != nil {
		return fmt.Sprintf("signal: %s", r.Signal)
	}
	return fmt.Sprintf("exit code: %d", r.Code)
}

// Execute runs a task and returns the exit result.
func (e *Executor) Execute(ctx context.Context, task *db.Task) *ExitResult {
	taskDir := filepath.Join(e.Workdir, task.ID)
	dbLog := NewDBLogger(e.Pool, task.ID)

	src, err := GetSource(task.SourceType)
	if err != nil {
		dbLog.Log(ctx, "nextask", fmt.Sprintf("[error] unable to instantiate source '%s': %v", task.SourceType, err))
		return &ExitResult{Code: 1, Err: err}
	}
	if err := src.Fetch(ctx, task.SourceConfig, taskDir, dbLog); err != nil {
		dbLog.Log(ctx, "nextask", fmt.Sprintf("[error] source fetch failed: %v", err))
		return &ExitResult{Code: 1, Err: err}
	}

	// Create task logger with file output now that taskDir exists
	log, err := NewTaskLogger(e.Pool, task.ID, taskDir, LogConfig{
		FlushLines:    e.LogFlushLines,
		FlushInterval: e.LogFlushInterval,
		BufferSize:    e.LogBufferSize,
	})
	if err != nil {
		dbLog.Log(ctx, "nextask", fmt.Sprintf("[error] create task logger: %v", err))
		return &ExitResult{Code: 1, Err: err}
	}
	defer log.Close()

	log.Log(ctx, "nextask", fmt.Sprintf("[info] running: %s", task.Command))
	return e.runCommand(ctx, task, taskDir, log)
}

func (e *Executor) runCommand(ctx context.Context, task *db.Task, taskDir string, log Logger) *ExitResult {
	cmd := exec.CommandContext(ctx, "sh", "-c", task.Command)
	cmd.Dir = taskDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	processDone := make(chan struct{})

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}

		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}

		if err := syscall.Kill(-pgid, syscall.SIGINT); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}

		go func() {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			case <-processDone:
			}
		}()

		return nil
	}
	cmd.WaitDelay = 10 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return &ExitResult{Code: 1, Err: err}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return &ExitResult{Code: 1, Err: err}
	}

	if err := cmd.Start(); err != nil {
		return &ExitResult{Code: 1, Err: err}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanLines(ctx, stdout, "stdout", log)
	}()

	go func() {
		defer wg.Done()
		scanLines(ctx, stderr, "stderr", log)
	}()

	wg.Wait()

	err = cmd.Wait()
	close(processDone)

	if errors.Is(err, exec.ErrWaitDelay) {
		log.Log(ctx, "nextask", "[warn] pipes forced closed after WaitDelay (orphaned child?)")
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			return &ExitResult{Code: 0, Err: err}
		}
		if cmd.ProcessState != nil {
			return &ExitResult{Code: cmd.ProcessState.ExitCode(), Err: err}
		}
		return &ExitResult{Code: 1, Err: err}
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				sig := status.Signal()
				return &ExitResult{Code: -int(sig), Signal: sig}
			}
			return &ExitResult{Code: exitErr.ExitCode()}
		}
		return &ExitResult{Code: 1, Err: err}
	}

	return &ExitResult{Code: 0}
}

const maxLineSize = 1024 * 1024 // 1MB

// scanLines reads lines from r and logs them. Lines longer than maxLineSize
// are truncated but scanning continues — one oversized line doesn't kill
// the rest of the output.
func scanLines(ctx context.Context, r io.Reader, stream string, log Logger) {
	reader := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	truncated := false
	for {
		chunk, isPrefix, err := reader.ReadLine()
		if len(chunk) > 0 {
			if len(line)+len(chunk) <= maxLineSize {
				line = append(line, chunk...)
			} else if !truncated {
				truncated = true
				log.Log(ctx, "nextask", fmt.Sprintf("[warn] %s line truncated at %d bytes", stream, maxLineSize))
			}
		}
		if isPrefix {
			continue
		}
		if len(line) > 0 {
			log.Log(ctx, stream, string(line))
			line = line[:0]
		}
		truncated = false
		if err != nil {
			if err != io.EOF && !errors.Is(err, os.ErrClosed) {
				log.Log(ctx, "nextask", fmt.Sprintf("[warn] %s read: %v", stream, err))
			}
			return
		}
	}
}
