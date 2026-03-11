package worker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	Pool    *pgxpool.Pool
	Workdir string
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
	log, err := NewTaskLogger(e.Pool, task.ID, taskDir)
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
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			log.Log(ctx, "stdout", scanner.Text())
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			log.Log(ctx, "nextask", fmt.Sprintf("[warn] stdout scan: %v", err))
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			log.Log(ctx, "stderr", scanner.Text())
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			log.Log(ctx, "nextask", fmt.Sprintf("[warn] stderr scan: %v", err))
		}
	}()

	err = cmd.Wait()
	close(processDone)

	wg.Wait()

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
