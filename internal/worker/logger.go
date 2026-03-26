package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

const (
	flushLines    = 100
	flushInterval = 50 * time.Millisecond
	bufferSize    = 1000
)

// Logger defines the interface for capturing task output.
type Logger interface {
	Log(ctx context.Context, stream, data string)
}

// logLine is a buffered log entry waiting to be flushed.
type logLine struct {
	stream string
	data   string
}

// TaskLogger buffers log lines and flushes them to the database in batches.
// Lines are also written to local files immediately for durability.
type TaskLogger struct {
	pool   *pgxpool.Pool
	taskID string
	stdout *os.File
	stderr *os.File

	lines    chan logLine
	done     chan struct{}
	once     sync.Once
	notifyWg sync.WaitGroup
}

// NewTaskLogger creates a batching logger that writes to DB and files.
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

	l := &TaskLogger{
		pool:   pool,
		taskID: taskID,
		stdout: stdout,
		stderr: stderr,
		lines:  make(chan logLine, bufferSize),
		done:   make(chan struct{}),
	}
	go l.run()
	return l, nil
}

// Log writes a line to files immediately and buffers it for batched DB insertion.
// Non-blocking: if the buffer is full, the line is written to file only.
func (l *TaskLogger) Log(ctx context.Context, stream, data string) {
	if ctx.Err() != nil {
		return
	}

	// File write — immediate, never lost.
	switch stream {
	case "stdout":
		fmt.Fprintln(l.stdout, data)
	case "stderr":
		fmt.Fprintln(l.stderr, data)
	}

	// Buffer for batch DB insert — blocks if buffer is full (back-pressure).
	select {
	case l.lines <- logLine{stream: stream, data: data}:
	case <-ctx.Done():
	}
}

// Close flushes remaining buffered lines, waits for in-flight notifies,
// and closes log files.
func (l *TaskLogger) Close() error {
	l.once.Do(func() { close(l.lines) })
	<-l.done
	l.notifyWg.Wait()

	var firstErr error
	if err := l.stdout.Close(); err != nil {
		firstErr = err
	}
	if err := l.stderr.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// run is the flush goroutine. It collects lines and flushes when
// the batch is full or the flush interval expires.
func (l *TaskLogger) run() {
	defer close(l.done)

	buf := make([]db.LogEntry, 0, flushLines)
	timer := time.NewTimer(flushInterval)
	timer.Stop()
	timerRunning := false

	for {
		select {
		case line, ok := <-l.lines:
			if !ok {
				// Channel closed — flush remaining.
				if len(buf) > 0 {
					l.flush(buf)
				}
				return
			}
			buf = append(buf, db.LogEntry{Stream: line.stream, Data: line.data})
			if !timerRunning {
				timer.Reset(flushInterval)
				timerRunning = true
			}
			if len(buf) >= flushLines {
				timer.Stop()
				timerRunning = false
				l.flush(buf)
				buf = buf[:0]
			}

		case <-timer.C:
			timerRunning = false
			if len(buf) > 0 {
				l.flush(buf)
				buf = buf[:0]
			}
		}
	}
}

// flush inserts a batch of log lines into the DB and sends a single NOTIFY.
func (l *TaskLogger) flush(entries []db.LogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	maxID, err := db.InsertLogBatch(ctx, l.pool, l.taskID, entries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log batch insert failed (%d lines): %s\n", len(entries), db.HumanError(err))
		return
	}

	// Async NOTIFY — best-effort, consumer has poll fallback.
	channel := db.FromTaskChannel(l.taskID)
	l.notifyWg.Add(1)
	go func() {
		defer l.notifyWg.Done()
		if err := db.Notify(context.Background(), l.pool, channel, db.TaskLogEvent{ID: maxID}); err != nil {
			fmt.Fprintf(os.Stderr, "log notify failed: %s\n", db.HumanError(err))
		}
	}()
}

// DBLogger writes log lines to the database synchronously (used before task dir exists).
// Not batched — only used for a few status messages, not high-throughput output.
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

// Log writes a line to the specified stream and notifies listeners.
func (l *DBLogger) Log(ctx context.Context, stream, data string) {
	if ctx.Err() != nil {
		return
	}

	id, err := db.InsertLog(ctx, l.pool, l.taskID, stream, data)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "log insert failed: %s\n", db.HumanError(err))
		}
		return
	}

	channel := db.FromTaskChannel(l.taskID)
	if err := db.Notify(ctx, l.pool, channel, db.TaskLogEvent{ID: id}); err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "log notify failed: %s\n", db.HumanError(err))
		}
	}
}
