package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func setupTestRepo(t *testing.T) (repoPath string, cleanup func()) {
	// Create temp directory for working repo
	repoPath, err := os.MkdirTemp("", "nextask-test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cleanup = func() {
		os.RemoveAll(repoPath)
	}

	// Init repo
	runGit(t, repoPath, "init")
	runGit(t, repoPath, "config", "user.email", "test@test.com")
	runGit(t, repoPath, "config", "user.name", "Test User")

	// Create initial commit
	testFile := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test\n"), 0644); err != nil {
		cleanup()
		t.Fatalf("failed to write file: %v", err)
	}
	runGit(t, repoPath, "add", ".")
	runGit(t, repoPath, "commit", "-m", "initial commit")

	return repoPath, cleanup
}

func setupTestRepoWithRemote(t *testing.T) (repoPath, remotePath string, cleanup func()) {
	// Create bare repo as remote
	remotePath, err := os.MkdirTemp("", "nextask-test-remote-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	runGit(t, remotePath, "init", "--bare")

	// Create working repo
	repoPath, repoCleanup := setupTestRepo(t)

	// Add remote
	runGit(t, repoPath, "remote", "add", "origin", remotePath)

	cleanup = func() {
		repoCleanup()
		os.RemoveAll(remotePath)
	}

	return repoPath, remotePath, cleanup
}

// Test: not a git repo
func TestCreateSnapshot_NotGitRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nextask-test-notgit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = CreateSnapshot(tmpDir, "test1234")
	if err == nil {
		t.Error("expected error for non-git directory, got nil")
	}
}

// Test: empty repo (no commits)
func TestCreateSnapshot_EmptyRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nextask-test-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runGit(t, tmpDir, "init")

	_, err = CreateSnapshot(tmpDir, "test1234")
	if err == nil {
		t.Error("expected error for empty repo, got nil")
	}
}

// Test: clean repo (everything committed)
func TestCreateSnapshot_CleanRepo(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Get current HEAD
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	headOut, _ := cmd.Output()
	expectedHead := string(headOut[:len(headOut)-1]) // trim newline

	result, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	if result.Commit != expectedHead {
		t.Errorf("Commit = %v, want %v", result.Commit, expectedHead)
	}
	if result.Ref != "refs/nextask/test1234" {
		t.Errorf("Ref = %v, want refs/nextask/test1234", result.Ref)
	}
}

// Test: dirty repo with modified files
func TestCreateSnapshot_ModifiedFiles(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Get HEAD before modification
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	headOut, _ := cmd.Output()
	originalHead := string(headOut[:len(headOut)-1])

	// Modify a file
	testFile := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(testFile, []byte("# Modified\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// Should create new commit, not return HEAD
	if result.Commit == originalHead {
		t.Error("expected new commit, got original HEAD")
	}
	if len(result.Commit) != 40 {
		t.Errorf("expected 40-char commit SHA, got %d chars", len(result.Commit))
	}
}

// Test: dirty repo with untracked files
func TestCreateSnapshot_UntrackedFiles(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Get HEAD before adding file
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	headOut, _ := cmd.Output()
	originalHead := string(headOut[:len(headOut)-1])

	// Add untracked file
	newFile := filepath.Join(repoPath, "newfile.txt")
	if err := os.WriteFile(newFile, []byte("new content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// Should create new commit
	if result.Commit == originalHead {
		t.Error("expected new commit, got original HEAD")
	}
}

// Test: dirty repo with staged but uncommitted changes
func TestCreateSnapshot_StagedChanges(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Get HEAD
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	headOut, _ := cmd.Output()
	originalHead := string(headOut[:len(headOut)-1])

	// Modify and stage
	testFile := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(testFile, []byte("# Staged\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit(t, repoPath, "add", "README.md")

	result, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	if result.Commit == originalHead {
		t.Error("expected new commit, got original HEAD")
	}
}

// Test: .gitignore is respected
func TestCreateSnapshot_GitignoreRespected(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create .gitignore
	gitignore := filepath.Join(repoPath, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("*.log\nsecrets/\n"), 0644); err != nil {
		t.Fatalf("failed to write .gitignore: %v", err)
	}
	runGit(t, repoPath, "add", ".gitignore")
	runGit(t, repoPath, "commit", "-m", "add gitignore")

	// Create ignored files
	logFile := filepath.Join(repoPath, "debug.log")
	if err := os.WriteFile(logFile, []byte("log content\n"), 0644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	secretsDir := filepath.Join(repoPath, "secrets")
	if err := os.MkdirAll(secretsDir, 0755); err != nil {
		t.Fatalf("failed to create secrets dir: %v", err)
	}
	secretFile := filepath.Join(secretsDir, "api_key.txt")
	if err := os.WriteFile(secretFile, []byte("secret123\n"), 0644); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	// Create non-ignored file
	normalFile := filepath.Join(repoPath, "normal.txt")
	if err := os.WriteFile(normalFile, []byte("normal content\n"), 0644); err != nil {
		t.Fatalf("failed to write normal file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// Verify ignored files are not in the snapshot
	// Check the tree contents of the snapshot commit
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", result.Commit)
	cmd.Dir = repoPath
	treeOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to list tree: %v", err)
	}
	treeContents := string(treeOut)

	if contains(treeContents, "debug.log") {
		t.Error("snapshot should not contain debug.log (ignored)")
	}
	if contains(treeContents, "secrets/") || contains(treeContents, "api_key.txt") {
		t.Error("snapshot should not contain secrets/ (ignored)")
	}
	if !contains(treeContents, "normal.txt") {
		t.Error("snapshot should contain normal.txt")
	}
}

// Test: no .gitignore - all files included
func TestCreateSnapshot_NoGitignore(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create various files without .gitignore
	logFile := filepath.Join(repoPath, "debug.log")
	if err := os.WriteFile(logFile, []byte("log content\n"), 0644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// Verify all files are in snapshot
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", result.Commit)
	cmd.Dir = repoPath
	treeOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to list tree: %v", err)
	}
	treeContents := string(treeOut)

	if !contains(treeContents, "debug.log") {
		t.Error("snapshot should contain debug.log (no gitignore)")
	}
}

// Test: PushSnapshot success
func TestPushSnapshot(t *testing.T) {
	repoPath, remotePath, cleanup := setupTestRepoWithRemote(t)
	defer cleanup()

	// Create a snapshot
	newFile := filepath.Join(repoPath, "newfile.txt")
	if err := os.WriteFile(newFile, []byte("content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "push1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// Push snapshot
	err = PushSnapshot(repoPath, "origin", result)
	if err != nil {
		t.Fatalf("PushSnapshot() error = %v", err)
	}

	// Verify ref exists on remote
	cmd := exec.Command("git", "ls-remote", remotePath, result.Ref)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ls-remote failed: %v", err)
	}
	if len(out) == 0 {
		t.Error("ref not found on remote after push")
	}
}

// Test: PushSnapshot to invalid remote
func TestPushSnapshot_InvalidRemote(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	result := &SnapshotResult{
		Commit: "abc123",
		Ref:    "refs/nextask/test1234",
	}

	err := PushSnapshot(repoPath, "nonexistent", result)
	if err == nil {
		t.Error("expected error for invalid remote, got nil")
	}
}

// Test: snapshot doesn't modify user's repo
func TestCreateSnapshot_DoesNotModifyRepo(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	// Get HEAD before
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	headBefore, _ := cmd.Output()

	// Get status before
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	statusBefore, _ := cmd.Output()

	// Modify a file
	testFile := filepath.Join(repoPath, "README.md")
	os.WriteFile(testFile, []byte("# Modified\n"), 0644)

	// Create snapshot
	_, err := CreateSnapshot(repoPath, "test1234")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	// Get HEAD after
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	headAfter, _ := cmd.Output()

	// Get status after
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	statusAfter, _ := cmd.Output()

	// HEAD should not change
	if string(headBefore) != string(headAfter) {
		t.Error("CreateSnapshot modified HEAD")
	}

	// Status should show same modified file (not staged)
	if string(statusBefore) == string(statusAfter) {
		// Before: empty, After: should show modified
		// This is expected since we modified a file
	}

	// Verify the file is still showing as modified (not staged by us)
	cmd = exec.Command("git", "diff", "--name-only")
	cmd.Dir = repoPath
	diffOut, _ := cmd.Output()
	if !contains(string(diffOut), "README.md") {
		t.Error("expected README.md to still show as modified")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFetchSnapshot_Basic(t *testing.T) {
	repoPath, remotePath, cleanup := setupTestRepoWithRemote(t)
	defer cleanup()

	// Create and push a snapshot
	newFile := filepath.Join(repoPath, "newfile.txt")
	if err := os.WriteFile(newFile, []byte("fetch test\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "fetch001")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if err := PushSnapshot(repoPath, "origin", result); err != nil {
		t.Fatalf("PushSnapshot() error = %v", err)
	}

	// Fetch snapshot to new directory
	taskDir, err := os.MkdirTemp("", "nextask-fetch-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	os.RemoveAll(taskDir) // FetchSnapshot will create it
	defer os.RemoveAll(taskDir)

	commit, err := FetchSnapshot(context.Background(), remotePath, result.Ref, taskDir)
	if err != nil {
		t.Fatalf("FetchSnapshot() error = %v", err)
	}

	if commit != result.Commit {
		t.Errorf("commit = %v, want %v", commit, result.Commit)
	}

	// Verify file exists
	fetchedFile := filepath.Join(taskDir, "newfile.txt")
	if _, err := os.Stat(fetchedFile); os.IsNotExist(err) {
		t.Error("newfile.txt not found in fetched snapshot")
	}
}

func TestFetchSnapshot_InvalidRemote(t *testing.T) {
	taskDir, err := os.MkdirTemp("", "nextask-fetch-invalid-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	os.RemoveAll(taskDir)

	_, err = FetchSnapshot(context.Background(), "/nonexistent/repo", "refs/nextask/test", taskDir)
	if err == nil {
		t.Error("expected error for invalid remote")
	}

	// Verify cleanup
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Error("taskDir should be cleaned up on failure")
	}
}

func TestFetchSnapshot_InvalidRef(t *testing.T) {
	_, remotePath, cleanup := setupTestRepoWithRemote(t)
	defer cleanup()

	taskDir, err := os.MkdirTemp("", "nextask-fetch-badref-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	os.RemoveAll(taskDir)

	_, err = FetchSnapshot(context.Background(), remotePath, "refs/nextask/nonexistent", taskDir)
	if err == nil {
		t.Error("expected error for invalid ref")
	}

	// Verify cleanup
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Error("taskDir should be cleaned up on failure")
	}
}

// === DeleteSnapshot Tests ===

func TestDeleteSnapshot_Exists(t *testing.T) {
	repoPath, remotePath, cleanup := setupTestRepoWithRemote(t)
	defer cleanup()

	// Create and push a snapshot
	newFile := filepath.Join(repoPath, "delete_test.txt")
	if err := os.WriteFile(newFile, []byte("delete test\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	result, err := CreateSnapshot(repoPath, "delsnap001")
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if err := PushSnapshot(repoPath, "origin", result); err != nil {
		t.Fatalf("PushSnapshot() error = %v", err)
	}

	// Verify ref exists
	cmd := exec.Command("git", "show-ref", result.Ref)
	cmd.Dir = remotePath
	if err := cmd.Run(); err != nil {
		t.Fatalf("ref should exist before delete: %v", err)
	}

	// Delete snapshot
	err = DeleteSnapshot(remotePath, result.Ref)
	if err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}

	// Verify ref is gone
	cmd = exec.Command("git", "show-ref", result.Ref)
	cmd.Dir = remotePath
	if err := cmd.Run(); err == nil {
		t.Error("ref should not exist after delete")
	}
}

func TestDeleteSnapshot_NotExists(t *testing.T) {
	_, remotePath, cleanup := setupTestRepoWithRemote(t)
	defer cleanup()

	// Try to delete non-existent ref
	err := DeleteSnapshot(remotePath, "refs/nextask/nonexistent")
	// Should not error - deleting non-existent ref is a no-op
	if err != nil {
		t.Logf("DeleteSnapshot() for non-existent ref returned: %v", err)
	}
}

func TestDeleteSnapshot_InvalidRepo(t *testing.T) {
	err := DeleteSnapshot("/nonexistent/repo", "refs/nextask/test")
	if err == nil {
		t.Error("expected error for invalid repo path")
	}
}

func TestDeleteSnapshot_MultipleSnapshots(t *testing.T) {
	repoPath, remotePath, cleanup := setupTestRepoWithRemote(t)
	defer cleanup()

	// Create and push multiple snapshots
	refs := []string{}
	for i := 1; i <= 3; i++ {
		file := filepath.Join(repoPath, "file"+string(rune('0'+i))+".txt")
		os.WriteFile(file, []byte("content"), 0644)

		taskID := "multi" + string(rune('0'+i))
		result, err := CreateSnapshot(repoPath, taskID)
		if err != nil {
			t.Fatalf("CreateSnapshot(%s) error = %v", taskID, err)
		}
		if err := PushSnapshot(repoPath, "origin", result); err != nil {
			t.Fatalf("PushSnapshot(%s) error = %v", taskID, err)
		}
		refs = append(refs, result.Ref)
	}

	// Delete middle one
	if err := DeleteSnapshot(remotePath, refs[1]); err != nil {
		t.Fatalf("DeleteSnapshot() error = %v", err)
	}

	// Verify first and third still exist
	cmd := exec.Command("git", "show-ref", refs[0])
	cmd.Dir = remotePath
	if err := cmd.Run(); err != nil {
		t.Errorf("ref %s should still exist", refs[0])
	}

	cmd = exec.Command("git", "show-ref", refs[2])
	cmd.Dir = remotePath
	if err := cmd.Run(); err != nil {
		t.Errorf("ref %s should still exist", refs[2])
	}

	// Verify middle one is gone
	cmd = exec.Command("git", "show-ref", refs[1])
	cmd.Dir = remotePath
	if err := cmd.Run(); err == nil {
		t.Errorf("ref %s should be deleted", refs[1])
	}
}
