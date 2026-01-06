package cli

import (
	"context"
	"fmt"
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
			fmt.Println("Task cancelled")
			return nil

		case db.StatusRunning:
			return waitForCancel(ctx, pool, taskID)

		default:
			return errWithHints(
				fmt.Sprintf("task already %s", *originalStatus),
				"Task has already finished and cannot be cancelled",
			)
		}
	},
}

func waitForCancel(ctx context.Context, pool *pgxpool.Pool, taskID string) error {
	conn, err := pgx.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	channel := fmt.Sprintf("task_cancelled_%s", taskID)
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, fmt.Sprintf("NOTIFY task_cancel_%s", taskID)); err != nil {
		return err
	}

	waitCtx, cancel := context.WithTimeout(ctx, cancelTimeout)
	defer cancel()

	_, err = conn.WaitForNotification(waitCtx)
	if err != nil {
		if waitCtx.Err() == context.DeadlineExceeded {
			return errWithHints("cancel requested but worker did not confirm",
				"Worker may be unresponsive or disconnected",
				"Check task status with "+codeStyle.Render("nextask show "+taskID),
			)
		}
		return err
	}

	fmt.Println("Task cancelled")
	return nil
}

func init() {
	cancelCmd.Flags().DurationVar(&cancelTimeout, "timeout", 10*time.Second, "Timeout waiting for cancel confirmation")
	RootCmd.AddCommand(cancelCmd)
}
