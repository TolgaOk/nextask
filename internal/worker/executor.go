package worker

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

// Executor handles task execution including source fetching and command running.
type Executor struct {
	Pool    *pgxpool.Pool
	Workdir string
}

// Execute runs a task and returns its exit code.
func (e *Executor) Execute(ctx context.Context, task *db.Task) int {
	taskDir := filepath.Join(e.Workdir, task.ID)
	log := NewDBLogger(ctx, e.Pool, task.ID)

	src, err := GetSource(task.SourceType)
	if err != nil {
		log.Log("nextask", fmt.Sprintf("[error] unable to instantiate source '%s': %v", task.SourceType, err))
		return 1
	}
	if err := src.Fetch(ctx, task.SourceConfig, taskDir, log); err != nil {
		log.Log("nextask", fmt.Sprintf("[error] source fetch failed: %v", err))
		return 1
	}

	init, err := GetInitializer(task.InitType)
	if err != nil {
		log.Log("nextask", fmt.Sprintf("[error] unable to instantiate init '%s': %v", task.InitType, err))
		return 1
	}
	if err := init.Run(ctx, task.InitConfig, taskDir, log); err != nil {
		log.Log("nextask", fmt.Sprintf("[error] init failed: %v", err))
		return 1
	}

	log.Log("nextask", fmt.Sprintf("[info] running: %s", task.Command))
	exitCode, err := e.runCommand(ctx, task, taskDir, log)
	if err != nil {
		log.Log("nextask", fmt.Sprintf("[error] command failed: %v", err))
	}
	log.Log("nextask", fmt.Sprintf("[info] exit code: %d", exitCode))

	return exitCode
}

func (e *Executor) runCommand(ctx context.Context, task *db.Task, taskDir string, log Logger) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", task.Command)
	cmd.Dir = taskDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 1, err
	}

	if err := cmd.Start(); err != nil {
		return 1, err
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
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}

	return 0, nil
}
