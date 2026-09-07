package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/pkg/namesgenerator"
	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

type workerOptions struct {
	workdir    string
	once       bool
	daemon     bool
	rm         bool
	exitIfIdle string
	id         string
	timeout    string
	filters    []string
}

func newWorkerCommand(cfg *config.Config) *cobra.Command {
	var opts workerOptions
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Start a worker to process tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			resolved, timeout, err := opts.resolve(*cfg)
			if err != nil {
				return err
			}
			out := outputFor(cmd)
			resolved.Stdout, resolved.Stderr = out.out, out.err
			if opts.daemon {
				return daemonize(resolved, timeout)
			}

			ctx, stop := interruptContext(cmd.Context())
			defer stop()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			w, err := worker.New(ctx, resolved)
			if err != nil {
				return err
			}
			defer w.Close()

			return w.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&opts.workdir, "workdir", "", "Base directory for task execution (default /tmp/nextask)")
	cmd.Flags().BoolVar(&opts.once, "once", false, "Run single task and exit")
	cmd.Flags().BoolVar(&opts.rm, "rm", false, "Remove task workdir after completion")
	cmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Run as background daemon")
	cmd.Flags().StringVar(&opts.timeout, "timeout", "", "Stop worker after duration (e.g., 1h, 24h, 7d)")
	cmd.Flags().StringVar(&opts.exitIfIdle, "exit-if-idle", "", "Exit if no tasks claimed within duration (e.g., 0s, 1m, 5m)")
	cmd.Flags().StringSliceVar(&opts.filters, "filter", nil, "Only claim tasks with tag (key=value, repeatable)")
	cmd.Flags().StringVar(&opts.id, "_id", "", "Worker ID (internal use)")
	cmd.Flags().MarkHidden("_id")
	cmd.AddCommand(newWorkerListCommand(cfg), newWorkerStopCommand(cfg))
	return cmd
}

// resolve validates flags before either starting a worker or spawning a daemon.
func (opts workerOptions) resolve(cfg config.Config) (worker.Config, time.Duration, error) {
	resolved := worker.Config{
		DBURL: cfg.DB.URL, Workdir: cfg.Worker.Workdir, Name: opts.id,
		Once: opts.once, Rm: opts.rm,
		HeartbeatInterval: cfg.Worker.HeartbeatInterval,
		BackoffInitial:    cfg.Retry.InitialInterval, BackoffMax: cfg.Retry.MaxInterval,
		LogFlushLines: cfg.Worker.LogFlushLines, LogFlushInterval: cfg.Worker.LogFlushInterval,
		LogBufferSize: cfg.Worker.LogBufferSize,
	}
	if opts.workdir != "" {
		resolved.Workdir = opts.workdir
	}
	var timeout time.Duration
	if opts.timeout != "" {
		var err error
		timeout, err = str2duration.ParseDuration(opts.timeout)
		if err != nil {
			return worker.Config{}, 0, errWithHints(fmt.Sprintf("invalid timeout: %s", opts.timeout),
				"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"))
		}
		if timeout <= 0 {
			return worker.Config{}, 0, errWithHints("timeout must be positive",
				"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"))
		}
	}
	if opts.exitIfIdle != "" {
		duration, err := str2duration.ParseDuration(opts.exitIfIdle)
		if err != nil {
			return worker.Config{}, 0, errWithHints(fmt.Sprintf("invalid exit-if-idle: %s", opts.exitIfIdle),
				"Examples: "+codeStyle.Render("0s")+", "+codeStyle.Render("1m")+", "+codeStyle.Render("5m"))
		}
		if duration < 0 {
			return worker.Config{}, 0, errWithHints("exit-if-idle must not be negative",
				"Examples: "+codeStyle.Render("0s")+", "+codeStyle.Render("1m")+", "+codeStyle.Render("5m"))
		}
		resolved.ExitIfIdle = &duration
	}
	var err error
	resolved.TagFilter, err = parseTags(opts.filters)
	if err != nil {
		return worker.Config{}, 0, err
	}
	return resolved, timeout, nil
}

type workerListOptions struct {
	limit  int
	offset int
	since  string
	json   bool
	csv    bool
	wrap   bool
}

func newWorkerListCommand(cfg *config.Config) *cobra.Command {
	var opts workerListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered workers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			ctx := cmd.Context()
			pool, err := db.Connect(ctx, cfg.DB.URL)
			if err != nil {
				return err
			}
			defer pool.Close()

			statusFlag, _ := cmd.Flags().GetString("status")
			var statusFilter *db.WorkerStatus
			if statusFlag != "" {
				s := db.WorkerStatus(statusFlag)
				switch s {
				case db.WorkerStatusRunning, db.WorkerStatusStopped, db.WorkerStatusStale:
				default:
					return errWithHints(fmt.Sprintf("unknown status: %s", statusFlag),
						"Valid: "+codeStyle.Render("running")+", "+codeStyle.Render("stopped")+", "+codeStyle.Render("stale"),
					)
				}
				statusFilter = &s
			}

			var since time.Time
			if opts.since != "" {
				dur, err := str2duration.ParseDuration(opts.since)
				if err != nil {
					return errWithHints(fmt.Sprintf("invalid since format: %s", opts.since),
						"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"),
					)
				}
				since = time.Now().Add(-dur)
			}

			if opts.limit <= 0 {
				return errWithHints("limit must be positive",
					"Example: "+codeStyle.Render("--limit 50"),
				)
			}

			if opts.offset < 0 {
				return errWithHints("offset must not be negative",
					"Example: "+codeStyle.Render("--offset 50"),
				)
			}

			filter := db.WorkerListFilter{
				Status:         statusFilter,
				Since:          since,
				Limit:          uint64(opts.limit),
				Offset:         uint64(opts.offset),
				StaleThreshold: cfg.Worker.StaleDuration(),
			}

			workers, err := db.ListWorkers(ctx, pool, filter)
			if err != nil {
				return err
			}

			total, err := db.CountWorkers(ctx, pool, filter)
			if err != nil {
				return err
			}

			if len(workers) == 0 {
				if opts.json {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "[]")
				} else if opts.csv {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "ID,PID,HOSTNAME,STATUS,STARTED")
				} else {
					_, err = fmt.Fprintln(cmd.ErrOrStderr(), "No workers found")
				}
				return err
			}

			plain := opts.json || opts.csv

			rows := [][]string{}
			for _, w := range workers {
				status := string(w.Status)
				displayStatus := status
				if !plain {
					displayStatus = statusStyle(db.TaskStatus(status)).Render(status)
				}
				rows = append(rows, []string{
					w.ID,
					fmt.Sprintf("%d", w.PID),
					w.Hostname,
					displayStatus,
					w.StartedAt.Format("2006-01-02 15:04"),
				})
			}

			return PrintTable(outputFor(cmd), TableConfig{
				Headers: []string{"ID", "PID", "HOSTNAME", "STATUS", "STARTED"},
				Rows:    rows,
				Count:   total,
				Offset:  opts.offset,
				JSON:    opts.json,
				CSV:     opts.csv,
				Wrap:    opts.wrap,
			})
		},
	}
	cmd.Flags().String("status", "", "Filter by status (running, stopped, stale)")
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "Max results")
	cmd.Flags().IntVar(&opts.offset, "offset", 0, "Skip first N results")
	cmd.Flags().StringVar(&opts.since, "since", "", "Workers started after (e.g., 1h, 24h, 7d)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&opts.csv, "csv", false, "Output as CSV")
	cmd.Flags().BoolVar(&opts.wrap, "wrap", false, "Wrap long lines instead of truncating")
	return cmd
}

type workerStopOptions struct {
	timeout time.Duration
}

func newWorkerStopCommand(cfg *config.Config) *cobra.Command {
	var opts workerStopOptions
	cmd := &cobra.Command{
		Use:   "stop WORKER_ID",
		Short: "Stop a running worker",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			if opts.timeout <= 0 {
				return errWithHints("timeout must be positive",
					"Example: "+codeStyle.Render("--timeout 10s"),
				)
			}

			ctx, stop := interruptContext(cmd.Context())
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, opts.timeout)
			defer cancel()
			pool, err := db.Connect(ctx, cfg.DB.URL)
			if err != nil {
				return err
			}
			defer pool.Close()
			return stopWorker(ctx, *cfg, outputFor(cmd).err, pool, args[0])
		},
	}
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 10*time.Second, "Timeout waiting for stop confirmation")
	return cmd
}

// stopWorker checks the registry before sending a hint and confirms stored state
// after subscribing, including when the worker's confirmation hint is lost.
func stopWorker(ctx context.Context, cfg config.Config, stderr io.Writer, pool *pgxpool.Pool, id string) error {
	status, err := workerStatus(ctx, pool, id)
	if err != nil {
		return err
	}
	if status == db.WorkerStatusStopped {
		_, err := fmt.Fprintf(stderr, "Worker %s is already stopped\n", id)
		return err
	}
	watch, err := newStateWatcher(ctx, cfg, stderr, db.FromWorkerChannel(id))
	if err != nil {
		return err
	}
	defer watch.Close()
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, '')", db.ToWorkerChannel(id)); err != nil {
		return fmt.Errorf("failed to send stop signal: %w", err)
	}
	err = watch.Run(ctx, func(ctx context.Context) (bool, error) {
		status, err := workerStatus(ctx, pool, id)
		return status == db.WorkerStatusStopped, err
	})
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		return errWithHints("stop signal sent but worker did not confirm",
			"Worker may be unresponsive or processing a task",
			"Check worker status with "+codeStyle.Render("nextask worker list"))
	}
	if err == nil {
		_, err = fmt.Fprintf(stderr, "Worker %s stopped\n", id)
	}
	return err
}

func workerStatus(ctx context.Context, pool *pgxpool.Pool, id string) (db.WorkerStatus, error) {
	workers, err := db.ListWorkers(ctx, pool, db.WorkerListFilter{ID: id, Limit: 1})
	if err != nil {
		return "", err
	}
	if len(workers) == 0 {
		return "", errWithHints(fmt.Sprintf("worker not found: %s", id),
			"Run "+codeStyle.Render("nextask worker list")+" to see available workers")
	}
	return workers[0].Status, nil
}

func daemonize(cfg worker.Config, timeout time.Duration) error {
	id := cfg.Name
	if id == "" {
		id = namesgenerator.GetRandomName(0)
	}

	// Create log directory: <workdir>/.nextask/<worker_id>/
	logDir := filepath.Join(cfg.Workdir, ".nextask", id)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Open log file
	logPath := filepath.Join(logDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	// Build child command args (without --daemon, with hidden --_id)
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable: %w", err)
	}

	args := []string{"worker", "--_id", id, "--workdir", cfg.Workdir}
	if cfg.Once {
		args = append(args, "--once")
	}
	if cfg.Rm {
		args = append(args, "--rm")
	}
	if timeout > 0 {
		args = append(args, "--timeout", timeout.String())
	}
	if cfg.ExitIfIdle != nil {
		args = append(args, "--exit-if-idle", cfg.ExitIfIdle.String())
	}
	for key, value := range cfg.TagFilter {
		args = append(args, "--filter", key+"="+value)
	}

	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "NEXTASK_DB_URL="+cfg.DBURL)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	pid := cmd.Process.Pid

	// Release child so it continues after parent exits
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("failed to release daemon process: %w", err)
	}

	_, err = fmt.Fprintf(cfg.Stderr, "Worker %s started as daemon (pid %d)\nLogs: %s\n", id, pid, logPath)
	return err
}
