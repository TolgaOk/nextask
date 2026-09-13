// Package taskexec supervises shell commands and their process groups.
package taskexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const StopTimeout = 5 * time.Second

// Result preserves command exit codes and signals across runtime wrappers.
type Result struct {
	Code   int
	Signal os.Signal
	Err    error
}

func (r *Result) String() string {
	if r.Signal != nil {
		return fmt.Sprintf("signal: %s", r.Signal)
	}
	return fmt.Sprintf("exit code: %d", r.Code)
}
func (r *Result) ShellCode() int {
	if r.Code < 0 {
		return 128 - r.Code
	}
	return r.Code
}

type Command struct {
	Text, Dir      string
	Env            []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	CleanupTimeout time.Duration
}

// Run waits for both the shell and its children. Cancellation signals the group,
// then enforces a bound that includes any declared integration cleanup time.
func Run(ctx context.Context, command Command) *Result {
	if err := ctx.Err(); err != nil {
		return &Result{Code: 130, Err: err}
	}
	if command.CleanupTimeout < 0 || command.CleanupTimeout > 24*time.Hour {
		return &Result{Code: 1, Err: errors.New("invalid cleanup timeout")}
	}
	cmd := exec.Command("sh", "-c", command.Text)
	cmd.Dir, cmd.Env = command.Dir, command.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = command.Stdin, command.Stdout, command.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return &Result{Code: 1, Err: err}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		grace := StopTimeout + command.CleanupTimeout
		if command.CleanupTimeout > 0 {
			grace += time.Second
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	err := cmd.Wait()
	// A shell can exit while background children are still writing artifacts.
waitChildren:
	for syscall.Kill(-cmd.Process.Pid, 0) == nil {
		select {
		case <-stopped:
			// SIGKILL has been sent. Orphan zombies may remain until PID 1
			// reaps them, but they can no longer write task files.
			break waitChildren
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(done)
	<-stopped
	result := &Result{}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				result.Code, result.Signal = -int(status.Signal()), status.Signal()
			} else {
				result.Code = exit.ExitCode()
			}
		} else {
			result.Code, result.Err = 1, err
		}
	}
	if ctx.Err() != nil && result.Code == 0 {
		result.Code, result.Signal = -int(syscall.SIGINT), syscall.SIGINT
	}
	return result
}
