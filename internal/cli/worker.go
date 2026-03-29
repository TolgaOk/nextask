package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/pkg/namesgenerator"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

var (
	workdir        string
	once           bool
	daemon         bool
	rm             bool
	exitIfIdle     string
	workerID       string // hidden, used by daemon mode
	workerTimeout  string
	workerFilters  []string
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Start a worker to process tasks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		// Apply command-specific flag
		if workdir != "" {
			cfg.Worker.Workdir = workdir
		}
		// Use default if still empty
		if cfg.Worker.Workdir == "" {
			cfg.Worker.Workdir = "/tmp/nextask"
		}

		// Daemon mode: spawn child process without --daemon and exit
		if daemon {
			return daemonize()
		}

		// Parse timeout if provided
		var timeout time.Duration
		if workerTimeout != "" {
			var err error
			timeout, err = str2duration.ParseDuration(workerTimeout)
			if err != nil {
				return errWithHints(fmt.Sprintf("invalid timeout: %s", workerTimeout),
					"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"),
				)
			}
		}

		// Parse exit-if-idle if provided
		var exitIfIdleDuration *time.Duration
		if exitIfIdle != "" {
			d, err := str2duration.ParseDuration(exitIfIdle)
			if err != nil {
				return errWithHints(fmt.Sprintf("invalid exit-if-idle: %s", exitIfIdle),
					"Examples: "+codeStyle.Render("0s")+", "+codeStyle.Render("1m")+", "+codeStyle.Render("5m"),
				)
			}
			exitIfIdleDuration = &d
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start timeout goroutine if specified
		if timeout > 0 {
			go func() {
				select {
				case <-time.After(timeout):
					fmt.Fprintf(os.Stderr, "\nTimeout reached (%s), shutting down...\n", workerTimeout)
					cancel()
				case <-ctx.Done():
				}
			}()
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			select {
			case sig := <-sigCh:
				fmt.Fprintf(os.Stderr, "\nReceived %s, shutting down...\n", sig)
				cancel()
			case <-ctx.Done():
				signal.Stop(sigCh)
			}
		}()

		// Parse tag filters
		tagFilter := make(map[string]string)
		for _, f := range workerFilters {
			parts := strings.SplitN(f, "=", 2)
			if len(parts) != 2 {
				return errWithHints(fmt.Sprintf("invalid filter format: %s", f),
					"Expected format: "+codeStyle.Render("key=value"),
				)
			}
			tagFilter[parts[0]] = parts[1]
		}

		w, err := worker.New(ctx, worker.Config{
			DBURL:             cfg.DB.URL,
			Workdir:           cfg.Worker.Workdir,
			Name:              workerID,
			Once:              once,
			Rm:                rm,
			ExitIfIdle:        exitIfIdleDuration,
			HeartbeatInterval: cfg.Worker.HeartbeatInterval,
			TagFilter:         tagFilter,
			LogFlushLines:     cfg.Worker.LogFlushLines,
			LogFlushInterval:  cfg.Worker.LogFlushInterval,
			LogBufferSize:     cfg.Worker.LogBufferSize,
		})
		if err != nil {
			return err
		}
		defer w.Close()

		return w.Run(ctx)
	},
}

var (
	workerListLimit  int
	workerListOffset int
	workerListSince  string
	workerListJSON   bool
	workerListCSV    bool
	workerListWrap   bool
)

var workerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered workers",
	Args:  cobra.NoArgs,
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

		statusFlag, _ := cmd.Flags().GetString("status")
		var statusFilter *db.WorkerStatus
		if statusFlag != "" {
			s := db.WorkerStatus(statusFlag)
			switch s {
			case db.WorkerStatusRunning, db.WorkerStatusStopped:
			default:
				return errWithHints(fmt.Sprintf("unknown status: %s", statusFlag),
					"Valid: "+codeStyle.Render("running")+", "+codeStyle.Render("stopped"),
				)
			}
			statusFilter = &s
		}

		var since time.Time
		if workerListSince != "" {
			dur, err := str2duration.ParseDuration(workerListSince)
			if err != nil {
				return errWithHints(fmt.Sprintf("invalid since format: %s", workerListSince),
					"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"),
				)
			}
			since = time.Now().Add(-dur)
		}

		filter := db.WorkerListFilter{
			Status: statusFilter,
			Since:  since,
			Limit:  uint64(workerListLimit),
			Offset: uint64(workerListOffset),
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
			if workerListJSON {
				fmt.Println("[]")
			} else if workerListCSV {
				fmt.Println("ID,PID,HOSTNAME,STATUS,STARTED")
			} else {
				fmt.Fprintln(os.Stderr, "No workers found")
			}
			return nil
		}

		staleThreshold := cfg.Worker.StaleDuration()
		plain := workerListJSON || workerListCSV

		rows := [][]string{}
		for _, w := range workers {
			status := string(w.Status)
			if w.Status == db.WorkerStatusRunning && time.Since(w.LastHeartbeat) > staleThreshold {
				status = "stale"
			}
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

		return PrintTable(TableConfig{
			Headers: []string{"ID", "PID", "HOSTNAME", "STATUS", "STARTED"},
			Rows:    rows,
			Count:   total,
			Offset:  workerListOffset,
			JSON:    workerListJSON,
			CSV:     workerListCSV,
			Wrap:    workerListWrap,
		})
	},
}

var (
	workerStopTimeout time.Duration
)

var workerStopCmd = &cobra.Command{
	Use:   "stop WORKER_ID",
	Short: "Stop a running worker",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		ctx := context.Background()
		workerID := args[0]

		pool, err := db.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer pool.Close()

		// Verify worker exists and is running
		workers, err := db.ListWorkers(ctx, pool, db.WorkerListFilter{})
		if err != nil {
			return err
		}

		var found *db.WorkerRecord
		for _, w := range workers {
			if w.ID == workerID {
				found = &w
				break
			}
		}

		if found == nil {
			return errWithHints(
				fmt.Sprintf("worker not found: %s", workerID),
				"Run "+codeStyle.Render("nextask worker list")+" to see available workers",
			)
		}

		if found.Status == db.WorkerStatusStopped {
			fmt.Fprintf(os.Stderr, "Worker %s is already stopped\n", workerID)
			return nil
		}

		// Set up listener for confirmation before sending stop signal
		listenConn, err := pgx.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer listenConn.Close(ctx)

		fromWorkerCh := db.FromWorkerChannel(workerID)
		if _, err := listenConn.Exec(ctx, `LISTEN "`+fromWorkerCh+`"`); err != nil {
			return err
		}

		// Send stop notification
		toWorkerCh := db.ToWorkerChannel(workerID)
		if _, err := pool.Exec(ctx, "SELECT pg_notify($1, '')", toWorkerCh); err != nil {
			return fmt.Errorf("failed to send stop signal: %w", err)
		}

		// Wait for confirmation with timeout
		waitCtx, waitCancel := context.WithTimeout(ctx, workerStopTimeout)
		defer waitCancel()

		_, err = listenConn.WaitForNotification(waitCtx)
		if err != nil {
			if waitCtx.Err() == context.DeadlineExceeded {
				return errWithHints("stop signal sent but worker did not confirm",
					"Worker may be unresponsive or processing a task",
					"Check worker status with "+codeStyle.Render("nextask worker list"),
				)
			}
			return err
		}

		fmt.Fprintf(os.Stderr, "Worker %s stopped\n", workerID)
		return nil
	},
}

func init() {
	workerCmd.Flags().StringVar(&workdir, "workdir", "", "Base directory for task execution (default /tmp/nextask)")
	workerCmd.Flags().BoolVar(&once, "once", false, "Run single task and exit")
	workerCmd.Flags().BoolVar(&rm, "rm", false, "Remove task workdir after completion")
	workerCmd.Flags().BoolVar(&daemon, "daemon", false, "Run as background daemon")
	workerCmd.Flags().StringVar(&workerTimeout, "timeout", "", "Stop worker after duration (e.g., 1h, 24h, 7d)")
	workerCmd.Flags().StringVar(&exitIfIdle, "exit-if-idle", "", "Exit if no tasks claimed within duration (e.g., 0s, 1m, 5m)")
	workerCmd.Flags().StringSliceVar(&workerFilters, "filter", nil, "Only claim tasks with tag (key=value, repeatable)")
	workerCmd.Flags().StringVar(&workerID, "_id", "", "Worker ID (internal use)")
	workerCmd.Flags().MarkHidden("_id")

	workerListCmd.Flags().String("status", "", "Filter by status (running, stopped)")
	workerListCmd.Flags().IntVar(&workerListLimit, "limit", 50, "Max results")
	workerListCmd.Flags().IntVar(&workerListOffset, "offset", 0, "Skip first N results")
	workerListCmd.Flags().StringVar(&workerListSince, "since", "", "Workers started after (e.g., 1h, 24h, 7d)")
	workerListCmd.Flags().BoolVar(&workerListJSON, "json", false, "Output as JSON")
	workerListCmd.Flags().BoolVar(&workerListCSV, "csv", false, "Output as CSV")
	workerListCmd.Flags().BoolVar(&workerListWrap, "wrap", false, "Wrap long lines instead of truncating")
	workerCmd.AddCommand(workerListCmd)

	workerStopCmd.Flags().DurationVar(&workerStopTimeout, "timeout", 10*time.Second, "Timeout waiting for stop confirmation")
	workerCmd.AddCommand(workerStopCmd)

	RootCmd.AddCommand(workerCmd)
}

func daemonize() error {
	id := namesgenerator.GetRandomName(0)

	// Create log directory: <workdir>/.nextask/<worker_id>/
	logDir := filepath.Join(cfg.Worker.Workdir, ".nextask", id)
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

	args := []string{"worker", "--_id", id, "--workdir", cfg.Worker.Workdir, "--db-url", cfg.DB.URL}
	if once {
		args = append(args, "--once")
	}
	if workerTimeout != "" {
		args = append(args, "--timeout", workerTimeout)
	}
	for _, f := range workerFilters {
		args = append(args, "--filter", f)
	}

	cmd := exec.Command(exe, args...)
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

	fmt.Fprintf(os.Stderr, "Worker %s started as daemon (pid %d)\n", id, pid)
	fmt.Fprintf(os.Stderr, "Logs: %s\n", logPath)

	return nil
}
