package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LogConfig holds batching parameters for the task logger.
type LogConfig struct {
	Stderr        io.Writer // Diagnostics; nil uses the process stderr. Must support concurrent writes.
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

	seq       atomic.Int64
	lines     chan logLine
	done      chan struct{}
	once      sync.Once
	notifyWg  sync.WaitGroup
	flushCtx  context.Context
	stopFlush context.CancelFunc
	errMu     sync.Mutex
	fileErr   error
	closeErr  error
}

// NewTaskLogger creates a batching logger that writes to DB and files.
func NewTaskLogger(pool *pgxpool.Pool, taskID, taskDir string, cfg LogConfig) (*TaskLogger, error) {
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
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

	flushCtx, stopFlush := context.WithCancel(context.Background())
	l := &TaskLogger{
		pool:      pool,
		taskID:    taskID,
		stdout:    stdout,
		stderr:    stderr,
		cfg:       cfg,
		lines:     make(chan logLine, cfg.BufferSize),
		done:      make(chan struct{}),
		flushCtx:  flushCtx,
		stopFlush: stopFlush,
	}
	go l.run()
	return l, nil
}

// Log writes local output even during cancellation cleanup.
// A full DB queue applies backpressure until cancellation, then keeps only the local copy.
func (l *TaskLogger) Log(ctx context.Context, stream, data string) {
	var err error
	switch stream {
	case "stdout":
		_, err = fmt.Fprintln(l.stdout, data)
	case "stderr":
		_, err = fmt.Fprintln(l.stderr, data)
	}
	if err != nil {
		l.errMu.Lock()
		first := l.fileErr == nil
		if first {
			l.fileErr = fmt.Errorf("write %s log: %w", stream, err)
		}
		diagnostic := l.fileErr
		l.errMu.Unlock()
		if first {
			fmt.Fprintf(l.cfg.Stderr, "[error] %v\n", diagnostic)
			l.enqueue(ctx, "nextask", fmt.Sprintf("[error] %v", diagnostic))
		}
	}
	l.enqueue(ctx, stream, data)
}

func (l *TaskLogger) enqueue(ctx context.Context, stream, data string) {
	// PostgreSQL TEXT requires valid UTF-8 and cannot store NUL bytes.
	data = strings.ToValidUTF8(strings.ReplaceAll(data, "\x00", ""), "\uFFFD")
	seq := int(l.seq.Add(1))
	line := logLine{seq: seq, stream: stream, data: data}
	select {
	case l.lines <- line:
		return
	default:
	}
	select {
	case l.lines <- line:
	case <-ctx.Done():
	}
}

// Close flushes remaining buffered lines, waits for in-flight notifies,
// and closes log files.
func (l *TaskLogger) Close() error {
	l.once.Do(func() {
		close(l.lines)
		l.stopFlush()
		<-l.done
		l.notifyWg.Wait()

		l.errMu.Lock()
		defer l.errMu.Unlock()
		l.closeErr = errors.Join(l.fileErr, l.stdout.Close(), l.stderr.Close())
	})
	return l.closeErr
}

// run keeps at most one batch outside the bounded queue. If the database is
// down, retries apply backpressure instead of growing a second unbounded buffer.
func (l *TaskLogger) run() {
	defer close(l.done)
	limit := max(l.cfg.FlushLines, 1)
	buf := make([]db.LogEntry, 0, limit)
	interval := max(l.cfg.FlushInterval, time.Millisecond)
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		if l.flushCtx.Err() != nil {
			l.drain(buf, limit)
			return
		}
		if len(buf) >= limit {
			if l.flush(l.flushCtx, buf) {
				buf = buf[:0]
			} else if l.flushCtx.Err() == nil {
				select {
				case <-time.After(interval):
				case <-l.flushCtx.Done():
				}
			}
			continue
		}
		select {
		case <-l.flushCtx.Done():
			l.drain(buf, limit)
			return
		case line, ok := <-l.lines:
			if !ok {
				l.drain(buf, limit)
				return
			}
			buf = append(buf, db.LogEntry{Seq: line.seq, Stream: line.stream, Data: line.data})
		case <-timer.C:
			if len(buf) > 0 && l.flush(l.flushCtx, buf) {
				buf = buf[:0]
			}
			timer.Reset(interval)
		}
	}
}

// drain shares one deadline across all remaining batches. Local files retain
// output if the outage lasts longer than this shutdown window.
func (l *TaskLogger) drain(buf []db.LogEntry, limit int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if len(buf) >= limit {
			if !l.flush(ctx, buf) {
				return
			}
			buf = buf[:0]
		}
		line, ok := <-l.lines
		if !ok {
			if len(buf) > 0 {
				l.flush(ctx, buf)
			}
			return
		}
		buf = append(buf, db.LogEntry{Seq: line.seq, Stream: line.stream, Data: line.data})
	}
}

// flush retries an idempotent batch within both its own and its caller's deadline.
func (l *TaskLogger) flush(parent context.Context, entries []db.LogEntry) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	var maxID int
	err := db.Retry(ctx, func() error {
		var err error
		maxID, err = db.InsertLogBatch(ctx, l.pool, l.taskID, entries)
		return err
	}, backoff.WithBackOff(db.NewBackOff(100*time.Millisecond, time.Second)))
	if err != nil {
		if parent.Err() != context.Canceled {
			fmt.Fprintf(l.cfg.Stderr, "log batch insert failed (%d lines): %s\n", len(entries), db.HumanError(err))
		}
		return false
	}

	// Notifications are best effort; consumers can also poll for persisted logs.
	channel := db.FromTaskChannel(l.taskID)
	l.notifyWg.Add(1)
	go func() {
		defer l.notifyWg.Done()
		notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer notifyCancel()
		if err := db.Notify(notifyCtx, l.pool, channel, db.TaskLogEvent{ID: maxID}); err != nil {
			fmt.Fprintf(l.cfg.Stderr, "log notify failed: %s\n", db.HumanError(err))
		}
	}()
	return true
}

// DBLogger writes log lines to the database synchronously (used before task dir exists).
// Not batched — only used for a few status messages, not high-throughput output.
type DBLogger struct {
	stderr io.Writer
	pool   *pgxpool.Pool
	taskID string
}

// NewDBLogger creates a logger that persists output to the database.
func NewDBLogger(pool *pgxpool.Pool, taskID string, stderr io.Writer) *DBLogger {
	if stderr == nil {
		stderr = os.Stderr
	}
	return &DBLogger{
		stderr: stderr,
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
			fmt.Fprintf(l.stderr, "log insert failed: %s\n", db.HumanError(err))
		}
		return
	}

	channel := db.FromTaskChannel(l.taskID)
	if err := db.Notify(ctx, l.pool, channel, db.TaskLogEvent{ID: id}); err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(l.stderr, "log notify failed: %s\n", db.HumanError(err))
		}
	}
}
