package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/spf13/cobra"
)

var tags []string
var snapshot bool
var remote string
var attach bool

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
		if cmd.Flags().Changed("id") {
			id, _ := cmd.Flags().GetString("id")
			return db.ValidateTaskID(id)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		command := args[0]

		parsedTags, err := parseTags(tags)
		if err != nil {
			return err
		}

		id, _ := cmd.Flags().GetString("id")
		if !cmd.Flags().Changed("id") {
			id, err = gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 8)
			if err != nil {
				return fmt.Errorf("failed to generate ID: %w", err)
			}
		}

		task := &db.Task{
			ID:         id,
			Command:    command,
			Status:     db.StatusPending,
			Tags:       parsedTags,
			SourceType: "noop",
		}

		plan, err := enqueueIntegrations(cmd)
		if err != nil {
			return err
		}
		baseCtx := cmd.Context()
		ctx, stopSignals := interruptContext(baseCtx)
		defer stopSignals()
		pool, err := db.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer pool.Close()

		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		// Reserve the ID before preparation side effects. Workers only see the task
		// once its execution command is ready and this transaction commits.
		if err := db.CreateTask(ctx, tx, task); err != nil {
			return err
		}

		prepared, err := plan.Prepare(ctx, integrations.Task{ID: task.ID, Command: task.Command})
		if err != nil {
			return err
		}
		if prepared.Command != task.Command || prepared.CleanupTimeout != 0 {
			if _, err := tx.Exec(ctx, "UPDATE tasks SET execution_command = $2, cleanup_timeout_ms = $3, source_type = 'command' WHERE id = $1", task.ID, prepared.Command, prepared.CleanupTimeout.Milliseconds()); err != nil {
				return err
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return err
		}

		stopSignals()
		ctx = baseCtx
		if attach {
			return enqueueAndAttach(ctx, pool, id)
		}

		if err := db.Notify(ctx, pool, db.ToWorkersChannel, db.WorkerWakeEvent{}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: notify failed: %v\n", err)
		}

		fmt.Fprintf(os.Stderr, "Task enqueued: %s\n", id)
		return nil
	},
}

func init() {
	enqueueCmd.Flags().StringArray("with", nil, "Enable integration (repeatable)")
	enqueueCmd.Flags().StringArray("set", nil, "Override TOOL.KEY=VALUE (repeatable)")
	enqueueCmd.Flags().String("id", "", "Task ID (1–53 letters, digits, underscores or hyphens; starts with a letter or digit)")
	enqueueCmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags (key=value, can specify multiple)")
	enqueueCmd.Flags().BoolVar(&snapshot, "snapshot", false, "Create and push source snapshot")
	enqueueCmd.Flags().StringVar(&remote, "remote", "", "Git remote name or path for snapshot (required if --snapshot)")
	enqueueCmd.Flags().BoolVarP(&attach, "attach", "a", false, "Watch task output and wait for completion")
	enqueueCmd.Flags().MarkDeprecated("snapshot", "use --with git")
	enqueueCmd.Flags().MarkDeprecated("remote", "use --set git.remote=REMOTE")
	RootCmd.AddCommand(enqueueCmd)
}

func enqueueAndAttach(ctx context.Context, pool *pgxpool.Pool, taskID string) error {
	sigCtx, stop := interruptContext(ctx)
	defer stop()
	if err := db.Notify(sigCtx, pool, db.ToWorkersChannel, db.WorkerWakeEvent{}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: notify failed: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "Task enqueued: %s\n", taskID)
	fmt.Fprintln(os.Stderr, "Watching output (Ctrl+C to cancel)...")

	var lastLogID int
	task, err := streamTask(sigCtx, pool, taskID, &lastLogID, printLogLine)
	if errors.Is(err, context.Canceled) && ctx.Err() == nil {
		// The signal interrupts reads first. Persist cancellation using a fresh,
		// bounded context, then keep watching the worker's final outcome.
		stop()
		pending, cancelErr := cancelAttachedTask(ctx, pool, taskID)
		if cancelErr != nil || pending {
			return cancelErr
		}
		task, err = streamTask(ctx, pool, taskID, &lastLogID, printLogLine)
	}
	if err != nil {
		return err
	}
	printAttachedCompletion(task)
	return exitOrNil(taskExitCode(task))
}

func cancelAttachedTask(ctx context.Context, pool *pgxpool.Pool, taskID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fmt.Fprintln(os.Stderr, "\nCancelling task...")
	status, err := db.RequestCancel(ctx, pool, taskID)
	if err != nil {
		return false, fmt.Errorf("failed to request cancel: %w", err)
	}
	if status == nil {
		return false, fmt.Errorf("task not found: %s", taskID)
	}
	if *status == db.StatusPending {
		fmt.Fprintln(os.Stderr, "Task cancelled")
		return true, nil
	}
	if *status == db.StatusRunning {
		if err := db.Notify(ctx, pool, db.ToTaskChannel(taskID), db.TaskCancelEvent{}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cancel requested but notification failed: %v\n", err)
		}
	}
	return false, nil
}

func printLogLine(log db.TaskLog) {
	if log.Stream == "nextask" {
		fmt.Fprintf(os.Stderr, "%s %s\n", hintStyle.Render("[nextask]"), log.Data)
	} else {
		fmt.Println(log.Data)
	}
}

func enqueueIntegrations(cmd *cobra.Command) (*integrations.Plan, error) {
	with, _ := cmd.Flags().GetStringArray("with")
	overrides, _ := cmd.Flags().GetStringArray("set")
	if snapshot {
		with = append(with, "git")
	}
	values := make(map[string]map[string]any)
	for name, options := range cfg.Integrations {
		values[name] = make(map[string]any)
		for key, value := range options {
			values[name][key] = value
		}
	}
	if values["git"] == nil && cfg.Source.Remote != "" {
		values["git"] = map[string]any{"remote": cfg.Source.Remote}
	}
	if remote != "" {
		overrides = append([]string{"git.remote=" + config.NormalizeRemote(remote)}, overrides...)
	}
	return integrations.Builtins().Resolve(with, values, overrides)
}
