package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

// Logger defines the interface for capturing task output.
type Logger interface {
	Log(ctx context.Context, stream, data string)
}

// TaskLogger writes log lines to both the database and files.
type TaskLogger struct {
	pool   *pgxpool.Pool
	taskID string
	stdout *os.File
	stderr *os.File
}

// NewTaskLogger creates a logger that writes to DB and files.
// Creates <taskDir>/.nextask/log/out.txt and err.txt.
func NewTaskLogger(pool *pgxpool.Pool, taskID, taskDir string) (*TaskLogger, error) {
	logDir := filepath.Join(taskDir, ".nextask", "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	stdout, err := os.Create(filepath.Join(logDir, "out.txt"))
	if err != nil {
		return nil, fmt.Errorf("create out.txt: %w", err)
	}

	stderr, err := os.Create(filepath.Join(logDir, "err.txt"))
	if err != nil {
		stdout.Close()
		return nil, fmt.Errorf("create err.txt: %w", err)
	}

	return &TaskLogger{
		pool:   pool,
		taskID: taskID,
		stdout: stdout,
		stderr: stderr,
	}, nil
}

// Log writes a line to the DB and appropriate file based on stream.
func (l *TaskLogger) Log(ctx context.Context, stream, data string) {
	// Check if context is cancelled
	if ctx.Err() != nil {
		return
	}

	// Write to file (always, even if DB fails)
	switch stream {
	case "stdout":
		fmt.Fprintln(l.stdout, data)
	case "stderr":
		fmt.Fprintln(l.stderr, data)
	}

	// Write to DB
	id, err := db.InsertLog(ctx, l.pool, l.taskID, stream, data)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "failed to insert log: %v\n", err)
		}
		return
	}

	channel := db.FromTaskChannel(l.taskID)
	if err := db.Notify(ctx, l.pool, channel, db.TaskLogEvent{ID: id}); err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "failed to notify log: %v\n", err)
		}
	}
}

// Close closes the log files.
func (l *TaskLogger) Close() error {
	var firstErr error
	if err := l.stdout.Close(); err != nil {
		firstErr = err
	}
	if err := l.stderr.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// DBLogger writes log lines to the database only (used before task dir exists).
type DBLogger struct {
	pool   *pgxpool.Pool
	taskID string
}

// NewDBLogger creates a logger that persists output to the database.
func NewDBLogger(pool *pgxpool.Pool, taskID string) *DBLogger {
	return &DBLogger{
		pool:   pool,
		taskID: taskID,
	}
}

// Log writes a line to the specified stream (stdout/stderr) and notifies listeners.
func (l *DBLogger) Log(ctx context.Context, stream, data string) {
	if ctx.Err() != nil {
		return
	}

	id, err := db.InsertLog(ctx, l.pool, l.taskID, stream, data)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "failed to insert log: %v\n", err)
		}
		return
	}

	channel := db.FromTaskChannel(l.taskID)
	if err := db.Notify(ctx, l.pool, channel, db.TaskLogEvent{ID: id}); err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "failed to notify log: %v\n", err)
		}
	}
}
