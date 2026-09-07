package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/spf13/cobra"
)

type enqueueOptions struct {
	tags     []string
	snapshot bool
	remote   string
	attach   bool
}

func newEnqueueCommand(cfg *config.Config) *cobra.Command {
	var opts enqueueOptions
	cmd := &cobra.Command{
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

			parsedTags, err := parseTags(opts.tags)
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

			plan, err := enqueueIntegrations(cmd, *cfg, opts)
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
				if err := db.SetTaskExecution(ctx, tx, task.ID, prepared.Command, prepared.CleanupTimeout); err != nil {
					return err
				}
			}

			if err := tx.Commit(ctx); err != nil {
				return err
			}

			stopSignals()
			ctx = baseCtx
			if opts.attach {
				return enqueueAndAttach(ctx, *cfg, outputFor(cmd), pool, id)
			}

			if err := db.Notify(ctx, pool, db.ToWorkersChannel, db.WorkerWakeEvent{}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: notify failed: %v\n", err)
			}

			_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Task enqueued: %s\n", id)
			return err
		},
	}

	cmd.Flags().StringArray("with", nil, "Enable integration (repeatable)")
	cmd.Flags().StringArray("set", nil, "Override TOOL.KEY=VALUE (repeatable)")
	cmd.Flags().String("id", "", "Task ID (1–53 letters, digits, underscores or hyphens; starts with a letter or digit)")
	cmd.Flags().StringSliceVar(&opts.tags, "tag", nil, "Tags (key=value, can specify multiple)")
	cmd.Flags().BoolVar(&opts.snapshot, "snapshot", false, "Create and push source snapshot")
	cmd.Flags().StringVar(&opts.remote, "remote", "", "Git remote name or path for snapshot (required if --snapshot)")
	cmd.Flags().BoolVarP(&opts.attach, "attach", "a", false, "Watch task output and wait for completion")
	cmd.Flags().MarkDeprecated("snapshot", "use --with git")
	cmd.Flags().MarkDeprecated("remote", "use --set git.remote=REMOTE")
	return cmd
}

func enqueueAndAttach(ctx context.Context, cfg config.Config, out commandOutput, pool *pgxpool.Pool, taskID string) error {
	sigCtx, stop := interruptContext(ctx)
	defer stop()
	if err := db.Notify(sigCtx, pool, db.ToWorkersChannel, db.WorkerWakeEvent{}); err != nil {
		fmt.Fprintf(out.err, "warning: notify failed: %v\n", err)
	}
	if _, err := fmt.Fprintf(out.err, "Task enqueued: %s\nWatching output (Ctrl+C to cancel)...\n", taskID); err != nil {
		return err
	}

	print := func(log db.TaskLog) error { return printLogLine(out, log) }
	var lastLogID int
	task, err := streamTask(sigCtx, cfg, out.err, pool, taskID, &lastLogID, print)
	if errors.Is(err, context.Canceled) && ctx.Err() == nil {
		// The signal interrupts reads first. Persist cancellation using a fresh,
		// bounded context, then keep watching the worker's final outcome.
		stop()
		pending, cancelErr := cancelAttachedTask(ctx, out.err, pool, taskID)
		if cancelErr != nil || pending {
			return cancelErr
		}
		task, err = streamTask(ctx, cfg, out.err, pool, taskID, &lastLogID, print)
	}
	if err != nil {
		return err
	}
	return errors.Join(exitOrNil(taskExitCode(task)), printAttachedCompletion(out.err, task))
}

func cancelAttachedTask(ctx context.Context, stderr io.Writer, pool *pgxpool.Pool, taskID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fmt.Fprintln(stderr, "\nCancelling task...")
	status, err := db.RequestCancel(ctx, pool, taskID)
	if err != nil {
		return false, fmt.Errorf("failed to request cancel: %w", err)
	}
	if status == nil {
		return false, fmt.Errorf("task not found: %s", taskID)
	}
	if *status == db.StatusPending {
		_, err := fmt.Fprintln(stderr, "Task cancelled")
		return true, err
	}
	if *status == db.StatusRunning {
		if err := db.Notify(ctx, pool, db.ToTaskChannel(taskID), db.TaskCancelEvent{}); err != nil {
			fmt.Fprintf(stderr, "warning: cancel requested but notification failed: %v\n", err)
		}
	}
	return false, nil
}

func printLogLine(out commandOutput, log db.TaskLog) error {
	if log.Stream == "nextask" {
		_, err := fmt.Fprintf(out.err, "%s %s\n", hintStyle.Render("[nextask]"), log.Data)
		return err
	}
	_, err := fmt.Fprintln(out.out, log.Data)
	return err
}

func enqueueIntegrations(cmd *cobra.Command, cfg config.Config, opts enqueueOptions) (*integrations.Plan, error) {
	with, _ := cmd.Flags().GetStringArray("with")
	overrides, _ := cmd.Flags().GetStringArray("set")
	if opts.snapshot {
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
	if opts.remote != "" {
		overrides = append([]string{"git.remote=" + config.NormalizeRemote(opts.remote)}, overrides...)
	}
	return integrations.Builtins().Resolve(with, values, overrides)
}
