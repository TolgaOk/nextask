// Package worker implements task execution with source fetching and log capture.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/TolgaOk/nextask/internal/db"
)

// CancelRequest is the JSON payload for task_cancel notifications.
type CancelRequest struct {
	TaskID string `json:"task_id"`
}

// TaskEvent is the JSON payload for task_event notifications.
type TaskEvent struct {
	TaskID string `json:"task_id"`
	Event  string `json:"event"`
}

type cancelReg struct {
	taskID string
	cancel context.CancelFunc
}

// Worker processes tasks from the queue.
type Worker struct {
	ID           string
	Info         *db.WorkerInfo
	Pool         *pgxpool.Pool
	ListenConn   *pgx.Conn
	CancelConn   *pgx.Conn
	Executor     *Executor
	Once         bool
	registerCh   chan cancelReg
	unregisterCh chan string
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

	cleanup := func() {
		cancelConn.Close(ctx)
		listenConn.Close(ctx)
		pool.Close()
	}

	if _, err := listenConn.Exec(ctx, "LISTEN new_task"); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to listen for new_task: %w", err)
	}

	if _, err := cancelConn.Exec(ctx, "LISTEN task_cancel"); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to listen for task_cancel: %w", err)
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
		ID:           workerID,
		Info:         workerInfo,
		Pool:         pool,
		ListenConn:   listenConn,
		CancelConn:   cancelConn,
		Executor:     &Executor{Pool: pool, Workdir: cfg.Workdir},
		Once:         cfg.Once,
		registerCh:   make(chan cancelReg, 64),
		unregisterCh: make(chan string, 64),
	}, nil
}

// Close releases database connections.
func (w *Worker) Close(ctx context.Context) {
	w.CancelConn.Close(ctx)
	w.ListenConn.Close(ctx)
	w.Pool.Close()
}

// runCancelDispatcher handles all task cancellation notifications.
func (w *Worker) runCancelDispatcher(ctx context.Context) error {
	cancelMap := make(map[string]context.CancelFunc)

	notifyCh := make(chan string, 128)
	bridgeErr := make(chan error, 1)

	go func() {
		defer close(notifyCh)
		for {
			notif, err := w.CancelConn.WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() == nil {
					bridgeErr <- fmt.Errorf("cancel conn died: %w", err)
				}
				return
			}
			select {
			case notifyCh <- notif.Payload:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case reg := <-w.registerCh:
			cancelMap[reg.taskID] = reg.cancel

		case taskID := <-w.unregisterCh:
			delete(cancelMap, taskID)

		case payload, ok := <-notifyCh:
			if !ok {
				select {
				case err := <-bridgeErr:
					return err
				default:
					return ctx.Err()
				}
			}
			var req CancelRequest
			if err := json.Unmarshal([]byte(payload), &req); err != nil {
				fmt.Fprintf(os.Stderr, "invalid cancel payload: %v\n", err)
				continue
			}
			if cancel, ok := cancelMap[req.TaskID]; ok {
				delete(cancelMap, req.TaskID)
				cancel()
			}

		case err := <-bridgeErr:
			return err

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Run starts the worker loop, processing tasks until context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	fmt.Printf("Worker %s started\n", w.ID)

	dispErr := make(chan error, 1)
	go func() { dispErr <- w.runCancelDispatcher(ctx) }()

	for {
		select {
		case err := <-dispErr:
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("cancel dispatcher stopped: %w", err)
			}
			return nil
		default:
		}

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
		case _, ok := <-waitForNotification(ctx, w.ListenConn):
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
			}
		case err := <-dispErr:
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("cancel dispatcher stopped: %w", err)
			}
			return nil
		case <-ctx.Done():
			return nil
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

func (w *Worker) processTask(ctx context.Context, task *db.Task) {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	select {
	case w.registerCh <- cancelReg{taskID: task.ID, cancel: cancel}:
	case <-ctx.Done():
		return
	}
	defer func() {
		select {
		case w.unregisterCh <- task.ID:
		case <-ctx.Done():
		}
	}()

	result := w.Executor.Execute(taskCtx, task)

	wasCancelled := taskCtx.Err() == context.Canceled && ctx.Err() == nil

	logCtx, logCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer logCancel()
	log := NewDBLogger(logCtx, w.Pool, task.ID)

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

	w.notifyTaskEvent(ctx, task.ID, string(status))

	fmt.Printf("Task %s %s (exit %d)\n", task.ID, status, exitCode)
}

func (w *Worker) notifyTaskEvent(ctx context.Context, taskID, event string) {
	payload, _ := json.Marshal(TaskEvent{TaskID: taskID, Event: event})
	if _, err := w.Pool.Exec(ctx, "SELECT pg_notify('task_event', $1)", string(payload)); err != nil {
		fmt.Fprintf(os.Stderr, "failed to notify task_event: %v\n", err)
	}
}
