package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var cancelTimeout time.Duration

var cancelCmd = &cobra.Command{
	Use:   "cancel TASK_ID",
	Short: "Cancel a pending or running task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		if cancelTimeout <= 0 {
			return errWithHints("timeout must be positive",
				"Example: "+codeStyle.Render("--timeout 10s"),
			)
		}

		ctx := context.Background()
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
			timeout := cancelTimeout
			if !cmd.Flags().Changed("timeout") {
				task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
				if err != nil {
					return err
				}
				if task != nil {
					timeout += time.Duration(task.CleanupTimeoutMS) * time.Millisecond
				}
			}
			return waitForCancel(ctx, pool, taskID, timeout)

		default:
			return errWithHints(
				fmt.Sprintf("task already %s", *originalStatus),
				"Task has already finished and cannot be cancelled",
			)
		}
	},
}

func waitForCancel(ctx context.Context, pool *pgxpool.Pool, taskID string, timeout time.Duration) error {
	conn, err := pgx.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	fromChannel := db.FromTaskChannel(taskID)
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{fromChannel}.Sanitize()); err != nil {
		return err
	}

	toChannel := db.ToTaskChannel(taskID)
	if err := db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{}); err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	waitCtx, waitCancel := context.WithTimeout(sigCtx, timeout)
	defer waitCancel()

	for {
		// A worker may act on the durable request before this CLI starts
		// listening, or its confirmation notification may be lost.
		task, err := db.GetTask(waitCtx, pool, taskID, cfg.Worker.StaleDuration())
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		switch task.Status {
		case db.StatusCancelled:
			fmt.Fprintln(os.Stderr, "Task cancelled")
			return nil
		case db.StatusCompleted, db.StatusFailed:
			return fmt.Errorf("task %s finished as %s before cancellation was confirmed", taskID, task.Status)
		}

		pollCtx, pollCancel := context.WithTimeout(waitCtx, time.Second)
		_, err = conn.WaitForNotification(pollCtx)
		pollCancel()
		if err != nil {
			if sigCtx.Err() == context.Canceled {
				fmt.Fprintln(os.Stderr, "\nInterrupted - cancel request already sent")
				fmt.Fprintf(os.Stderr, "Check task status with %s\n", codeStyle.Render("nextask show "+taskID))
				return nil
			}
			if waitCtx.Err() == context.DeadlineExceeded {
				return errWithHints("cancel requested but worker did not confirm",
					"The task may still be stopping, finalizing, or disconnected",
					"Check task status with "+codeStyle.Render("nextask show "+taskID),
				)
			}
			if pollCtx.Err() == context.DeadlineExceeded {
				continue
			}
			return err
		}
	}
}

func init() {
	cancelCmd.Flags().DurationVar(&cancelTimeout, "timeout", 10*time.Second, "Timeout waiting for cancel confirmation (default adds task cleanup time)")
	RootCmd.AddCommand(cancelCmd)
}
