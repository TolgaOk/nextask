package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/source"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove TASK_ID",
	Short: "Remove a task, its logs, and snapshot",
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

		task, err := db.GetTask(ctx, pool, taskID)
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

		var snapshotDeleted bool
		if task.SourceType == "git" && len(task.SourceConfig) > 0 {
			var cfg struct {
				Remote string `json:"remote"`
				Ref    string `json:"ref"`
			}
			if err := json.Unmarshal(task.SourceConfig, &cfg); err == nil && cfg.Remote != "" && cfg.Ref != "" {
				if err := source.DeleteSnapshot(cfg.Remote, cfg.Ref); err == nil {
					snapshotDeleted = true
				}
			}
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

		if snapshotDeleted {
			fmt.Println("Task and snapshot removed")
		} else {
			fmt.Println("Task removed")
		}
		return nil
	},
}

func init() {
	RootCmd.AddCommand(removeCmd)
}
