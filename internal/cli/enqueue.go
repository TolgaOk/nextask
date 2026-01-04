package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/nextask/nextask/internal/db"
	"github.com/nextask/nextask/internal/source"
	"github.com/nextask/nextask/internal/worker"
	"github.com/spf13/cobra"
)

var tags []string
var snapshot bool
var remote string

var enqueueCmd = &cobra.Command{
	Use:   "enqueue COMMAND",
	Short: "Add a task to the queue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dbURL == "" {
			return fmt.Errorf("--db-url is required")
		}

		command := args[0]

		// Parse tags
		parsedTags := make(map[string]string)
		for _, tag := range tags {
			parts := strings.SplitN(tag, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid tag format: %s (expected key=value)", tag)
			}
			parsedTags[parts[0]] = parts[1]
		}

		// Generate short ID
		id, err := gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 8)
		if err != nil {
			return fmt.Errorf("failed to generate ID: %w", err)
		}

		// Validate snapshot flags
		if snapshot && remote == "" {
			return fmt.Errorf("--remote is required when using --snapshot")
		}

		task := &db.Task{
			ID:         id,
			Command:    command,
			Status:     db.StatusPending,
			Tags:       parsedTags,
			SourceType: "noop",
			InitType:   "noop",
		}

		// Create and push source snapshot if requested
		if snapshot {
			result, err := source.CreateSnapshot(".", id)
			if err != nil {
				cmd.SilenceUsage = true
				return fmt.Errorf("failed to create snapshot: %w", err)
			}

			if err := source.PushSnapshot(".", remote, result); err != nil {
				cmd.SilenceUsage = true
				return fmt.Errorf("failed to push snapshot: %w", err)
			}

			task.SourceType = "git"
			task.SourceConfig, _ = json.Marshal(worker.GitSourceConfig{
				Remote: remote,
				Ref:    result.Ref,
				Commit: result.Commit,
			})
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx, dbURL)
		if err != nil {
			cmd.SilenceUsage = true
			return err
		}
		defer pool.Close()

		if err := db.CreateTask(ctx, pool, task); err != nil {
			cmd.SilenceUsage = true
			return fmt.Errorf("failed to enqueue task: %w", err)
		}

		// Notify waiting workers (non-fatal if fails)
		if _, err := pool.Exec(ctx, "NOTIFY new_task"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: notify failed: %v\n", err)
		}

		fmt.Printf("Task enqueued: %s\n", id)
		return nil
	},
}

func init() {
	enqueueCmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags (key=value, can specify multiple)")
	enqueueCmd.Flags().BoolVar(&snapshot, "snapshot", false, "Create and push source snapshot")
	enqueueCmd.Flags().StringVar(&remote, "remote", "", "Git remote name or path for snapshot (required if --snapshot)")
	rootCmd.AddCommand(enqueueCmd)
}
