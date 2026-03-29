// Package worker implements task execution with source fetching and log capture.
package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	Rm                bool
	ExitIfIdle        *time.Duration
	dbURL             string
	workdir           string
	heartbeatInterval time.Duration
	tagFilter         map[string]string
	backoff           *backoff.ExponentialBackOff
}

// Config contains worker configuration options.
type Config struct {
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
		ID:                workerID,
		Info:              workerInfo,
		Pool:              pool,
		Executor:          &Executor{Pool: pool, Workdir: cfg.Workdir, LogFlushLines: cfg.LogFlushLines, LogFlushInterval: cfg.LogFlushInterval, LogBufferSize: cfg.LogBufferSize},
		Once:              cfg.Once,
		Rm:                cfg.Rm,
		ExitIfIdle:        cfg.ExitIfIdle,  // nil = disabled, 0 = exit immediately, >0 = wait duration
		dbURL:             cfg.DBURL,
		workdir:           cfg.Workdir,
		heartbeatInterval: cfg.HeartbeatInterval,
		tagFilter:         cfg.TagFilter,
		backoff:           db.NewBackOff(backoffInitial, backoffMax),
	}, nil
}

// Close releases database connections.
func (w *Worker) Close() {
	w.Pool.Close()
}

// Run starts the worker loop, processing tasks until context is cancelled.
func (w *Worker) Run(parentCtx context.Context) error {
	ctx, cancel := context.WithCancel(parentCtx)

	hostname, _ := os.Hostname()

	// Register worker in DB
	if err := db.RegisterWorker(ctx, w.Pool, w.ID, os.Getpid(), hostname, w.workdir); err != nil {
		cancel()
		return fmt.Errorf("failed to register worker: %w", err)
	}

	// Single notifier for all channels (wake, stop, cancel)
	toWorkerCh := db.ToWorkerChannel(w.ID)
	notifier, err := db.NewNotifier(ctx, w.dbURL, w.backoff, []string{
		db.ToWorkersChannel,
		toWorkerCh,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("failed to start notifier: %w", err)
	}

	// Start heartbeat goroutine
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		w.runHeartbeat(ctx)
	}()

	// Cleanup
	defer func() {
		cancel()

		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()

		notifier.Close(closeCtx)
		<-heartbeatDone

		// Notify and unregister
		unregCtx, unregCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unregCancel()
		fromWorkerCh := db.FromWorkerChannel(w.ID)
		w.Pool.Exec(unregCtx, "SELECT pg_notify($1, 'stopped')", fromWorkerCh)
		db.UnregisterWorker(unregCtx, w.Pool, w.ID)
	}()

	fmt.Printf("Worker %s started\n", w.ID)

	claimBackoff := db.NewBackOff(1*time.Second, 30*time.Second)

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

		task, err := db.ClaimTask(ctx, w.Pool, w.ID, w.Info, w.tagFilter)
		if err != nil {
			wait := claimBackoff.NextBackOff()
			fmt.Fprintf(os.Stderr, "failed to claim task: %v (retry in %v)\n", err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		claimBackoff.Reset()
		w.backoff.Reset()

		if task != nil {
			w.processTask(ctx, cancel, notifier, toWorkerCh, task)
			if w.Rm {
				taskDir := filepath.Join(w.workdir, task.ID)
				if err := os.RemoveAll(taskDir); err != nil {
					fmt.Fprintf(os.Stderr, "cleanup failed: %v\n", err)
				}
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
				fmt.Printf("No pending tasks matching filter: %s\n", strings.Join(filters, ", "))
			} else {
				fmt.Println("No pending tasks")
			}
			return nil
		}

		select {
		case notif, ok := <-notifier.C:
			if !ok {
				return nil
			}
			if notif.Channel == toWorkerCh {
				fmt.Println("Received stop signal, shutting down...")
				return nil
			}
			// wake event — loop to claim
		case <-idleCh:
			fmt.Println("No tasks received, exiting (idle timeout)")
			return nil
		case <-ctx.Done():
			return nil
		}
	}
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
			}, backoff.WithBackOff(w.backoff), backoff.WithMaxTries(3))
			if err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "heartbeat failed: %v\n", err)
			}
			hbCancel()
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) processTask(ctx context.Context, runCancel context.CancelFunc, notifier *db.Notifier, toWorkerCh string, task *db.Task) {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	// Subscribe to task cancel channel on the existing connection
	toTaskCh := db.ToTaskChannel(task.ID)
	if err := notifier.Add(taskCtx, toTaskCh); err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen for cancel: %v\n", err)
		w.finishTask(task, &ExitResult{Code: -1}, false)
		return
	}
	defer notifier.Remove(toTaskCh)

	// Run executor in background
	resultCh := make(chan *ExitResult, 1)
	go func() {
		resultCh <- w.Executor.Execute(taskCtx, task)
	}()

	// Dispatch notifications during execution
	var result *ExitResult
	wasCancelled := false

	for {
		select {
		case result = <-resultCh:
			goto finish

		case notif, ok := <-notifier.C:
			if !ok {
				taskCancel()
				result = <-resultCh
				goto finish
			}
			switch notif.Channel {
			case toTaskCh:
				eventType, _, err := db.ParseEvent(notif.Payload)
				if err == nil && eventType == db.EventTypeCancel {
					wasCancelled = true
					taskCancel()
					result = <-resultCh
					goto finish
				}
			case toWorkerCh:
				fmt.Println("Received stop signal, shutting down...")
				runCancel()
				taskCancel()
				result = <-resultCh
				goto finish
			}

		case <-ctx.Done():
			result = <-resultCh
			goto finish
		}
	}

finish:
	w.finishTask(task, result, wasCancelled)
}

// finishTask logs the result, marks the task complete in the DB, and notifies listeners.
func (w *Worker) finishTask(task *db.Task, result *ExitResult, wasCancelled bool) {
	logCtx, logCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer logCancel()
	log := NewDBLogger(w.Pool, task.ID)

	var status db.TaskStatus
	exitCode := result.Code
	if wasCancelled {
		status = db.StatusCancelled
		exitCode = -1
		if result.Signal != nil {
			log.Log(logCtx, "nextask", fmt.Sprintf("[info] task cancelled (%s)", result.Signal))
		} else {
			log.Log(logCtx, "nextask", "[info] task cancelled")
		}
	} else if exitCode != 0 {
		status = db.StatusFailed
		log.Log(logCtx, "nextask", fmt.Sprintf("[info] %s", result))
	} else {
		status = db.StatusCompleted
		log.Log(logCtx, "nextask", fmt.Sprintf("[info] %s", result))
	}

	// Complete task with retry
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer completeCancel()

	err := db.Retry(completeCtx, func() error {
		return db.CompleteTask(completeCtx, w.Pool, task.ID, status, exitCode)
	}, backoff.WithBackOff(w.backoff))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to complete task: %v\n", err)
	}

	// Notify status (best effort, no retry)
	fromChannel := db.FromTaskChannel(task.ID)
	event := db.TaskStatusEvent{Status: string(status), ExitCode: exitCode}
	if err := db.Notify(completeCtx, w.Pool, fromChannel, event); err != nil {
		fmt.Fprintf(os.Stderr, "failed to notify status: %v\n", err)
	}

	fmt.Printf("Task %s %s (exit %d)\n", task.ID, status, exitCode)
}
