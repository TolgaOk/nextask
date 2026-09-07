package cli

import (
	"fmt"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

func newRemoveCommand(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove TASK_ID",
		Short: "Remove a task and its logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			ctx := cmd.Context()
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

			_, err = fmt.Fprintln(cmd.ErrOrStderr(), "Task removed")
			return err
		},
	}

	return cmd
}
