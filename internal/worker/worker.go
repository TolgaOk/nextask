package worker

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/nextask/nextask/internal/db"
)

type Worker struct {
	ID         string
	Info       *db.WorkerInfo
	Pool       *pgxpool.Pool
	ListenConn *pgx.Conn
	Executor   *Executor
	Once       bool
}

type Config struct {
	DBURL   string
	Workdir string
	Name    string
	Once    bool
}

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

	if _, err := listenConn.Exec(ctx, "LISTEN new_task"); err != nil {
		listenConn.Close(ctx)
		pool.Close()
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
		Executor: &Executor{
			Pool:    pool,
			Workdir: cfg.Workdir,
		},
		Once: cfg.Once,
	}, nil
}

func (w *Worker) Close(ctx context.Context) {
	w.ListenConn.Close(ctx)
	w.Pool.Close()
}

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

		// Block until notification (no polling)
		if _, err := w.ListenConn.WaitForNotification(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
		}
	}
}

func (w *Worker) processTask(ctx context.Context, task *db.Task) {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	exitCode := w.Executor.Execute(ctx, task)

	status := db.StatusCompleted
	if exitCode != 0 {
		status = db.StatusFailed
	}

	if err := db.CompleteTask(ctx, w.Pool, task.ID, status, exitCode); err != nil {
		fmt.Fprintf(os.Stderr, "failed to complete task: %v\n", err)
	}

	fmt.Printf("Task %s %s (exit %d)\n", task.ID, status, exitCode)
}
