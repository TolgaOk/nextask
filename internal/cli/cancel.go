package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type cancelOptions struct {
	timeout time.Duration
}

func newCancelCommand(cfg *config.Config) *cobra.Command {
	var opts cancelOptions
	cmd := &cobra.Command{
		Use:   "cancel TASK_ID",
		Short: "Cancel a pending or running task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			if opts.timeout <= 0 {
				return errWithHints("timeout must be positive",
					"Example: "+codeStyle.Render("--timeout 10s"),
				)
			}

			ctx := cmd.Context()
			taskID := args[0]

			pool, err := db.Connect(ctx, cfg.DB.URL)
			if err != nil {
				return err
			}
			defer pool.Close()

			originalStatus, err := db.RequestCancel(ctx, pool, taskID)
			if err != nil {
				return err
			}

			if originalStatus == nil {
				return errWithHints(fmt.Sprintf("task not found: %s", taskID),
					"Run "+codeStyle.Render("nextask list")+" to see available tasks",
				)
			}

			switch *originalStatus {
			case db.StatusPending:
				fmt.Fprintln(os.Stderr, "Task cancelled")
				return nil

			case db.StatusRunning:
				timeout := opts.timeout
				if !cmd.Flags().Changed("timeout") {
					task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
					if err != nil {
						return err
					}
					if task != nil {
						timeout += time.Duration(task.CleanupTimeoutMS) * time.Millisecond
					}
				}
				return waitForCancel(ctx, *cfg, pool, taskID, timeout)

			default:
				return errWithHints(
					fmt.Sprintf("task already %s", *originalStatus),
					"Task has already finished and cannot be cancelled",
				)
			}
		},
	}

	cmd.Flags().DurationVar(&opts.timeout, "timeout", 10*time.Second, "Timeout waiting for cancel confirmation (default adds task cleanup time)")
	return cmd
}

func waitForCancel(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, taskID string, timeout time.Duration) error {
	sigCtx, stop := interruptContext(ctx)
	defer stop()
	waitCtx, cancel := context.WithTimeout(sigCtx, timeout)
	defer cancel()
	err := confirmCancellation(waitCtx, cfg, pool, taskID)
	if errors.Is(err, context.Canceled) && sigCtx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\nInterrupted - cancel request already sent")
		fmt.Fprintf(os.Stderr, "Check task status with %s\n", codeStyle.Render("nextask show "+taskID))
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) && waitCtx.Err() != nil {
		return errWithHints("cancel requested but worker did not confirm",
			"The task may still be stopping, finalizing, or disconnected",
			"Check task status with "+codeStyle.Render("nextask show "+taskID))
	}
	return err
}

func confirmCancellation(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, taskID string) error {
	watch, err := newStateWatcher(ctx, cfg, db.FromTaskChannel(taskID))
	if err != nil {
		return err
	}
	defer watch.Close()
	if err := db.Notify(ctx, pool, db.ToTaskChannel(taskID), db.TaskCancelEvent{}); err != nil {
		return err
	}
	return watch.Run(ctx, func(ctx context.Context) (bool, error) {
		task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
		if err != nil {
			return false, err
		}
		if task == nil {
			return false, fmt.Errorf("task not found: %s", taskID)
		}
		switch task.Status {
		case db.StatusCancelled:
			fmt.Fprintln(os.Stderr, "Task cancelled")
			return true, nil
		case db.StatusCompleted, db.StatusFailed:
			return false, fmt.Errorf("task %s finished as %s before cancellation was confirmed", taskID, task.Status)
		}
		return false, nil
	})
}
