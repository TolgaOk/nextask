package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestDaemonStartupFailure(t *testing.T) {
	binary := buildTestCLI(t)
	for _, failure := range []string{"connection", "journal"} {
		t.Run(failure, func(t *testing.T) {
			pool := setupTestDB(t)
			defer pool.Close()
			workdir := t.TempDir()
			connection := getTestDBURL(t)
			if failure == "connection" {
				connection = "postgres://test@127.0.0.1:1/test?sslmode=disable"
			} else {
				journal := filepath.Join(workdir, ".nextask", "completions")
				if err := os.MkdirAll(journal, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(journal, "corrupt.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "worker", "--daemon", "--once", "--workdir", workdir, "--_id", "failed-daemon")
			cmd.Dir = t.TempDir()
			cmd.Env = append(isolatedCLIEnv(t), "NEXTASK_DB_URL="+connection, "GORACE=atexit_sleep_ms=0")
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "daemon startup failed") || strings.Contains(string(output), "started as daemon") {
				t.Fatalf("startup failure reported success: %v %s", err, output)
			}
			if ctx.Err() != nil {
				t.Fatal("startup failure hung")
			}
			logPath := filepath.Join(workdir, ".nextask", "failed-daemon", "daemon.log")
			if !strings.Contains(string(output), logPath) {
				t.Fatalf("missing diagnostic path: %s", output)
			}
			if failure == "journal" {
				var status db.WorkerStatus
				if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id='failed-daemon'").Scan(&status); err != nil || status != db.WorkerStatusStopped {
					t.Fatalf("startup registration left active: %s %v", status, err)
				}
			}
		})
	}
}

func TestDaemonStartupReapsChild(t *testing.T) {
	for _, scenario := range []string{"exit", "invalid", "timeout", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			script := "exec sleep 30"
			if scenario == "exit" {
				script = "exit 9"
			}
			if scenario == "invalid" {
				script = "printf x >&3; exec sleep 30"
			}
			if scenario == "cancel" {
				timer := time.AfterFunc(20*time.Millisecond, cancel)
				defer timer.Stop()
			}
			cmd := exec.Command("sh", "-c", script)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			_, err := startDaemon(ctx, cmd)
			if err == nil {
				t.Fatal("unready child accepted")
			}
			if scenario == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if cmd.Process == nil || cmd.ProcessState == nil {
				t.Fatal("failed child was not started and reaped")
			}
		})
	}
}
