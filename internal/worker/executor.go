package worker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
	"github.com/TolgaOk/nextask/internal/taskexec"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor runs task commands and captures their output.
type Executor struct {
	Pool             *pgxpool.Pool
	DBURL            string
	Workdir          string
	LogFlushLines    int
	LogFlushInterval time.Duration
	LogBufferSize    int
}

// ExitResult is shared with integration runtimes.
type ExitResult = taskexec.Result

// Execute runs a task and returns the exit result.
func (e *Executor) Execute(ctx context.Context, task *db.Task) *ExitResult {
	taskDir := filepath.Join(e.Workdir, task.ID)
	dbLog := NewDBLogger(e.Pool, task.ID)

	if err := db.ValidateTaskID(task.ID); err != nil {
		return &ExitResult{Code: 1, Err: err}
	}
	command := task.Command
	if task.ExecutionCommand != nil {
		command = *task.ExecutionCommand
	} else {
		var err error
		command, err = integrations.LegacyCommand(task.SourceType, task.SourceConfig, task.Command)
		if err != nil {
			dbLog.Log(ctx, "nextask", fmt.Sprintf("[error] prepare legacy task: %v", err))
			return &ExitResult{Code: 1, Err: err}
		}
	}
	if err := os.MkdirAll(taskDir, 0755); err != nil {
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
	executable := *task
	executable.Command = command
	return e.runCommand(ctx, &executable, taskDir, log)
}

func (e *Executor) runCommand(ctx context.Context, task *db.Task, taskDir string, log Logger) *ExitResult {
	executable, err := os.Executable()
	if err != nil {
		return &ExitResult{Code: 1, Err: err}
	}
	stdout, outWriter := io.Pipe()
	stderr, errWriter := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); defer stdout.Close(); scanLines(ctx, stdout, "stdout", log) }()
	go func() { defer wg.Done(); defer stderr.Close(); scanLines(ctx, stderr, "stderr", log) }()
	result := taskexec.Run(ctx, taskexec.Command{
		Text: task.Command, Dir: taskDir,
		Env:    append(os.Environ(), "NEXTASK_TASK_ID="+task.ID, "NEXTASK_DB_URL="+e.DBURL, "NEXTASK_EXECUTABLE="+executable),
		Stdout: outWriter, Stderr: errWriter,
		CleanupTimeout: time.Duration(task.CleanupTimeoutMS) * time.Millisecond,
	})
	outWriter.Close()
	errWriter.Close()
	wg.Wait()
	return result
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
