// Package worker implements task execution with source fetching and log capture.
package worker

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/TolgaOk/nextask/internal/db"
)

// Worker processes tasks from the queue.
type Worker struct {
	ID                string
	Info              *db.WorkerInfo
	Pool              *pgxpool.Pool
	Executor          *Executor
	Once              bool
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
	HeartbeatInterval time.Duration
	TagFilter         map[string]string
	BackoffInitial    time.Duration
	BackoffMax        time.Duration
}

// New creates a worker with the given configuration.
func New(ctx context.Context, cfg Config) (*Worker, error) {
	pool, err := db.Connect(ctx, cfg.DBURL)
	if err != nil {
		return nil, err
	}

	workerID := cfg.Name
	if workerID == "" {
		workerID, _ = gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 8)
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
		Executor:          &Executor{Pool: pool, Workdir: cfg.Workdir},
		Once:              cfg.Once,
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

	// Start listeners with auto-reconnect
	taskListener, err := db.Listen(ctx, w.dbURL, w.backoff, db.ToWorkersChannel)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to listen for %s: %w", db.ToWorkersChannel, err)
	}

	toWorkerCh := db.ToWorkerChannel(w.ID)
	stopListener, err := db.Listen(ctx, w.dbURL, w.backoff, toWorkerCh)
	if err != nil {
		taskListener.Close(context.Background())
		cancel()
		return fmt.Errorf("failed to listen for stop: %w", err)
	}

	// Handle stop signal
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		select {
		case <-stopListener.C:
			fmt.Println("Received stop signal, shutting down...")
			cancel()
		case <-ctx.Done():
		}
	}()

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

		taskListener.Close(closeCtx)
		stopListener.Close(closeCtx)
		<-stopDone
		<-heartbeatDone

		// Notify and unregister
		unregCtx, unregCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unregCancel()
		fromWorkerCh := db.FromWorkerChannel(w.ID)
		w.Pool.Exec(unregCtx, "SELECT pg_notify($1, 'stopped')", fromWorkerCh)
		db.UnregisterWorker(unregCtx, w.Pool, w.ID)
	}()

	fmt.Printf("Worker %s started\n", w.ID)

	for {
		if ctx.Err() != nil {
			return nil
		}

		task, err := db.ClaimTask(ctx, w.Pool, w.ID, w.Info, w.tagFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to claim task: %v\n", err)
			continue
		}

		if task != nil {
			w.processTask(ctx, task)
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
		case <-taskListener.C:
			// wake event received, loop to claim task
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

func (w *Worker) processTask(ctx context.Context, task *db.Task) {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Listen for task cancellation
	toChannel := db.ToTaskChannel(task.ID)
	cancelListener, err := db.Listen(taskCtx, w.dbURL, w.backoff, toChannel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen for cancel: %v\n", err)
		return
	}

	// Handle cancel signal
	cancelDone := make(chan struct{})
	go func() {
		defer close(cancelDone)
		for {
			select {
			case notif, ok := <-cancelListener.C:
				if !ok {
					return
				}
				eventType, _, err := db.ParseEvent(notif.Payload)
				if err != nil {
					continue
				}
				if eventType == db.EventTypeCancel {
					cancel()
					return
				}
			case <-taskCtx.Done():
				return
			}
		}
	}()

	result := w.Executor.Execute(taskCtx, task)

	// Check if user cancelled BEFORE defer cancel() runs
	wasCancelled := taskCtx.Err() == context.Canceled && ctx.Err() == nil

	// Stop cancel listener
	cancelListener.Close(context.Background())
	<-cancelDone

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

	err = db.Retry(completeCtx, func() error {
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
