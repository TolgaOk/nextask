package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/spf13/cobra"
)

var (
	workdir    string
	workerName string
	once       bool
	daemon     bool
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Start a worker to process tasks",
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

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			select {
			case sig := <-sigCh:
				fmt.Printf("\nReceived %s, shutting down...\n", sig)
				cancel()
			case <-ctx.Done():
				signal.Stop(sigCh)
			}
		}()

		w, err := worker.New(ctx, worker.Config{
			DBURL:             cfg.DB.URL,
			Workdir:           cfg.Worker.Workdir,
			Name:              workerName,
			Once:              once,
			HeartbeatInterval: cfg.Worker.HeartbeatInterval,
		})
		if err != nil {
			return err
		}
		defer w.Close(context.Background())

		return w.Run(ctx)
	},
}

func init() {
	workerCmd.Flags().StringVar(&workdir, "workdir", "", "Base directory for task execution (default /tmp/nextask)")
	workerCmd.Flags().StringVar(&workerName, "name", "", "Worker identifier (default: random)")
	workerCmd.Flags().BoolVar(&once, "once", false, "Run single task and exit")
	workerCmd.Flags().BoolVar(&daemon, "daemon", false, "Run as background daemon")
	RootCmd.AddCommand(workerCmd)
}

func daemonize() error {
	// Generate worker ID if not provided (needed for log directory)
	name := workerName
	if name == "" {
		var err error
		name, err = gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 8)
		if err != nil {
			return fmt.Errorf("failed to generate worker id: %w", err)
		}
	}

	// Create log directory: <workdir>/.nextask/<worker_id>/
	logDir := filepath.Join(cfg.Worker.Workdir, ".nextask", name)
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

	// Build child command args (without --daemon, with explicit --name)
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable: %w", err)
	}

	args := []string{"worker", "--name", name, "--workdir", cfg.Worker.Workdir, "--db-url", cfg.DB.URL}
	if once {
		args = append(args, "--once")
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

	fmt.Printf("Worker %s started as daemon (pid %d)\n", name, pid)
	fmt.Printf("Logs: %s\n", logPath)

	return nil
}
