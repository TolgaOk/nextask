package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type waitOptions struct {
	tags    []string
	timeout durationFlag
	any     bool
}

func newWaitCommand(cfg *config.Config) *cobra.Command {
	var opts waitOptions
	cmd := &cobra.Command{
		Use:   "wait [TASK_ID...]",
		Short: "Block until tasks complete",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(opts.tags) > 0 && len(args) > 0 {
				return errWithHints("cannot use both task IDs and --tag",
					"Use either: "+codeStyle.Render("nextask wait <id1> <id2>"),
					"Or:         "+codeStyle.Render("nextask wait --tag key=value"),
				)
			}
			if len(opts.tags) == 0 && len(args) == 0 {
				return errWithHints("task ID or --tag is required",
					"Example: "+codeStyle.Render("nextask wait <id>"),
					"Or:      "+codeStyle.Render("nextask wait --tag key=value"),
				)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return runWait(cmd, args, *cfg, opts) },
	}

	cmd.Flags().StringSliceVar(&opts.tags, "tag", nil, "Select tasks matching tag key=value (repeatable)")
	opts.timeout.addFlag(cmd, "timeout", 0, "Exit 124 after duration; 0s disables the deadline")
	cmd.Flags().BoolVar(&opts.any, "any", false, "Return after the first observed terminal result")
	return cmd
}

func runWait(cmd *cobra.Command, args []string, cfg config.Config, opts waitOptions) error {
	if opts.timeout.Duration < 0 {
		return errWithHints("timeout must not be negative", "Use 0s to disable the deadline")
	}
	parsedTags, err := parseTags(opts.tags)
	if err != nil {
		return err
	}
	if cfg.DB.URL == "" {
		return errDBRequired()
	}
	ctx, stop := interruptContext(cmd.Context())
	defer stop()
	if opts.timeout.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout.Duration)
		defer cancel()
	}
	remaining := make(map[string]bool)
	for _, id := range args {
		remaining[id] = true
	}
	err = runTaskWait(ctx, cfg, outputFor(cmd).err, opts, parsedTags, args, remaining)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		return handleTimeout(cmd.ErrOrStderr(), remaining)
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func runTaskWait(ctx context.Context, cfg config.Config, stderr io.Writer, opts waitOptions, parsedTags map[string]string, ids []string, remaining map[string]bool) error {
	pool, err := db.Connect(ctx, cfg.DB.URL)
	if err != nil {
		return err
	}
	defer pool.Close()
	channels := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		channels = append(channels, db.FromTaskChannel(id))
	}
	if len(ids) == 0 {
		channels = append(channels, db.ToWorkersChannel)
	}
	watch, err := newStateWatcher(ctx, cfg, stderr, channels...)
	if err != nil {
		return err
	}
	defer watch.Close()
	w := &waiter{stderr: stderr, staleThreshold: cfg.Worker.StaleDuration(), pool: pool, watch: watch, remaining: remaining, any: opts.any, seen: make(map[string]bool)}
	for _, id := range ids {
		w.add(id)
	}
	err = watch.Run(ctx, func(ctx context.Context) (bool, error) {
		if len(ids) == 0 {
			if err := w.discover(ctx, parsedTags); err != nil {
				return false, err
			}
			if len(w.seen) == 0 {
				return false, errWithHints("no tasks found matching tags",
					"Check with: "+codeStyle.Render("nextask list --tag "+strings.Join(opts.tags, " --tag ")))
			}
		}
		return w.check(ctx)
	})
	if err != nil {
		return err
	}
	return exitOrNil(w.failCode)
}

// waiter owns one wait operation's task set and completion policy.
type waiter struct {
	stderr         io.Writer
	staleThreshold time.Duration
	pool           *pgxpool.Pool
	watch          *stateWatcher
	remaining      map[string]bool
	seen           map[string]bool
	order          []string
	any            bool
	failCode       int
}

func (w *waiter) add(id string) {
	if !w.seen[id] {
		w.seen[id] = true
		w.remaining[id] = true
		w.order = append(w.order, id)
	}
}

func (w *waiter) discover(ctx context.Context, tags map[string]string) error {
	tasks, err := db.ListTasks(ctx, w.pool, db.ListFilter{Tags: tags, StaleThreshold: w.staleThreshold})
	if err != nil {
		return err
	}
	var ids, channels []string
	for _, task := range tasks {
		if !w.seen[task.ID] {
			ids = append(ids, task.ID)
			channels = append(channels, db.FromTaskChannel(task.ID))
		}
	}
	if err := w.watch.notifier.Add(ctx, channels...); err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	for _, id := range ids {
		w.add(id)
	}
	return nil
}

func (w *waiter) check(ctx context.Context) (bool, error) {
	for _, id := range w.order {
		if !w.remaining[id] {
			continue
		}
		task, err := db.GetTask(ctx, w.pool, id, w.staleThreshold)
		if err != nil {
			return false, err
		}
		if task != nil && !isTerminal(task.Status) {
			continue
		}
		delete(w.remaining, id)
		code := 1
		if task == nil {
			err = printError(w.stderr, errWithHints(fmt.Sprintf("task not found: %s", id),
				"Run "+codeStyle.Render("nextask list")+" to see available tasks"))
		} else {
			code = taskExitCode(task)
			err = printWaitLine(w.stderr, id, task.Status, code)
		}
		if err != nil {
			return false, backoff.Permanent(err)
		}
		w.failCode = firstNonZero(w.failCode, code)
		if w.any {
			return true, nil
		}
	}
	return len(w.remaining) == 0, nil
}

func handleTimeout(stderr io.Writer, remaining map[string]bool) error {
	ids := make([]string, 0, len(remaining))
	for id := range remaining {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	_, err := fmt.Fprintf(stderr, "timeout: %s still running\n", strings.Join(ids, ", "))
	return errors.Join(&exitCodeError{code: 124}, err)
}

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

func printWaitLine(stderr io.Writer, id string, status db.TaskStatus, exitCode int) error {
	if status == db.StatusStale {
		_, err := fmt.Fprintf(stderr, "task %s stale (worker heartbeat expired)\n", id)
		return err
	}
	_, err := fmt.Fprintf(stderr, "task %s %s (exit %d)\n", id, status, exitCode)
	return err
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
