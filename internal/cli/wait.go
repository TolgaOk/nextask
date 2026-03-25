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

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var (
	waitTags    []string
	waitTimeout time.Duration
	waitAny     bool
)

var waitCmd = &cobra.Command{
	Use:   "wait TASK_ID [TASK_ID...]",
	Short: "Block until tasks complete",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(waitTags) > 0 && len(args) > 0 {
			return errWithHints("cannot use both task IDs and --tag",
				"Use either: "+codeStyle.Render("nextask wait <id1> <id2>"),
				"Or:         "+codeStyle.Render("nextask wait --tag key=value"),
			)
		}
		if len(waitTags) == 0 && len(args) == 0 {
			return errWithHints("task ID or --tag is required",
				"Example: "+codeStyle.Render("nextask wait <id>"),
				"Or:      "+codeStyle.Render("nextask wait --tag key=value"),
			)
		}
		return nil
	},
	RunE: runWait,
}

func init() {
	waitCmd.Flags().StringSliceVar(&waitTags, "tag", nil, "Wait for all tasks matching tag (key=value)")
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 0, "Exit 124 if tasks not done within duration")
	waitCmd.Flags().BoolVar(&waitAny, "any", false, "Return when any task completes (not all)")
	RootCmd.AddCommand(waitCmd)
}

func runWait(cmd *cobra.Command, args []string) error {
	if cfg.DB.URL == "" {
		return errDBRequired()
	}

	ctx := context.Background()
	if waitTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, waitTimeout)
		defer cancel()
	}
	ctx = withSignalCancel(ctx)

	pool, err := db.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return err
	}
	defer pool.Close()

	taskIDs, err := resolveWaitTargets(ctx, pool, args)
	if err != nil {
		return err
	}

	// Dedicated connection for LISTEN (separate from pool)
	conn, err := pgx.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close(context.Background())

	remaining := make(map[string]bool, len(taskIDs))

	// LISTEN first, then check — avoids race where task finishes between check and listen
	for _, id := range taskIDs {
		if _, err := conn.Exec(ctx, "LISTEN "+db.FromTaskChannel(id)); err != nil {
			return fmt.Errorf("listen failed: %w", err)
		}
		remaining[id] = true
	}

	// Remove already-finished tasks
	var failCode int
	for _, id := range taskIDs {
		task, err := db.GetTask(ctx, pool, id, cfg.Worker.StaleDuration())
		if err != nil {
			return err
		}
		if task == nil {
			delete(remaining, id)
			fmt.Fprintf(os.Stderr, "task %s not found\n", id)
			failCode = firstNonZero(failCode, 1)
			continue
		}
		if isTerminal(task.Status) {
			delete(remaining, id)
			code := taskExitCode(task)
			printWaitLine(task.ID, task.Status, code)
			failCode = firstNonZero(failCode, code)
			if waitAny {
				return exitOrNil(code)
			}
		}
	}

	// Block until all remaining tasks complete
	for len(remaining) > 0 {
		notif, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return handleTimeout(remaining)
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("connection lost: %w", err)
		}

		id, code, ok := parseCompletionNotify(notif.Channel, notif.Payload)
		if !ok || !remaining[id] {
			continue
		}
		delete(remaining, id)
		failCode = firstNonZero(failCode, code)
		if waitAny {
			return exitOrNil(code)
		}
	}

	return exitOrNil(failCode)
}

func resolveWaitTargets(ctx context.Context, pool *pgxpool.Pool, args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}

	parsedTags, err := parseTags(waitTags)
	if err != nil {
		return nil, err
	}

	tasks, err := db.ListTasks(ctx, pool, db.ListFilter{
		Tags:           parsedTags,
		StaleThreshold: cfg.Worker.StaleDuration(),
	})
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, errWithHints("no tasks found matching tags",
			"Check with: "+codeStyle.Render("nextask list --tag "+strings.Join(waitTags, " --tag ")),
		)
	}

	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids, nil
}

func parseCompletionNotify(channel, payload string) (taskID string, exitCode int, ok bool) {
	eventType, data, err := db.ParseEvent(payload)
	if err != nil || eventType != db.EventTypeStatus {
		return "", 0, false
	}
	var status db.TaskStatusEvent
	if err := json.Unmarshal(data, &status); err != nil {
		return "", 0, false
	}
	taskID = strings.TrimPrefix(channel, "from_task_")
	fmt.Fprintf(os.Stderr, "task %s %s (exit %d)\n", taskID, status.Status, status.ExitCode)
	return taskID, status.ExitCode, true
}

func handleTimeout(remaining map[string]bool) error {
	ids := make([]string, 0, len(remaining))
	for id := range remaining {
		ids = append(ids, id)
	}
	fmt.Fprintf(os.Stderr, "timeout: %s still running\n", strings.Join(ids, ", "))
	return &exitCodeError{code: 124}
}

func withSignalCancel(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			signal.Stop(sigCh)
		}
	}()
	return ctx
}

// --- helpers ---

func isTerminal(status db.TaskStatus) bool {
	switch status {
	case db.StatusCompleted, db.StatusFailed, db.StatusCancelled, db.StatusStale:
		return true
	}
	return false
}

func taskExitCode(task *db.Task) int {
	if task.ExitCode != nil {
		return *task.ExitCode
	}
	if task.Status == db.StatusStale {
		return 1
	}
	return 0
}

func printWaitLine(id string, status db.TaskStatus, exitCode int) {
	if status == db.StatusStale {
		fmt.Fprintf(os.Stderr, "task %s stale (worker heartbeat expired)\n", id)
		return
	}
	fmt.Fprintf(os.Stderr, "task %s %s (exit %d)\n", id, status, exitCode)
}

func firstNonZero(current, new int) int {
	if current != 0 || new == 0 {
		return current
	}
	return new
}

func exitOrNil(code int) error {
	if code != 0 {
		return &exitCodeError{code: code}
	}
	return nil
}

func parseTags(tags []string) (map[string]string, error) {
	parsed := make(map[string]string, len(tags))
	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) != 2 {
			return nil, errWithHints(fmt.Sprintf("invalid tag format: %s", tag),
				"Expected format: "+codeStyle.Render("key=value"),
			)
		}
		parsed[parts[0]] = parts[1]
	}
	return parsed, nil
}
