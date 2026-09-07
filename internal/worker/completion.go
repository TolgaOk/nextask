package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/cenkalti/backoff/v5"
)

const completionShutdownTimeout = 30 * time.Second

// finishTask journals an outcome before cleanup or any database write.
func (w *Worker) finishTask(ctx context.Context, task *db.Task, execution taskExecution, wasCancelled bool) error {
	if task.WorkerID == nil || task.StartedAt == nil {
		return fmt.Errorf("task %s has no original claim for its completion journal", task.ID)
	}
	result := execution.result
	completion := db.TaskCompletion{
		TaskID: task.ID, WorkerID: *task.WorkerID,
		CreatedAt: task.CreatedAt.UTC(), StartedAt: task.StartedAt.UTC(),
		FinishedAt: time.Now().UTC().Truncate(time.Microsecond), Status: db.StatusCompleted, ExitCode: result.Code,
	}
	message := fmt.Sprintf("[info] %s", result)
	if wasCancelled {
		completion.Status, completion.ExitCode = db.StatusCancelled, -1
		message = "[info] task cancelled"
		if result.Signal != nil {
			message += fmt.Sprintf(" (%s)", result.Signal)
		}
	} else if result.Code != 0 {
		completion.Status = db.StatusFailed
	}
	completion, err := w.journal.save(completion)
	if err != nil {
		return fmt.Errorf("journal task %s result; task files preserved: %w", task.ID, err)
	}
	w.Executor.cleanup(execution.directory)
	confirmed, err := w.saveCompletion(ctx, completion)
	if err != nil {
		return err
	}
	if confirmed {
		logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		log := NewDBLogger(w.Pool, task.ID, w.stderr)
		if result.Err != nil {
			log.Log(logCtx, "nextask", fmt.Sprintf("[error] %v", result.Err))
		}
		log.Log(logCtx, "nextask", message)
		cancel()
		w.notifyCompletion(completion)
	}
	return w.acknowledgeCompletion(completion)
}

func (w *Worker) saveCompletion(ctx context.Context, c db.TaskCompletion) (bool, error) {
	save := func(parent context.Context) (bool, error) {
		return db.RetryValue(parent, func() (bool, error) {
			return db.CompleteClaim(parent, w.Pool, c)
		}, backoff.WithBackOff(w.newBackoff()), backoff.WithMaxElapsedTime(0),
			backoff.WithNotify(func(err error, delay time.Duration) {
				fmt.Fprintf(w.stderr, "save task %s result: %s (retry in %v)\n", c.TaskID, db.HumanError(err), delay)
			}))
	}
	confirmed, err := save(ctx)
	if err != nil && ctx.Err() != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), completionShutdownTimeout)
		confirmed, err = save(shutdownCtx)
		cancel()
	}
	if err != nil {
		return false, fmt.Errorf("task %s exited with code %d but its result could not be saved; journal retained at %s: %w",
			c.TaskID, c.ExitCode, filepath.Join(w.journal.dir, completionName(c)), err)
	}
	if !confirmed {
		fmt.Fprintf(w.stderr, "discarding obsolete result for task %s: original claim or outcome no longer matches\n", c.TaskID)
	}
	return confirmed, nil
}

func (w *Worker) notifyCompletion(c db.TaskCompletion) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	event := db.TaskStatusEvent{Status: string(c.Status), ExitCode: c.ExitCode}
	if err := db.Notify(ctx, w.Pool, db.FromTaskChannel(c.TaskID), event); err != nil {
		fmt.Fprintf(w.stderr, "failed to notify status: %v\n", err)
	}
	fmt.Fprintf(w.stdout, "Task %s %s (exit %d)\n", c.TaskID, c.Status, c.ExitCode)
}

func (w *Worker) acknowledgeCompletion(c db.TaskCompletion) error {
	if err := w.journal.acknowledge(c); err != nil {
		return fmt.Errorf("remove acknowledged completion journal for task %s: %w", c.TaskID, err)
	}
	return nil
}

func (w *Worker) recoverCompletions(ctx context.Context) error {
	names, err := w.journal.pending()
	if err != nil {
		return fmt.Errorf("read completion journal: %w", err)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		completion, err := w.journal.read(name)
		if errors.Is(err, os.ErrNotExist) {
			continue // Another worker already acknowledged it.
		}
		if err != nil {
			return fmt.Errorf("read completion journal %s: %w", filepath.Join(w.journal.dir, name), err)
		}
		confirmed, err := w.saveCompletion(ctx, completion)
		if err != nil {
			return err
		}
		if confirmed {
			fmt.Fprintf(w.stdout, "Recovered result for task %s\n", completion.TaskID)
			w.notifyCompletion(completion)
		}
		// Recovery updates results only. Old task directories may have been reused.
		if err := w.acknowledgeCompletion(completion); err != nil {
			return err
		}
	}
	return nil
}
