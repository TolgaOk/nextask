package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// streamTask drains stored logs and returns the stored terminal task. The caller
// chooses how interrupts and the task's exit code affect its command.
func streamTask(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, taskID string, lastLogID *int, print func(db.TaskLog)) (*db.Task, error) {
	watch, err := newStateWatcher(ctx, cfg, db.FromTaskChannel(taskID))
	if err != nil {
		return nil, err
	}
	defer watch.Close()
	var finished *db.Task
	err = watch.Run(ctx, func(ctx context.Context) (bool, error) {
		task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
		if err != nil {
			return false, err
		}
		if task == nil {
			return false, fmt.Errorf("task not found: %s", taskID)
		}
		logs, err := db.GetLogsSince(ctx, pool, taskID, *lastLogID)
		if err != nil {
			return false, err
		}
		for _, log := range logs {
			print(log)
			*lastLogID = log.ID
		}
		if isTerminal(task.Status) {
			finished = task
			return true, nil
		}
		return false, nil
	})
	return finished, err
}

func printAttachedCompletion(task *db.Task) {
	if task.Status == db.StatusStale {
		fmt.Fprintln(os.Stderr, "\nTask stale (worker heartbeat expired)")
		return
	}
	fmt.Fprintf(os.Stderr, "\nTask %s (exit %d)\n", task.Status, taskExitCode(task))
}
