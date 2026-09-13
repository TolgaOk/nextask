package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/storage/storagetest"
)

func TestS3CLI(t *testing.T) {
	pool := setupTestDB(t)
	binary := buildTestCLI(t)
	server := storagetest.New()
	t.Cleanup(server.Close)
	root := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	workdir, home := t.TempDir(), t.TempDir()
	env := append(os.Environ(), "HOME="+home, "NEXTASK_DB_URL="+getTestDBURL(t), "NEXTASK_SOURCE_REMOTE=", "NEXTASK_GIT_URL=", "NEXTASK_S3_ENDPOINT=", "NEXTASK_GIT_REMOTE=", "NEXTASK_TASK_ID=inherited", "NEXTASK_EXECUTABLE=/invalid/inherited/path", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Dir, cmd.Env = root, env
		return cmd
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := command(args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = root, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("source snapshot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "input.txt")
	git("commit", "-qm", "Init")
	remote := filepath.Join(t.TempDir(), "snapshots.git")
	git("init", "--bare", "-q", remote)
	env = append(env, "TEST_GIT_CONNECTION="+remote, "TEST_STORAGE_CONNECTION="+strings.Replace(server.URL, "://", "://test-access:test-secret@", 1))
	config := fmt.Sprintf(`[integrations.git]
remote = %q
[integrations.s3]
endpoint = %q
region = "fsn1"
remote = "s3://bucket/project"
include = ["outputs/**"]
final_include = ["reports/**"]
exclude = ["**/*.tmp"]
interval = "50ms"
final_timeout = "10s"
`, "${TEST_GIT_CONNECTION}", "${TEST_STORAGE_CONNECTION}")
	if err := os.WriteFile(filepath.Join(root, ".nextask.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	configOut := run("config", "show", "--sources")
	if !strings.Contains(configOut, `integrations.s3.include = ["outputs/**"]`) || strings.Contains(configOut, "test-secret") {
		t.Fatalf("bad diagnostics: %s", configOut)
	}
	if strings.Contains(run("enqueue", "--help"), "--without") || strings.Contains(run("--help"), "_run") {
		t.Fatal("internal flags leaked into help")
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for task")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	startWorker := func() (*exec.Cmd, <-chan error, *bytes.Buffer) {
		cmd := command("worker", "--once", "--workdir", workdir)
		output := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		finished := make(chan struct{})
		go func() {
			done <- cmd.Wait()
			close(finished)
		}()
		t.Cleanup(func() {
			select {
			case <-finished:
				return
			default:
			}
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-finished:
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
				<-finished
			}
		})
		return cmd, done, output
	}
	awaitWorker := func(done <-chan error, output *bytes.Buffer) {
		t.Helper()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("worker: %v\n%s", err, output)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("worker did not finish")
		}
	}
	payload := `mkdir -p outputs reports; cat input.txt > outputs/artifact; echo temp > outputs/file.tmp; while [ ! -f continue ]; do sleep 0.05; done; echo final > reports/result`
	run("enqueue", "--id", "s3-periodic", "--with", "git", "--with", "s3", payload)
	task, err := db.GetTask(context.Background(), pool, "s3-periodic", time.Minute)
	if err != nil || task == nil || task.CleanupTimeoutMS != 10000 || task.Command != payload || task.ExecutionCommand == nil || strings.Contains(*task.ExecutionCommand, "test-secret") {
		t.Fatalf("invalid task: %+v %v", task, err)
	}
	_, done, output := startWorker()
	wait(func() bool { _, ok := server.Object("bucket/project/s3-periodic/outputs/artifact"); return ok })
	task, err = db.GetTask(context.Background(), pool, "s3-periodic", time.Minute)
	if err != nil || task.Status != db.StatusRunning {
		t.Fatal("periodic upload happened after completion")
	}
	if _, ok := server.Object("bucket/project/s3-periodic/reports/result"); ok {
		t.Fatal("final-only file uploaded early")
	}
	if err := os.WriteFile(filepath.Join(workdir, "s3-periodic", "continue"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitWorker(done, output)
	if !strings.Contains(run("show", "s3-periodic"), "completed") {
		t.Fatal("task not completed")
	}
	if o, ok := server.Object("bucket/project/s3-periodic/reports/result"); !ok || string(o.Data) != "final\n" {
		t.Fatal("missing final upload")
	}
	if _, ok := server.Object("bucket/project/s3-periodic/outputs/file.tmp"); ok {
		t.Fatal("excluded file uploaded")
	}
	run("wait", "s3-periodic")
	run("remove", "s3-periodic")
	if _, ok := server.Object("bucket/project/s3-periodic/outputs/artifact"); !ok {
		t.Fatal("removal deleted artifact")
	}
	before, _ := server.Counts()
	run("enqueue", "--id", "s3-plain", "mkdir -p outputs; echo plain > outputs/file")
	run("worker", "--once", "--workdir", workdir)
	if after, _ := server.Counts(); after != before {
		t.Fatal("config enabled storage")
	}

	// This final upload exceeds the old worker's five-second SIGKILL deadline.
	server.DelayPuts(6 * time.Second)
	run("enqueue", "--id", "s3-cancel", "--with", "s3", "--set", "s3.interval=0s", `trap 'mkdir -p outputs; echo saved > outputs/result; exit 0' INT; echo ready > ready; while :; do sleep 1; done`)
	_, done, output = startWorker()
	wait(func() bool { _, err := os.Stat(filepath.Join(workdir, "s3-cancel", "ready")); return err == nil })
	run("cancel", "s3-cancel")
	awaitWorker(done, output)
	if !strings.Contains(run("show", "s3-cancel"), "cancelled") {
		t.Fatal("cancelled state lost")
	}
	if o, ok := server.Object("bucket/project/s3-cancel/outputs/result"); !ok || string(o.Data) != "saved\n" {
		t.Fatalf("shutdown cut off final upload: %s", output)
	}
}
