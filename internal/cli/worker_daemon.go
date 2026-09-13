package cli

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/moby/moby/pkg/namesgenerator"
)

const daemonStartupTimeout = 30 * time.Second
const daemonStopTimeout = 5 * time.Second

func daemonize(ctx context.Context, cfg worker.Config, timeout time.Duration) error {
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

	args := []string{"worker", "--_id", id, "--workdir", cfg.Workdir, "--_ready-fd", "3"}
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
	if len(cfg.TagFilter) > 0 {
		filters := sortedTags(cfg.TagFilter)
		var encoded strings.Builder
		writer := csv.NewWriter(&encoded)
		if err := writer.Write(filters); err != nil {
			return err
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		args = append(args, "--tag", strings.TrimSuffix(encoded.String(), "\n"))
	}

	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "NEXTASK_DB_URL="+cfg.DBURL)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	startupCtx, cancel := context.WithTimeout(ctx, daemonStartupTimeout)
	defer cancel()
	pid, err := startDaemon(startupCtx, cmd)
	if err != nil {
		return fmt.Errorf("daemon startup failed (logs: %s): %w", logPath, err)
	}
	_, err = fmt.Fprintf(cfg.Stderr, "Worker %s started as daemon (pid %d)\nLogs: %s\n", id, pid, logPath)
	return err
}

// startDaemon owns the child until readiness. Any failure stops and reaps it;
// only a confirmed worker is released to outlive the caller.
func startDaemon(ctx context.Context, cmd *exec.Cmd) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	defer writer.Close()
	cmd.ExtraFiles = []*os.File{writer}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	writer.Close()
	pid := cmd.Process.Pid
	if err := awaitDaemonReady(ctx, reader); err != nil {
		stopDaemon(cmd)
		return 0, err
	}
	if err := cmd.Process.Release(); err != nil {
		stopDaemon(cmd)
		return 0, fmt.Errorf("release daemon process: %w", err)
	}
	return pid, nil
}

func awaitDaemonReady(ctx context.Context, reader *os.File) error {
	done := make(chan error, 1)
	go func() {
		var ready [1]byte
		_, err := io.ReadFull(reader, ready[:])
		if errors.Is(err, io.EOF) {
			err = fmt.Errorf("worker exited before confirming readiness")
		}
		if err == nil && ready[0] != 1 {
			err = fmt.Errorf("invalid worker readiness response")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		reader.Close()
		<-done
		return ctx.Err()
	}
}

func stopDaemon(cmd *exec.Cmd) {
	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	timer := time.NewTimer(daemonStopTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		cmd.Process.Kill()
		<-done
	}
}
