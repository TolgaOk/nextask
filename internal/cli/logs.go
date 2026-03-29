package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	logsStream string
	logsHead   int
	logsTail   int
	logsAttach bool
)

var (
	defaultLogStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	defaultLogPrefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	logsStreamStyles      = map[string]lipgloss.Style{
		"stdout":  lipgloss.NewStyle(),
		"stderr":  lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		"nextask": lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
	}
)

var logsCmd = &cobra.Command{
	Use:   "log TASK_ID",
	Short: "View task output",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer pool.Close()

		task, err := db.GetTask(ctx, pool, args[0], cfg.Worker.StaleDuration())
		if err != nil {
			return err
		}
		if task == nil {
			return errWithHints(fmt.Sprintf("task not found: %s", args[0]),
				"Run "+codeStyle.Render("nextask list")+" to see available tasks",
			)
		}

		if logsHead < 0 {
			return errWithHints("--head must be positive",
				"Example: "+codeStyle.Render("--head 50"),
			)
		}
		if logsTail < 0 {
			return errWithHints("--tail must be positive",
				"Example: "+codeStyle.Render("--tail 50"),
			)
		}

		if logsHead > 0 && logsTail > 0 {
			return errWithHints("cannot use both --head and --tail",
				"Use "+codeStyle.Render("--head N")+" for first N lines",
				"Use "+codeStyle.Render("--tail N")+" for last N lines",
			)
		}

		if logsAttach && logsHead > 0 {
			return errWithHints("cannot use --attach with --head",
				"Use "+codeStyle.Render("--tail N --attach")+" to show last N lines then stream",
			)
		}

		limit := logsHead
		tail := false
		if logsTail > 0 {
			limit = logsTail
			tail = true
		}

		logs, err := db.GetLogs(ctx, pool, args[0], logsStream, limit, tail)
		if err != nil {
			return err
		}

		var lastLogID int
		if len(logs) == 0 {
			if logsStream != "" {
				fmt.Fprintln(os.Stderr, hintStyle.Render(fmt.Sprintf("No logs with stream %q", logsStream)))
			} else if !logsAttach {
				fmt.Fprintln(os.Stderr, hintStyle.Render("No logs available"))
			}
		} else {
			for _, log := range logs {
				printLog(log)
				if log.ID > lastLogID {
					lastLogID = log.ID
				}
			}
		}

		if !logsAttach {
			return nil
		}

		// Only stream if task is still active
		if task.Status != db.StatusPending && task.Status != db.StatusRunning {
			return nil
		}

		return logsAndAttach(ctx, pool, task.ID, lastLogID)
	},
}

func printLog(log db.TaskLog) {
	style, ok := logsStreamStyles[log.Stream]
	if !ok {
		style = defaultLogStyle
	}

	prefix := ""
	if log.Stream == "stderr" {
		prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("[error]") + " "
	} else if log.Stream != "stdout" {
		prefix = defaultLogPrefixStyle.Bold(true).Render("["+log.Stream+"]") + " "
	}

	fmt.Println(prefix + style.Render(log.Data))
}

func init() {
	logsCmd.Flags().StringVarP(&logsStream, "stream", "s", "", "Filter by stream (stdout, stderr, nextask)")
	logsCmd.Flags().IntVar(&logsHead, "head", 0, "Show first N lines")
	logsCmd.Flags().IntVar(&logsTail, "tail", 0, "Show last N lines")
	logsCmd.Flags().BoolVarP(&logsAttach, "attach", "a", false, "Stream logs until task completes")
	RootCmd.AddCommand(logsCmd)
}

func logsAndAttach(ctx context.Context, pool *pgxpool.Pool, taskID string, lastLogID int) error {
	fromChannel := db.FromTaskChannel(taskID)
	backoff := db.NewBackOff(cfg.Retry.InitialInterval, cfg.Retry.MaxInterval)
	listener, err := db.Listen(ctx, cfg.DB.URL, backoff, fromChannel)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	defer listener.Close(context.Background())

	fmt.Fprintln(os.Stderr, hintStyle.Render("Streaming logs (Ctrl+C to stop watching)..."))

	cancelCtx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			cancelFunc()
		case <-cancelCtx.Done():
		}
	}()

	// Poll ticker for status check (handles missed events during reconnect)
	pollTicker := time.NewTicker(5 * time.Second)
	defer pollTicker.Stop()

	for {
		select {
		case notif, ok := <-listener.C:
			if !ok {
				// Listener closed - check final status
				return logsCheckCompletion(ctx, pool, taskID, &lastLogID)
			}

			eventType, data, err := db.ParseEvent(notif.Payload)
			if err != nil {
				continue
			}

			switch eventType {
			case db.EventTypeLog:
				logsFetchLogs(ctx, pool, taskID, &lastLogID)

			case db.EventTypeStatus:
				var status db.TaskStatusEvent
				if err := json.Unmarshal(data, &status); err != nil {
					continue
				}
				logsFetchLogs(ctx, pool, taskID, &lastLogID)
				fmt.Fprintf(os.Stderr, "\nTask %s (exit %d)\n", status.Status, status.ExitCode)
				return nil
			}

		case <-pollTicker.C:
			if err := logsCheckCompletion(ctx, pool, taskID, &lastLogID); err == nil {
				return nil
			}

		case <-cancelCtx.Done():
			fmt.Fprintln(os.Stderr) // newline after Ctrl+C
			return nil
		}
	}
}

func logsFetchLogs(ctx context.Context, pool *pgxpool.Pool, taskID string, lastLogID *int) {
	logs, err := db.GetLogsSince(ctx, pool, taskID, *lastLogID)
	if err != nil {
		return
	}
	for _, log := range logs {
		printLog(log)
		if log.ID > *lastLogID {
			*lastLogID = log.ID
		}
	}
}

func logsCheckCompletion(ctx context.Context, pool *pgxpool.Pool, taskID string, lastLogID *int) error {
	task, err := db.GetTask(ctx, pool, taskID, cfg.Worker.StaleDuration())
	if err != nil || task == nil {
		return fmt.Errorf("not done")
	}

	logsFetchLogs(ctx, pool, taskID, lastLogID)

	if task.Status == db.StatusCompleted || task.Status == db.StatusFailed || task.Status == db.StatusCancelled {
		exitCode := 0
		if task.ExitCode != nil {
			exitCode = *task.ExitCode
		}
		fmt.Fprintf(os.Stderr, "\nTask %s (exit %d)\n", task.Status, exitCode)
		return nil
	}
	if task.Status == db.StatusStale {
		fmt.Fprintf(os.Stderr, "\nTask %s (worker heartbeat expired)\n", task.Status)
		return nil
	}
	return fmt.Errorf("not done")
}
