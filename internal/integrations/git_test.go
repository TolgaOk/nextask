package integrations

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func fixture(t *testing.T) (root, remote string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "spaces and 'quotes")
	root, remote = filepath.Join(base, "project"), filepath.Join(base, "remote.git")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "init", "-q")
	runGitTest(t, root, "config", "user.name", "Test")
	runGitTest(t, root, "config", "user.email", "test@example.invalid")
	writeFixture(t, root, "tracked.txt", "original\n")
	writeFixture(t, root, "deleted.txt", "delete me\n")
	writeFixture(t, root, "ignored-but-tracked", "keep tracked\n")
	runGitTest(t, root, "add", ".")
	runGitTest(t, root, "commit", "-qm", "Init")
	runGitTest(t, root, "init", "--bare", "-q", remote)
	runGitTest(t, root, "remote", "add", "snapshots", remote)
	return
}

func writeFixture(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func repoState(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = "directory"
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var content []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			content = []byte(target)
		} else {
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		result[path] = fmt.Sprintf("%v:%x", info.Mode(), sha256.Sum256(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func executeWrapper(t *testing.T, command string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	// Task logging starts before the Git wrapper, so the directory isn't empty.
	if err := os.MkdirAll(filepath.Join(dir, ".nextask"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, ".nextask"), "log", "worker log\n")
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NEXTASK_TASK_ID=test-task")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestGitSnapshotPreservesRepository(t *testing.T) {
	root, remote := fixture(t)
	writeFixture(t, root, "tracked.txt", "staged\n")
	runGitTest(t, root, "add", "tracked.txt")
	writeFixture(t, root, "tracked.txt", "working\n")
	writeFixture(t, root, ".gitignore", "ignored*\n")
	writeFixture(t, root, "ignored.txt", "excluded\n")
	writeFixture(t, root, "space ' and\nnewline.txt", "untracked\n")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tracked.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "job.sh", "#!/bin/sh\nprintf '%s\\n' ok\n")
	if err := os.Chmod(filepath.Join(root, "job.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ntouch " + Quote(filepath.Join(root, "hook-ran")) + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "hooks", "pre-push"), []byte(hook), 0755); err != nil {
		t.Fatal(err)
	}
	before := repoState(t, root)
	snapshot, err := publishSnapshot(context.Background(), root, "test-task", "snapshots")
	if err != nil {
		t.Fatal(err)
	}
	if after := repoState(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("snapshot changed source files or .git")
	}
	if snapshot.Ref != "refs/heads/project/test-task" {
		t.Fatalf("ref = %s", snapshot.Ref)
	}
	for name, want := range map[string]string{"tracked.txt": "working", "ignored-but-tracked": "keep tracked", "space ' and\nnewline.txt": "untracked", "link": "tracked.txt"} {
		if got := runGitTest(t, remote, "show", snapshot.Commit+":"+name); got != want {
			t.Errorf("%q = %q, want %q", name, got, want)
		}
	}
	files := runGitTest(t, remote, "ls-tree", "-r", "--name-only", snapshot.Commit)
	if strings.Contains(files, "ignored.txt") || strings.Contains(files, "deleted.txt") {
		t.Fatalf("incorrect file selection: %s", files)
	}
	command, err := snapshot.Wrap(`printf '%s\n' "argument with 'quotes'" "$NEXTASK_TASK_ID"; cat tracked.txt; ./job.sh`)
	if err != nil {
		t.Fatal(err)
	}
	output, err := executeWrapper(t, command)
	if err != nil {
		t.Fatalf("wrapper: %v\n%s", err, output)
	}
	if !strings.Contains(output, "argument with 'quotes'\ntest-task\nworking\nok") {
		t.Fatalf("unexpected output: %s", output)
	}
	if _, err := publishSnapshot(context.Background(), root, "test-task", "snapshots"); err == nil {
		t.Fatal("existing remote ref was overwritten")
	}
	if after := repoState(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("failed publication changed source repository")
	}
}

func TestGitSnapshotFailureAndWorktree(t *testing.T) {
	root, _ := fixture(t)
	worktree := filepath.Join(filepath.Dir(root), "linked")
	runGitTest(t, root, "worktree", "add", "-q", "-b", "linked", worktree)
	writeFixture(t, worktree, "tracked.txt", "linked working files\n")
	beforeRoot, beforeWorktree := repoState(t, root), repoState(t, worktree)
	if _, err := publishSnapshot(context.Background(), worktree, "linked-task", "snapshots"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeRoot, repoState(t, root)) || !reflect.DeepEqual(beforeWorktree, repoState(t, worktree)) {
		t.Fatal("linked worktree or shared Git directory changed")
	}
	if _, err := publishSnapshot(context.Background(), worktree, "failed-task", filepath.Join(t.TempDir(), "missing.git")); err == nil {
		t.Fatal("missing remote accepted")
	}
	if !reflect.DeepEqual(beforeRoot, repoState(t, root)) || !reflect.DeepEqual(beforeWorktree, repoState(t, worktree)) {
		t.Fatal("failed push changed linked repository")
	}
}

func TestGitWrapperPinsCommitAndPreservesExit(t *testing.T) {
	root, remote := fixture(t)
	snapshot, err := publishSnapshot(context.Background(), root, "pinned-task", "snapshots")
	if err != nil {
		t.Fatal(err)
	}
	// Advance the remote branch to a descendant with different content.
	clone := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, root, "clone", "-q", "--branch", "project/pinned-task", remote, clone)
	writeFixture(t, clone, "tracked.txt", "later content\n")
	runGitTest(t, clone, "add", ".")
	runGitTest(t, clone, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "Advance")
	runGitTest(t, clone, "push", "-q", "origin", "HEAD:"+snapshot.Ref)
	command, err := snapshot.Wrap("cat tracked.txt; exit 17")
	if err != nil {
		t.Fatal(err)
	}
	output, err := executeWrapper(t, command)
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 17 {
		t.Fatalf("exit was lost: %v\n%s", err, output)
	}
	if strings.TrimSpace(output) != "original" {
		t.Fatalf("ran branch tip instead of pinned commit: %s", output)
	}
	snapshot.Commit = strings.Repeat("a", 40)
	command, err = snapshot.Wrap("echo should-not-run")
	if err != nil {
		t.Fatal(err)
	}
	output, err = executeWrapper(t, command)
	if err == nil || strings.Contains(output, "should-not-run") {
		t.Fatalf("missing commit ran payload: %v\n%s", err, output)
	}
}

func TestGitPreparationCancellation(t *testing.T) {
	root, remote := fixture(t)
	marker := filepath.Join(t.TempDir(), "receiving")
	hook := "#!/bin/sh\nprintf ready > " + Quote(marker) + "\nsleep 60\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0755); err != nil {
		t.Fatal(err)
	}
	before := repoState(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := publishSnapshot(ctx, root, "cancelled-task", "snapshots"); done <- err }()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
ready:
	for {
		select {
		case err := <-done:
			t.Fatalf("preparation exited before cancellation: %v", err)
		case <-deadline:
			cancel()
			<-done
			t.Fatal("push did not reach receiver")
		case <-ticker.C:
			if _, err := os.Stat(marker); err == nil {
				break ready
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancelled Git operation did not exit")
	}
	if !reflect.DeepEqual(before, repoState(t, root)) {
		t.Fatal("cancelled preparation changed local repository")
	}
	if ref := runGitTest(t, remote, "for-each-ref", "--format=%(refname)", "refs/heads/project/cancelled-task"); ref != "" {
		t.Fatalf("cancelled push published %s", ref)
	}
}
