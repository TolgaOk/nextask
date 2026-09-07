// Package worker implements task execution with source fetching and log capture.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/pkg/namesgenerator"
)

// Worker processes tasks from the queue.
type Worker struct {
	ID                string
	Info              *db.WorkerInfo
	Pool              *pgxpool.Pool
	Executor          *Executor
	Once              bool
	ExitIfIdle        *time.Duration
	dbURL             string
	workdir           string
	heartbeatInterval time.Duration
	tagFilter         map[string]string
	backoffInitial    time.Duration
	backoffMax        time.Duration
	journal           completionJournal
	stdout            io.Writer
	stderr            io.Writer
	ready             func() error
}

// Config contains worker configuration options.
type Config struct {
	// Ready runs after startup and recovery, before claiming tasks. An error
	// aborts startup and releases the registration and background goroutines.
	Ready func() error
	// Stdout and Stderr receive worker diagnostics; nil uses the process streams.
	// Writers must support concurrent writes.
	Stdout            io.Writer
	Stderr            io.Writer
	DBURL             string
	Workdir           string
	Name              string
	Once              bool
	Rm                bool
	ExitIfIdle        *time.Duration
	HeartbeatInterval time.Duration
	TagFilter         map[string]string
	BackoffInitial    time.Duration
	BackoffMax        time.Duration
	LogFlushLines     int
	LogFlushInterval  time.Duration
	LogBufferSize     int
}

// New creates a worker with the given configuration.
func New(ctx context.Context, cfg Config) (*Worker, error) {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	pool, err := db.Connect(ctx, cfg.DBURL)
	if err != nil {
		return nil, err
	}

	workerID := cfg.Name
	if workerID == "" {
		workerID = namesgenerator.GetRandomName(0)
	}

	hostname, _ := os.Hostname()
	workerInfo := &db.WorkerInfo{
		Hostname: hostname,
		OS:       runtime.GOOS,
		PID:      os.Getpid(),
	}

	// Set defaults for backoff
	backoffInitial := cfg.BackoffInitial
	if backoffInitial == 0 {
		backoffInitial = 1 * time.Second
	}
	backoffMax := cfg.BackoffMax
	if backoffMax == 0 {
		backoffMax = 30 * time.Second
	}

	return &Worker{
		ID:     workerID,
		stdout: cfg.Stdout, stderr: cfg.Stderr, ready: cfg.Ready,
		Info: workerInfo,
		Pool: pool,
		Executor: &Executor{
			Stderr: cfg.Stderr,
			Pool:   pool, DBURL: cfg.DBURL, Workdir: cfg.Workdir,
			LogFlushLines: cfg.LogFlushLines, LogFlushInterval: cfg.LogFlushInterval,
			LogBufferSize: cfg.LogBufferSize, RemoveWorkdir: cfg.Rm,
		},
		Once:              cfg.Once,
		ExitIfIdle:        cfg.ExitIfIdle, // nil = disabled, 0 = exit immediately, >0 = wait duration
		dbURL:             cfg.DBURL,
		workdir:           cfg.Workdir,
		heartbeatInterval: cfg.HeartbeatInterval,
		tagFilter:         cfg.TagFilter,
		backoffInitial:    backoffInitial,
		backoffMax:        backoffMax,
		journal:           newCompletionJournal(cfg.Workdir),
	}, nil
}

// Each retry loop owns its mutable backoff state. Heartbeats, notifications,
// claims, and completion can run concurrently.
func (w *Worker) newBackoff() *backoff.ExponentialBackOff {
	return db.NewBackOff(w.backoffInitial, w.backoffMax)
}

// Close releases database connections.
func (w *Worker) Close() {
	w.Pool.Close()
}

// Run starts the worker loop, processing tasks until context is cancelled.
func (w *Worker) Run(parentCtx context.Context) (runErr error) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	if err := w.journal.init(); err != nil {
		return fmt.Errorf("initialize completion journal: %w", err)
	}

	hostname, _ := os.Hostname()

	// Register worker in DB
	if err := db.RegisterWorker(ctx, w.Pool, w.ID, os.Getpid(), hostname, w.workdir); err != nil {
		return fmt.Errorf("failed to register worker: %w", err)
	}

	defer func() {
		cancel()
		runErr = errors.Join(runErr, w.unregister())
	}()

	// Single notifier for all channels (wake, stop, cancel)
	toWorkerCh := db.ToWorkerChannel(w.ID)
	notifier, err := db.NewNotifier(ctx, w.dbURL, w.newBackoff(), []string{
		db.ToWorkersChannel,
		toWorkerCh,
	}, w.stderr)
	if err != nil {
		return fmt.Errorf("failed to start notifier: %w", err)
	}
	defer func() {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := notifier.Close(closeCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close worker notifier: %w", err))
		}
	}()

	control := watchWorker(ctx, cancel, notifier.C, toWorkerCh, w.stdout)
	defer func() {
		cancel()
		<-control.done
	}()

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		w.runHeartbeat(ctx)
	}()
	defer func() {
		cancel()
		<-heartbeatDone
	}()

	if err := w.recoverCompletions(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.ready != nil {
		if err := w.ready(); err != nil {
			return fmt.Errorf("confirm worker startup: %w", err)
		}
	}
	fmt.Fprintf(w.stdout, "Worker %s started\n", w.ID)

	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if w.ExitIfIdle != nil {
		idleTimer = time.NewTimer(*w.ExitIfIdle)
		idleCh = idleTimer.C
		defer idleTimer.Stop()
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		task, err := w.claimTask(ctx)
		if err != nil {
			if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				return nil
			}
			return fmt.Errorf("failed to claim task: %w", err)
		}

		if task != nil {
			if err := w.processTask(ctx, notifier, control.events, task); err != nil {
				return err
			}
			if idleTimer != nil {
				idleTimer.Reset(*w.ExitIfIdle)
			}
			if w.Once {
				return nil
			}
			continue
		}

		if w.Once {
			if len(w.tagFilter) > 0 {
				filters := make([]string, 0, len(w.tagFilter))
				for k, v := range w.tagFilter {
					filters = append(filters, k+"="+v)
				}
				fmt.Fprintf(w.stdout, "No pending tasks matching filter: %s\n", strings.Join(filters, ", "))
			} else {
				fmt.Fprintln(w.stdout, "No pending tasks")
			}
			return nil
		}

		select {
		case _, ok := <-control.events:
			if !ok {
				return nil
			}
			// wake event — loop to claim
		case <-idleCh:
			fmt.Fprintln(w.stdout, "No tasks received, exiting (idle timeout)")
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// claimTask uses the same transient-error policy as completion and cancellation.
func (w *Worker) claimTask(ctx context.Context) (*db.Task, error) {
	return db.RetryValue(ctx, func() (*db.Task, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return db.ClaimTask(ctx, w.Pool, w.ID, w.Info, w.tagFilter)
	}, backoff.WithBackOff(w.newBackoff()), backoff.WithMaxElapsedTime(0),
		backoff.WithNotify(func(err error, delay time.Duration) {
			fmt.Fprintf(w.stderr, "failed to claim task: %s (retry in %v)\n", db.HumanError(err), delay)
		}))
}

// unregister also runs on partial startup failure, using an independent deadline.
// Confirm shutdown only after the worker record has been updated.
func (w *Worker) unregister() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.UnregisterWorker(ctx, w.Pool, w.ID); err != nil {
		return fmt.Errorf("unregister worker %s: %w", w.ID, err)
	}
	if _, err := w.Pool.Exec(ctx, "SELECT pg_notify($1, 'stopped')", db.FromWorkerChannel(w.ID)); err != nil {
		return fmt.Errorf("notify worker %s stopped: %w", w.ID, err)
	}
	return nil
}

// runHeartbeat periodically updates the worker's heartbeat timestamp.
func (w *Worker) runHeartbeat(ctx context.Context) {
	if w.heartbeatInterval <= 0 {
		return
	}

	ticker := time.NewTicker(w.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hbCtx, hbCancel := context.WithTimeout(ctx, 30*time.Second)
			err := db.Retry(hbCtx, func() error {
				return db.UpdateHeartbeat(hbCtx, w.Pool, w.ID)
			}, backoff.WithBackOff(w.newBackoff()), backoff.WithMaxTries(3))
			if err != nil && ctx.Err() == nil {
				fmt.Fprintf(w.stderr, "heartbeat failed: %v\n", err)
			}
			hbCancel()
		case <-ctx.Done():
			return
		}
	}
}
