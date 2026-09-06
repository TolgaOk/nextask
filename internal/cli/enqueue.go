package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
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
		ctx, stopSignals := signal.NotifyContext(baseCtx, os.Interrupt, syscall.SIGTERM)
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
		if prepared.Command != task.Command {
			if _, err := tx.Exec(ctx, "UPDATE tasks SET execution_command = $2, source_type = 'command' WHERE id = $1", task.ID, prepared.Command); err != nil {
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
	// Create listener with auto-reconnect before notifying workers
	fromChannel := db.FromTaskChannel(taskID)
	backoff := db.NewBackOff(cfg.Retry.InitialInterval, cfg.Retry.MaxInterval)
	listener, err := db.Listen(ctx, cfg.DB.URL, backoff, fromChannel)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	defer listener.Close(context.Background())

	// Notify workers
	if err := db.Notify(ctx, pool, db.ToWorkersChannel, db.WorkerWakeEvent{}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: notify failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "Task enqueued: %s\n", taskID)
	fmt.Fprintf(os.Stderr, "Watching output (Ctrl+C to cancel)...\n")

	// Signal handler: Ctrl+C cancels the task
	cancelCtx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\nCancelling task...\n")

			originalStatus, err := db.RequestCancel(ctx, pool, taskID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to request cancel: %v\n", err)
				cancelFunc()
				return
			}

			if originalStatus != nil && *originalStatus == db.StatusPending {
				fmt.Fprintf(os.Stderr, "Task cancelled\n")
				cancelFunc()
				return
			}

			// Running task - notify worker
			toChannel := db.ToTaskChannel(taskID)
			if err := db.Notify(ctx, pool, toChannel, db.TaskCancelEvent{}); err != nil {
				fmt.Fprintf(os.Stderr, "failed to send cancel: %v\n", err)
			}
		case <-cancelCtx.Done():
			signal.Stop(sigCh)
		}
	}()

	// Poll ticker for status check (handles missed events during reconnect)
	pollTicker := time.NewTicker(5 * time.Second)
	defer pollTicker.Stop()

	var lastLogID int
	for {
		select {
		case notif, ok := <-listener.C:
			if !ok {
				// Listener closed - check final status
				return enqueueCheckCompletion(ctx, pool, taskID, &lastLogID)
			}

			eventType, data, err := db.ParseEvent(notif.Payload)
			if err != nil {
				continue
			}

			switch eventType {
			case db.EventTypeLog:
				enqueueFetchLogs(ctx, pool, taskID, &lastLogID)

			case db.EventTypeStatus:
				var status db.TaskStatusEvent
				if err := json.Unmarshal(data, &status); err != nil {
					continue
				}
				enqueueFetchLogs(ctx, pool, taskID, &lastLogID)
				fmt.Fprintf(os.Stderr, "\nTask %s (exit %d)\n", status.Status, status.ExitCode)
				if status.ExitCode != 0 {
					return &exitCodeError{code: status.ExitCode}
				}
				return nil
			}

		case <-pollTicker.C:
			if err := enqueueCheckCompletion(ctx, pool, taskID, &lastLogID); err == nil {
				return nil
			}

		case <-cancelCtx.Done():
			return nil
		}
	}
}

func enqueueFetchLogs(ctx context.Context, pool *pgxpool.Pool, taskID string, lastLogID *int) {
	logs, err := db.GetLogsSince(ctx, pool, taskID, *lastLogID)
	if err != nil {
		return
	}
	for _, log := range logs {
		printLogLine(log)
		if log.ID > *lastLogID {
			*lastLogID = log.ID
		}
	}
}

func enqueueCheckCompletion(ctx context.Context, pool *pgxpool.Pool, taskID string, lastLogID *int) error {
	task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
	if err != nil || task == nil {
		return fmt.Errorf("not done")
	}

	enqueueFetchLogs(ctx, pool, taskID, lastLogID)

	if task.Status == db.StatusCompleted || task.Status == db.StatusFailed || task.Status == db.StatusCancelled {
		exitCode := 0
		if task.ExitCode != nil {
			exitCode = *task.ExitCode
		}
		fmt.Fprintf(os.Stderr, "\nTask %s (exit %d)\n", task.Status, exitCode)
		if exitCode != 0 {
			return &exitCodeError{code: exitCode}
		}
		return nil
	}
	if task.Status == db.StatusStale {
		fmt.Fprintf(os.Stderr, "\nTask %s (worker heartbeat expired)\n", task.Status)
		return &exitCodeError{code: 1}
	}
	return fmt.Errorf("not done")
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
	values := make(map[string]map[string]string)
	for name, options := range cfg.Integrations {
		values[name] = make(map[string]string)
		for key, value := range options {
			values[name][key] = value
		}
	}
	if values["git"] == nil && cfg.Source.Remote != "" {
		values["git"] = map[string]string{"remote": cfg.Source.Remote}
	}
	if remote != "" {
		overrides = append([]string{"git.remote=" + config.NormalizeRemote(remote)}, overrides...)
	}
	return integrations.Builtins().Resolve(with, values, overrides)
}
