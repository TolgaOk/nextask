package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

// LogConfig holds batching parameters for the task logger.
type LogConfig struct {
	FlushLines    int
	FlushInterval time.Duration
	BufferSize    int
}

// Logger defines the interface for capturing task output.
type Logger interface {
	Log(ctx context.Context, stream, data string)
}

// logLine is a buffered log entry waiting to be flushed.
type logLine struct {
	seq    int
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
	cfg    LogConfig

	seq      atomic.Int64
	lines    chan logLine
	done     chan struct{}
	once     sync.Once
	notifyWg sync.WaitGroup
}

// NewTaskLogger creates a batching logger that writes to DB and files.
func NewTaskLogger(pool *pgxpool.Pool, taskID, taskDir string, cfg LogConfig) (*TaskLogger, error) {
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
		cfg:    cfg,
		lines:  make(chan logLine, cfg.BufferSize),
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
	// Strip null bytes: PostgreSQL TEXT columns cannot store \x00.
	data = strings.ReplaceAll(data, "\x00", "")
	seq := int(l.seq.Add(1))
	select {
	case l.lines <- logLine{seq: seq, stream: stream, data: data}:
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

	buf := make([]db.LogEntry, 0, l.cfg.FlushLines)
	timer := time.NewTimer(l.cfg.FlushInterval)
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
			buf = append(buf, db.LogEntry{Seq: line.seq, Stream: line.stream, Data: line.data})
			if !timerRunning {
				timer.Reset(l.cfg.FlushInterval)
				timerRunning = true
			}
			if len(buf) >= l.cfg.FlushLines {
				timer.Stop()
				timerRunning = false
				if l.flush(buf) {
					buf = buf[:0]
				}
			}

		case <-timer.C:
			timerRunning = false
			if len(buf) > 0 {
				if l.flush(buf) {
					buf = buf[:0]
				}
			}
		}
	}
}

// flush inserts a batch of log lines into the DB and sends a single NOTIFY.
// Returns true if the insert succeeded, false if it should be retried.
func (l *TaskLogger) flush(entries []db.LogEntry) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	maxID, err := db.InsertLogBatch(ctx, l.pool, l.taskID, entries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log batch insert failed (%d lines): %s\n", len(entries), db.HumanError(err))
		return false
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
	return true
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
