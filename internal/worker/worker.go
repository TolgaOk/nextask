// Package worker implements task execution with source fetching and log capture.
package worker

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/TolgaOk/nextask/internal/db"
)

// Worker processes tasks from the queue.
type Worker struct {
	ID                string
	Info              *db.WorkerInfo
	Pool              *pgxpool.Pool
	ListenConn        *pgx.Conn
	Executor          *Executor
	Once              bool
	dbURL             string
	workdir           string
	heartbeatInterval time.Duration
}

// Config contains worker configuration options.
type Config struct {
	DBURL             string
	Workdir           string
	Name              string
	Once              bool
	HeartbeatInterval time.Duration
}

// New creates a worker with the given configuration.
func New(ctx context.Context, cfg Config) (*Worker, error) {
	pool, err := db.Connect(ctx, cfg.DBURL)
	if err != nil {
		return nil, err
	}

	listenConn, err := pgx.Connect(ctx, cfg.DBURL)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to connect for listen: %w", err)
	}

	workerID := cfg.Name
	if workerID == "" {
		workerID, _ = gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 8)
	}

	if _, err := listenConn.Exec(ctx, "LISTEN new_task"); err != nil {
		listenConn.Close(ctx)
		pool.Close()
		return nil, fmt.Errorf("failed to listen for new_task: %w", err)
	}

	hostname, _ := os.Hostname()
	workerInfo := &db.WorkerInfo{
		Hostname: hostname,
		OS:       runtime.GOOS,
		PID:      os.Getpid(),
	}

	return &Worker{
		ID:                workerID,
		Info:              workerInfo,
		Pool:              pool,
		ListenConn:        listenConn,
		Executor:          &Executor{Pool: pool, Workdir: cfg.Workdir},
		Once:              cfg.Once,
		dbURL:             cfg.DBURL,
		workdir:           cfg.Workdir,
		heartbeatInterval: cfg.HeartbeatInterval,
	}, nil
}

// Close releases database connections.
func (w *Worker) Close(ctx context.Context) {
	w.ListenConn.Close(ctx)
	w.Pool.Close()
}

// Run starts the worker loop, processing tasks until context is cancelled.
func (w *Worker) Run(parentCtx context.Context) error {
	// Create internal cancellable context for stop signal handling
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	hostname, _ := os.Hostname()

	// Register worker in DB
	if err := db.RegisterWorker(ctx, w.Pool, w.ID, os.Getpid(), hostname, w.workdir); err != nil {
		return fmt.Errorf("failed to register worker: %w", err)
	}
	defer func() {
		// Use background context for cleanup
		unregCtx, unregCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unregCancel()

		// Notify that worker is stopping (for CLI waiting on stop confirmation)
		fromWorkerCh := db.FromWorkerChannel(w.ID)
		w.Pool.Exec(unregCtx, "SELECT pg_notify($1, 'stopped')", fromWorkerCh)

		db.UnregisterWorker(unregCtx, w.Pool, w.ID)
	}()

	// Start stop listener goroutine
	stopConn, err := pgx.Connect(ctx, w.dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect for stop listener: %w", err)
	}
	toWorkerCh := db.ToWorkerChannel(w.ID)
	if _, err := stopConn.Exec(ctx, `LISTEN "`+toWorkerCh+`"`); err != nil {
		stopConn.Close(context.Background())
		return fmt.Errorf("failed to listen for stop: %w", err)
	}
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		w.listenForStop(ctx, stopConn, cancel)
	}()
	defer func() {
		stopConn.Close(context.Background())
		<-stopDone
	}()

	// Start heartbeat goroutine
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		w.runHeartbeat(ctx)
	}()
	defer func() { <-heartbeatDone }()

	fmt.Printf("Worker %s started\n", w.ID)

	for {
		if ctx.Err() != nil {
			return nil
		}

		task, err := db.ClaimTask(ctx, w.Pool, w.ID, w.Info)
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
			fmt.Println("No pending tasks")
			return nil
		}

		select {
		case <-waitForNotification(ctx, w.ListenConn):
			// new_task notification received, loop to claim
		case <-ctx.Done():
			return nil
		}
	}
}

// runHeartbeat periodically updates the worker's heartbeat timestamp.
// Satisfies invariants: A (ctx.Done), B (context timeout), C (defer ticker.Stop).
func (w *Worker) runHeartbeat(ctx context.Context) {
	if w.heartbeatInterval <= 0 {
		return
	}

	ticker := time.NewTicker(w.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := db.UpdateHeartbeat(hbCtx, w.Pool, w.ID); err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(os.Stderr, "heartbeat failed: %v\n", err)
				}
			}
			cancel()
		case <-ctx.Done():
			return
		}
	}
}

func waitForNotification(ctx context.Context, conn *pgx.Conn) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		_, err := conn.WaitForNotification(ctx)
		if err == nil {
			ch <- struct{}{}
		}
		close(ch)
	}()
	return ch
}

// listenForStop listens for worker stop signals and cancels the context when received.
// Satisfies invariants: A (ctx.Done via conn.Close), B (WaitForNotification accepts ctx).
func (w *Worker) listenForStop(ctx context.Context, conn *pgx.Conn, cancel context.CancelFunc) {
	_, err := conn.WaitForNotification(ctx)
	if err != nil {
		return // context cancelled or connection closed
	}
	fmt.Println("Received stop signal, shutting down...")
	cancel()
}

func (w *Worker) processTask(ctx context.Context, task *db.Task) {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cancelConn, err := pgx.Connect(ctx, w.dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect for cancel listener: %v\n", err)
		return
	}

	toChannel := db.ToTaskChannel(task.ID)
	if _, err := cancelConn.Exec(ctx, "LISTEN "+toChannel); err != nil {
		cancelConn.Close(context.Background())
		fmt.Fprintf(os.Stderr, "failed to listen on %s: %v\n", toChannel, err)
		return
	}

	cancelDone := make(chan bool, 1)
	go func() {
		w.listenForCancel(taskCtx, cancelConn, cancel)
		cancelDone <- true
	}()

	result := w.Executor.Execute(taskCtx, task)

	// Check if user cancelled BEFORE defer cancel() runs
	wasCancelled := taskCtx.Err() == context.Canceled && ctx.Err() == nil

	// Stop listener goroutine by closing connection (unblocks WaitForNotification)
	cancelConn.Close(context.Background())
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

	// Use background context for DB cleanup - ensures task is marked complete even if worker is stopping
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer completeCancel()

	if err := db.CompleteTask(completeCtx, w.Pool, task.ID, status, exitCode); err != nil {
		fmt.Fprintf(os.Stderr, "failed to complete task: %v\n", err)
	}

	fromChannel := db.FromTaskChannel(task.ID)
	event := db.TaskStatusEvent{Status: string(status), ExitCode: exitCode}
	if err := db.Notify(completeCtx, w.Pool, fromChannel, event); err != nil {
		fmt.Fprintf(os.Stderr, "failed to notify status: %v\n", err)
	}

	fmt.Printf("Task %s %s (exit %d)\n", task.ID, status, exitCode)
}

func (w *Worker) listenForCancel(ctx context.Context, conn *pgx.Conn, cancel context.CancelFunc) {
	for {
		notif, err := conn.WaitForNotification(ctx)
		if err != nil {
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
	}
}
