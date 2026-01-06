package cli

import (
	"context"
	"encoding/json"
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

	if _, err := conn.Exec(ctx, "LISTEN task_event"); err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]string{"task_id": taskID})
	if _, err := pool.Exec(ctx, "SELECT pg_notify('task_cancel', $1)", string(payload)); err != nil {
		return err
	}

	waitCtx, cancel := context.WithTimeout(ctx, cancelTimeout)
	defer cancel()

	for {
		notif, err := conn.WaitForNotification(waitCtx)
		if err != nil {
			if waitCtx.Err() == context.DeadlineExceeded {
				return errWithHints("cancel requested but worker did not confirm",
					"Worker may be unresponsive or disconnected",
					"Check task status with "+codeStyle.Render("nextask show "+taskID),
				)
			}
			return err
		}

		var event struct {
			TaskID string `json:"task_id"`
			Event  string `json:"event"`
		}
		if err := json.Unmarshal([]byte(notif.Payload), &event); err != nil {
			continue
		}
		if event.TaskID == taskID && event.Event == "cancelled" {
			fmt.Println("Task cancelled")
			return nil
		}
	}
}

func init() {
	cancelCmd.Flags().DurationVar(&cancelTimeout, "timeout", 10*time.Second, "Timeout waiting for cancel confirmation")
	RootCmd.AddCommand(cancelCmd)
}
