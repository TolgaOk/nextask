package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove TASK_ID",
	Short: "Remove a task and its logs",
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

		task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
		if err != nil {
			return err
		}
		if task == nil {
			return errWithHints(fmt.Sprintf("task not found: %s", taskID),
				"Run "+codeStyle.Render("nextask list")+" to see available tasks",
			)
		}

		if task.Status == db.StatusRunning {
			return errWithHints("cannot remove running task",
				"Cancel the task first with "+codeStyle.Render("nextask cancel "+taskID),
			)
		}

		deleted, err := db.DeleteTask(ctx, pool, taskID)
		if err != nil {
			return err
		}
		if !deleted {
			return errWithHints(fmt.Sprintf("failed to delete task: %s", taskID),
				"Task may have already been deleted",
			)
		}

		fmt.Fprintln(os.Stderr, "Task removed")
		return nil
	},
}

func init() {
	RootCmd.AddCommand(removeCmd)
}
