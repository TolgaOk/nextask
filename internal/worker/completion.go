package worker

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/cenkalti/backoff/v5"
)

const completionShutdownTimeout = 30 * time.Second

// finishTask publishes a terminal result only after it has been stored. Until
// then the worker retains the result and cannot claim another task.
func (w *Worker) finishTask(ctx context.Context, task *db.Task, result *ExitResult, wasCancelled bool) error {
	status := db.StatusCompleted
	exitCode := result.Code
	message := fmt.Sprintf("[info] %s", result)
	if wasCancelled {
		status, exitCode = db.StatusCancelled, -1
		message = "[info] task cancelled"
		if result.Signal != nil {
			message += fmt.Sprintf(" (%s)", result.Signal)
		}
	} else if exitCode != 0 {
		status = db.StatusFailed
	}

	save := func(parent context.Context) error {
		return db.Retry(parent, func() error {
			return db.CompleteTask(parent, w.Pool, task.ID, status, exitCode)
		}, backoff.WithBackOff(w.newBackoff()), backoff.WithMaxElapsedTime(0),
			backoff.WithNotify(func(err error, delay time.Duration) {
				fmt.Fprintf(os.Stderr, "save task %s result: %s (retry in %v)\n", task.ID, db.HumanError(err), delay)
			}))
	}
	err := save(ctx)
	if err != nil && ctx.Err() != nil {
		// Stopping still allows the finished result to reach the database, but
		// an unavailable database must not prevent bounded worker shutdown.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), completionShutdownTimeout)
		err = save(shutdownCtx)
		cancel()
	}
	if err != nil {
		return fmt.Errorf("task %s exited with code %d but its result could not be saved: %w", task.ID, exitCode, err)
	}

	logCtx, logCancel := context.WithTimeout(context.Background(), 2*time.Second)
	log := NewDBLogger(w.Pool, task.ID)
	if result.Err != nil {
		log.Log(logCtx, "nextask", fmt.Sprintf("[error] %v", result.Err))
	}
	log.Log(logCtx, "nextask", message)
	logCancel()

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer notifyCancel()
	event := db.TaskStatusEvent{Status: string(status), ExitCode: exitCode}
	if err := db.Notify(notifyCtx, w.Pool, db.FromTaskChannel(task.ID), event); err != nil {
		fmt.Fprintf(os.Stderr, "failed to notify status: %v\n", err)
	}
	fmt.Printf("Task %s %s (exit %d)\n", task.ID, status, exitCode)
	return nil
}
