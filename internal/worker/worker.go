// Package worker implements task execution with source fetching and log capture.
package worker

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/TolgaOk/nextask/internal/db"
)

// Worker processes tasks from the queue.
type Worker struct {
	ID         string
	Info       *db.WorkerInfo
	Pool       *pgxpool.Pool
	ListenConn *pgx.Conn
	CancelConn *pgx.Conn
	Executor   *Executor
	Once       bool
}

// Config contains worker configuration options.
type Config struct {
	DBURL   string
	Workdir string
	Name    string
	Once    bool
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

	cancelConn, err := pgx.Connect(ctx, cfg.DBURL)
	if err != nil {
		listenConn.Close(ctx)
		pool.Close()
		return nil, fmt.Errorf("failed to connect for cancel: %w", err)
	}

	// Setup listeners - cleanup all on failure
	cleanup := func() {
		cancelConn.Close(ctx)
		listenConn.Close(ctx)
		pool.Close()
	}

	if _, err := listenConn.Exec(ctx, "LISTEN new_task"); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to listen: %w", err)
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

	return &Worker{
		ID:         workerID,
		Info:       workerInfo,
		Pool:       pool,
		ListenConn: listenConn,
		CancelConn: cancelConn,
		Executor: &Executor{
			Pool:    pool,
			Workdir: cfg.Workdir,
		},
		Once: cfg.Once,
	}, nil
}

// Close releases database connections.
func (w *Worker) Close(ctx context.Context) {
	w.CancelConn.Close(ctx)
	w.ListenConn.Close(ctx)
	w.Pool.Close()
}

// Run starts the worker loop, processing tasks until context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
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

		if _, err := w.ListenConn.WaitForNotification(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
		}
	}
}

func (w *Worker) processTask(ctx context.Context, task *db.Task) {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Listen for cancel on task-specific channel
	cancelChannel := fmt.Sprintf("task_cancel_%s", task.ID)
	w.CancelConn.Exec(ctx, "LISTEN "+cancelChannel)
	defer w.CancelConn.Exec(ctx, "UNLISTEN "+cancelChannel)

	// Cancel listener goroutine for this task
	go func() {
		notif, err := w.CancelConn.WaitForNotification(taskCtx)
		if err != nil {
			return
		}
		if notif.Channel == cancelChannel {
			cancel()
		}
	}()

	result := w.Executor.Execute(taskCtx, task)

	wasCancelled := taskCtx.Err() == context.Canceled && ctx.Err() == nil

	log := NewDBLogger(ctx, w.Pool, task.ID)

	var status db.TaskStatus
	exitCode := result.Code
	if wasCancelled {
		status = db.StatusCancelled
		exitCode = -1
		if result.Signal != nil {
			log.Log("nextask", fmt.Sprintf("[info] task cancelled (%s)", result.Signal))
		} else {
			log.Log("nextask", "[info] task cancelled")
		}
	} else if exitCode != 0 {
		status = db.StatusFailed
		log.Log("nextask", fmt.Sprintf("[info] %s", result))
	} else {
		status = db.StatusCompleted
		log.Log("nextask", fmt.Sprintf("[info] %s", result))
	}

	if err := db.CompleteTask(ctx, w.Pool, task.ID, status, exitCode); err != nil {
		fmt.Fprintf(os.Stderr, "failed to complete task: %v\n", err)
	}

	if wasCancelled {
		w.Pool.Exec(ctx, fmt.Sprintf("NOTIFY task_cancelled_%s", task.ID))
	}

	fmt.Printf("Task %s %s (exit %d)\n", task.ID, status, exitCode)
}
