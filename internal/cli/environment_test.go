package cli

import (
	"context"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func isolatedCLIEnv(t *testing.T) []string {
	t.Helper()
	return append(os.Environ(), "HOME="+t.TempDir(), "NEXTASK_DB_URL=", "NEXTASK_GIT_REMOTE=", "NEXTASK_SOURCE_REMOTE=", "S3_ACCESS_KEY=", "S3_SECRET_KEY=", "NEXTASK_TASK_ID=env-check")
}

func TestCredentialErrorsCLI(t *testing.T) {
	binary := buildTestCLI(t)
	dir, env := t.TempDir(), isolatedCLIEnv(t)
	run := func(environment []string, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir, cmd.Env = dir, environment
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(env, "list")
	if err == nil || !strings.Contains(out, "NEXTASK_DB_URL is required") {
		t.Fatalf("missing DB diagnostic: %v %s", err, out)
	}
	if out, err := run(env, "config", "show"); err != nil {
		t.Fatalf("diagnostics require credentials: %v %s", err, out)
	}
	out, err = run(env, "list", "--db-url", "postgres://user:argument-secret@host/db")
	if err == nil || !strings.Contains(out, "--db-url") || strings.Contains(out, "argument-secret") {
		t.Fatalf("removed flag accepted or exposed its value: %v %s", err, out)
	}
	file := filepath.Join(dir, ".nextask.toml")
	if err := os.WriteFile(file, []byte(`db.url = "postgres://user:file-secret@host/db"`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = run(env, "list")
	if err == nil || !strings.Contains(out, "db.url is not supported") || !strings.Contains(out, "NEXTASK_DB_URL") || strings.Contains(out, "file-secret") {
		t.Fatalf("unsafe migration diagnostic: %v %s", err, out)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	options := `{"endpoint":"https://storage.invalid","remote":"s3://bucket/project","include":["outputs/**"]}`
	for _, tc := range []struct {
		env     []string
		missing string
	}{
		{env, "S3_ACCESS_KEY, S3_SECRET_KEY"},
		{append(append([]string{}, env...), "S3_ACCESS_KEY=test-access"), "S3_SECRET_KEY"},
		{append(append([]string{}, env...), "S3_SECRET_KEY=test-secret"), "S3_ACCESS_KEY"},
	} {
		out, err = run(tc.env, "_run", "s3", options, "touch started", "0")
		if err == nil || !strings.Contains(out, "missing required worker environment variables: "+tc.missing) {
			t.Fatalf("missing S3 diagnostic: %v %s", err, out)
		}
		if strings.Contains(out, "test-secret") || strings.Contains(out, "test-access") {
			t.Fatal("credential error exposed a value")
		}
		if _, err := os.Stat(filepath.Join(dir, "started")); !os.IsNotExist(err) {
			t.Fatal("command ran without required S3 credentials")
		}
	}
}

func TestDaemonDatabaseEnvironment(t *testing.T) {
	pool := setupTestDB(t)
	binary := buildTestCLI(t)
	dir, workdir := t.TempDir(), t.TempDir()
	connection := getTestDBURL(t)
	env := append(isolatedCLIEnv(t), "NEXTASK_DB_URL="+connection)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI failed: %v %s", err, out)
		}
		return string(out)
	}
	id := "daemon-env"
	var encoded strings.Builder
	writer := csv.NewWriter(&encoded)
	if err := writer.Write([]string{"group=a,b \"quoted\"\nnext"}); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	filter := strings.TrimSuffix(encoded.String(), "\n")
	run("enqueue", "--tag", filter, "--id", id, `if (: >&3) 2>/dev/null; then exit 19; fi; printf '%s' "$NEXTASK_DB_URL" > db-env; echo ready > ready; while [ ! -f finish ]; do sleep 0.1; done`)
	out := run("worker", "--daemon", "--filter", filter, "--rm", "--exit-if-idle", "200ms", "--workdir", workdir)
	match := regexp.MustCompile(`pid ([0-9]+)`).FindStringSubmatch(out)
	if len(match) != 2 {
		t.Fatalf("missing daemon PID: %s", out)
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if syscall.Kill(pid, 0) == nil {
			_ = process.Signal(os.Interrupt)
			deadline := time.Now().Add(8 * time.Second)
			for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if syscall.Kill(pid, 0) == nil {
				_ = process.Kill()
			}
		}
		_ = process.Release()
	})
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("daemon did not reach expected state")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait(func() bool { _, err := os.Stat(filepath.Join(workdir, id, "ready")); return err == nil })
	args, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "--db-url") || strings.Contains(string(args), connection) {
		t.Fatal("database connection exposed in daemon arguments")
	}
	inherited, err := os.ReadFile(filepath.Join(workdir, id, "db-env"))
	if err != nil || string(inherited) != connection {
		t.Fatal("task lost worker database environment")
	}
	if err := os.WriteFile(filepath.Join(workdir, id, "finish"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		task, err := db.GetTask(context.Background(), pool, id, time.Minute)
		return err == nil && task != nil && task.Status == db.StatusCompleted
	})
	if _, err := os.Stat(filepath.Join(workdir, id)); !os.IsNotExist(err) {
		t.Fatalf("daemon ignored --rm: %v", err)
	}
	wait(func() bool {
		var stopped bool
		err := pool.QueryRow(context.Background(), "SELECT status = 'stopped' FROM workers WHERE pid = $1", pid).Scan(&stopped)
		return err == nil && stopped
	})
	// Plain tasks and daemon workers need no S3 credentials or Git integration.
}
