package worker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
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
	// Use Background context for logging so logs are captured even after cancellation
	log := NewDBLogger(context.Background(), e.Pool, task.ID)

	src, err := GetSource(task.SourceType)
	if err != nil {
		log.Log("nextask", fmt.Sprintf("[error] unable to instantiate source '%s': %v", task.SourceType, err))
		return &ExitResult{Code: 1, Err: err}
	}
	if err := src.Fetch(ctx, task.SourceConfig, taskDir, log); err != nil {
		log.Log("nextask", fmt.Sprintf("[error] source fetch failed: %v", err))
		return &ExitResult{Code: 1, Err: err}
	}

	init, err := GetInitializer(task.InitType)
	if err != nil {
		log.Log("nextask", fmt.Sprintf("[error] unable to instantiate init '%s': %v", task.InitType, err))
		return &ExitResult{Code: 1, Err: err}
	}
	if err := init.Run(ctx, task.InitConfig, taskDir, log); err != nil {
		log.Log("nextask", fmt.Sprintf("[error] init failed: %v", err))
		return &ExitResult{Code: 1, Err: err}
	}

	log.Log("nextask", fmt.Sprintf("[info] running: %s", task.Command))
	return e.runCommand(ctx, task, taskDir, log)
}

func (e *Executor) runCommand(ctx context.Context, task *db.Task, taskDir string, log Logger) *ExitResult {
	cmd := exec.CommandContext(ctx, "sh", "-c", task.Command)
	cmd.Dir = taskDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		// Send SIGINT to process group (graceful shutdown)
		syscall.Kill(-pgid, syscall.SIGINT)
		// Escalate to SIGKILL after 5 seconds if process doesn't exit
		go func() {
			time.Sleep(5 * time.Second)
			syscall.Kill(-pgid, syscall.SIGKILL)
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

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Log("stdout", scanner.Text())
		}
		done <- struct{}{}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Log("stderr", scanner.Text())
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	err = cmd.Wait()
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
