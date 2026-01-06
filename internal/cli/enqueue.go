package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/source"
	"github.com/TolgaOk/nextask/internal/worker"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/spf13/cobra"
)

var tags []string
var snapshot bool
var remote string

var enqueueCmd = &cobra.Command{
	Use:   "enqueue COMMAND",
	Short: "Add a task to the queue",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return errWithHints("command is required",
				"Example: "+codeStyle.Render("nextask enqueue \"python train.py\""),
			)
		}
		if len(args) > 1 {
			return errWithHints("too many arguments",
				"Wrap command in quotes: "+codeStyle.Render("nextask enqueue \"python train.py --epochs 10\""),
			)
		}
		if args[0] == "" {
			return errWithHints("command cannot be empty",
				"Example: "+codeStyle.Render("nextask enqueue \"python train.py\""),
			)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		// Apply command-specific flag
		if remote != "" {
			cfg.Source.Remote = config.ToAbsPath(remote)
		}

		command := args[0]

		parsedTags := make(map[string]string)
		for _, tag := range tags {
			parts := strings.SplitN(tag, "=", 2)
			if len(parts) != 2 {
				return errWithHints(fmt.Sprintf("invalid tag format: %s", tag),
					"Expected format: "+codeStyle.Render("key=value"),
				)
			}
			parsedTags[parts[0]] = parts[1]
		}

		id, err := gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 8)
		if err != nil {
			return fmt.Errorf("failed to generate ID: %w", err)
		}

		if snapshot && cfg.Source.Remote == "" {
			return errWithHints("remote is required when using --snapshot",
				"Provide: "+codeStyle.Render("--remote ~/.nextask/source.git"),
				"Or set "+codeStyle.Render("source.remote")+" in config file",
				"Create with: "+codeStyle.Render("nextask init source"),
			)
		}

		task := &db.Task{
			ID:         id,
			Command:    command,
			Status:     db.StatusPending,
			Tags:       parsedTags,
			SourceType: "noop",
			InitType:   "noop",
		}

		if snapshot {
			result, err := source.CreateSnapshot(".", id)
			if err != nil {
				return withHints(fmt.Errorf("failed to create snapshot: %w", err),
					"Ensure you are in a git repository",
				)
			}

			if err := source.PushSnapshot(".", cfg.Source.Remote, result); err != nil {
				return withHints(fmt.Errorf("failed to push snapshot: %w", err),
					"Check that remote exists: "+codeStyle.Render(cfg.Source.Remote),
				)
			}

			task.SourceType = "git"
			task.SourceConfig, _ = json.Marshal(worker.GitSourceConfig{
				Remote: cfg.Source.Remote,
				Ref:    result.Ref,
				Commit: result.Commit,
			})
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer pool.Close()

		if err := db.CreateTask(ctx, pool, task); err != nil {
			return err
		}

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
	RootCmd.AddCommand(enqueueCmd)
}
