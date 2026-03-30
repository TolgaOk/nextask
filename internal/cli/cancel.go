package cli

import (
	"context"
	"encoding/json"
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

	fromChannel := db.FromTaskChannel(taskID)
	if _, err := conn.Exec(ctx, "LISTEN "+fromChannel); err != nil {
		return err
	}

	toChannel := db.ToTaskChannel(taskID)
	if err := db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{}); err != nil {
		return err
	}

	// Create contexts: sigCtx for signal, waitCtx with timeout
	sigCtx, sigCancel := context.WithCancelCause(ctx)
	waitCtx, waitCancel := context.WithTimeout(sigCtx, cancelTimeout)
	defer sigCancel(nil)

	// Signal handler for graceful interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer signal.Stop(sigCh)
		select {
		case <-sigCh:
			sigCancel(context.Canceled)
		case <-waitCtx.Done():
		}
	}()

	// Ensure goroutine exits before function returns
	defer func() {
		waitCancel() // Unblock goroutine if waiting on waitCtx.Done()
		<-done       // Wait for goroutine to cleanup
	}()

	for {
		notif, err := conn.WaitForNotification(waitCtx)
		if err != nil {
			cause := context.Cause(sigCtx)
			if cause == context.Canceled {
				fmt.Fprintln(os.Stderr, "\nInterrupted - cancel request already sent")
				fmt.Fprintf(os.Stderr, "Check task status with %s\n", codeStyle.Render("nextask show "+taskID))
				return nil
			}
			if waitCtx.Err() == context.DeadlineExceeded {
				return errWithHints("cancel requested but worker did not confirm",
					"Worker may be unresponsive or disconnected",
					"Check task status with "+codeStyle.Render("nextask show "+taskID),
				)
			}
			return err
		}

		eventType, data, err := db.ParseEvent(notif.Payload)
		if err != nil {
			return fmt.Errorf("failed to parse event: %w", err)
		}
		if eventType == db.EventTypeStatus {
			var status db.TaskStatusEvent
			if err := json.Unmarshal(data, &status); err != nil {
				return fmt.Errorf("failed to parse status event: %w", err)
			}
			if status.Status == string(db.StatusCancelled) {
				fmt.Fprintln(os.Stderr, "Task cancelled")
				return nil
			}
		}
	}
}

func init() {
	cancelCmd.Flags().DurationVar(&cancelTimeout, "timeout", 10*time.Second, "Timeout waiting for cancel confirmation")
	RootCmd.AddCommand(cancelCmd)
}
