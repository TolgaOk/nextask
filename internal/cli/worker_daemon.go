package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/moby/moby/pkg/namesgenerator"
)

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
