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

	if waitTimeout < 0 {
		return errWithHints("timeout must not be negative",
			"Example: "+codeStyle.Render("--timeout 30s"),
		)
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

	conn, err := pgx.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close(context.Background())

	if len(args) > 0 {
		return waitByIDs(ctx, pool, conn, args)
	}
	return waitByTags(ctx, pool, conn)
}

// waiter tracks task completion state across the wait lifecycle.
type waiter struct {
	pool      *pgxpool.Pool
	conn      *pgx.Conn
	remaining map[string]bool // tasks still waiting on
	seen      map[string]bool // all task IDs encountered (prevents re-processing)
	failCode  int
}

func newWaiter(pool *pgxpool.Pool, conn *pgx.Conn) *waiter {
	return &waiter{
		pool:      pool,
		conn:      conn,
		remaining: make(map[string]bool),
		seen:      make(map[string]bool),
	}
}

// listen subscribes to a task's completion channel.
func (w *waiter) listen(ctx context.Context, taskID string) error {
	_, err := w.conn.Exec(ctx, "LISTEN "+db.FromTaskChannel(taskID))
	return err
}

// track adds a task ID to the wait set and subscribes to its channel.
// Returns false if the task was already seen.
func (w *waiter) track(ctx context.Context, taskID string) (bool, error) {
	if w.seen[taskID] {
		return false, nil
	}
	w.seen[taskID] = true

	if err := w.listen(ctx, taskID); err != nil {
		return false, fmt.Errorf("listen failed: %w", err)
	}
	w.remaining[taskID] = true
	return true, nil
}

// check re-checks a tracked task's status. If terminal, it removes
// the task from remaining and records the exit code.
func (w *waiter) check(ctx context.Context, taskID string) error {
	task, err := db.GetTask(ctx, w.pool, taskID, cfg.Worker.StaleDuration())
	if err != nil {
		return err
	}
	if task == nil {
		delete(w.remaining, taskID)
		printError(errWithHints(
			fmt.Sprintf("task not found: %s", taskID),
			"Run "+codeStyle.Render("nextask list")+" to see available tasks",
		))
		w.failCode = firstNonZero(w.failCode, 1)
		return nil
	}
	if isTerminal(task.Status) {
		delete(w.remaining, taskID)
		code := taskExitCode(task)
		printWaitLine(task.ID, task.Status, code)
		w.failCode = firstNonZero(w.failCode, code)
	}
	return nil
}

// trackAndCheck adds a task to the wait set then immediately verifies
// its status. This is the core race-free pattern: LISTEN before check
// ensures no completion event is missed.
func (w *waiter) trackAndCheck(ctx context.Context, taskID string) error {
	added, err := w.track(ctx, taskID)
	if err != nil {
		return err
	}
	if !added {
		return nil
	}
	return w.check(ctx, taskID)
}

// done reports whether all tracked tasks have completed.
func (w *waiter) done() bool {
	return len(w.remaining) == 0
}

// handleCompletion processes a task status notification.
func (w *waiter) handleCompletion(channel, payload string) {
	id, code, ok := parseCompletionNotify(channel, payload)
	if !ok || !w.remaining[id] {
		return
	}
	delete(w.remaining, id)
	w.failCode = firstNonZero(w.failCode, code)
}

// --- wait modes ---

// waitByIDs waits for a fixed set of task IDs.
func waitByIDs(ctx context.Context, pool *pgxpool.Pool, conn *pgx.Conn, taskIDs []string) error {
	w := newWaiter(pool, conn)

	for _, id := range taskIDs {
		if err := w.trackAndCheck(ctx, id); err != nil {
			return err
		}
		if waitAny && w.failCode != 0 {
			return exitOrNil(w.failCode)
		}
	}

	return waitLoop(ctx, w, nil)
}

// waitByTags waits for all tasks matching the tag filter, including
// tasks enqueued after the wait begins.
func waitByTags(ctx context.Context, pool *pgxpool.Pool, conn *pgx.Conn) error {
	parsedTags, err := parseTags(waitTags)
	if err != nil {
		return err
	}

	w := newWaiter(pool, conn)

	// Subscribe to enqueue events before querying — no race gap.
	if _, err := conn.Exec(ctx, "LISTEN "+db.ToWorkersChannel); err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}

	// Discover initial tasks.
	if err := discover(ctx, w, parsedTags); err != nil {
		return err
	}
	if len(w.seen) == 0 {
		return errWithHints("no tasks found matching tags",
			"Check with: "+codeStyle.Render("nextask list --tag "+strings.Join(waitTags, " --tag ")),
		)
	}

	// On each wake event, re-query for newly enqueued tasks.
	onWake := func() error {
		return discover(ctx, w, parsedTags)
	}

	return waitLoop(ctx, w, onWake)
}

// discover queries pending/running tasks by tag and tracks any new ones.
func discover(ctx context.Context, w *waiter, tags map[string]string) error {
	tasks, err := db.ListTasks(ctx, w.pool, db.ListFilter{
		Tags:           tags,
		StaleThreshold: cfg.Worker.StaleDuration(),
	})
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if err := w.trackAndCheck(ctx, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// --- notification loop ---

// waitLoop blocks until all tracked tasks complete. If onWake is non-nil,
// it is called when a worker wake event arrives (new task enqueued).
func waitLoop(ctx context.Context, w *waiter, onWake func() error) error {
	for !w.done() {
		notif, err := w.conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return handleTimeout(w.remaining)
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("connection lost: %w", err)
		}

		if notif.Channel == db.ToWorkersChannel {
			if onWake != nil {
				if err := onWake(); err != nil {
					return err
				}
			}
			continue
		}

		w.handleCompletion(notif.Channel, notif.Payload)
		if waitAny && w.done() {
			break
		}
	}

	return exitOrNil(w.failCode)
}

// --- parsing and output ---

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
		if parts[0] == "" || parts[1] == "" {
			return nil, errWithHints(fmt.Sprintf("invalid tag format: %s", tag),
				"Tag key and value must not be empty",
				"Expected format: "+codeStyle.Render("key=value"),
			)
		}
		parsed[parts[0]] = parts[1]
	}
	return parsed, nil
}
