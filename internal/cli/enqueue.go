package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/spf13/cobra"
	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/source"
	"github.com/TolgaOk/nextask/internal/worker"
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
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		// Apply command-specific flag
		if remote != "" {
			cfg.Source.Remote = config.NormalizeRemote(remote)
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

		if attach {
			return enqueueAndAttach(ctx, pool, id)
		}

		if err := db.Notify(ctx, pool, db.ToWorkersChannel, db.WorkerWakeEvent{}); err != nil {
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
	enqueueCmd.Flags().BoolVarP(&attach, "attach", "a", false, "Watch task output and wait for completion")
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

	fmt.Printf("Task enqueued: %s\n", taskID)
	fmt.Println("Watching output (Ctrl+C to cancel)...")

	// Signal handler: Ctrl+C cancels the task
	cancelCtx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\nCancelling task...")

			originalStatus, err := db.RequestCancel(ctx, pool, taskID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to request cancel: %v\n", err)
				cancelFunc()
				return
			}

			if originalStatus != nil && *originalStatus == db.StatusPending {
				fmt.Println("Task cancelled")
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
				fmt.Printf("\nTask %s (exit %d)\n", status.Status, status.ExitCode)
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
		fmt.Printf("\nTask %s (exit %d)\n", task.Status, exitCode)
		return nil
	}
	if task.Status == db.StatusStale {
		fmt.Printf("\nTask %s (worker heartbeat expired)\n", task.Status)
		return nil
	}
	return fmt.Errorf("not done")
}

func printLogLine(log db.TaskLog) {
	if log.Stream == "nextask" {
		fmt.Printf("%s %s\n", hintStyle.Render("[nextask]"), log.Data)
	} else {
		fmt.Println(log.Data)
	}
}
