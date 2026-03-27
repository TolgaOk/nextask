// Package source provides git-based source code snapshotting for task reproducibility.
package source

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SnapshotResult contains the commit hash and ref of a created snapshot.
type SnapshotResult struct {
	Commit string
	Ref    string
}

// CreateSnapshot creates a git commit capturing the current working tree state,
// including uncommitted changes, without modifying the repository's HEAD.
func CreateSnapshot(repoPath, taskID string) (*SnapshotResult, error) {
	runGit := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("git %s: %s", args[0], string(exitErr.Stderr))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	toplevel, err := runGit("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	project := filepath.Base(toplevel)

	gitDir, err := runGit("rev-parse", "--git-dir")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoPath, gitDir)
	}

	treeHash, err := buildTree(repoPath)
	if err != nil {
		return nil, err
	}

	headTree, _ := runGit("rev-parse", "HEAD^{tree}")
	if treeHash == headTree {
		headCommit, err := runGit("rev-parse", "HEAD")
		if err != nil {
			return nil, fmt.Errorf("failed to get HEAD: %w", err)
		}
		return &SnapshotResult{
			Commit: headCommit,
			Ref:    "refs/heads/" + project + "/" + taskID,
		}, nil
	}

	ref := "refs/heads/" + project + "/" + taskID

	cached, err := readCache(gitDir)
	if err == nil && cached.TreeHash == treeHash {
		return &SnapshotResult{
			Commit: cached.CommitHash,
			Ref:    ref,
		}, nil
	}

	commitHash, err := createCommit(repoPath, treeHash, taskID)
	if err != nil {
		return nil, err
	}

	result := &SnapshotResult{Commit: commitHash, Ref: ref}

	writeCache(gitDir, snapshotCache{
		TreeHash:   treeHash,
		CommitHash: commitHash,
		Ref:        ref,
	})

	return result, nil
}

func buildTree(repoPath string) (string, error) {
	tmpIndex, err := os.CreateTemp("", "nextask-index-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp index: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	tmpIndex.Close()
	os.Remove(tmpIndexPath)
	defer os.Remove(tmpIndexPath)

	runWithIndex := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tmpIndexPath)
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("git %s: %s", args[0], string(exitErr.Stderr))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	if _, err = runWithIndex("add", "-A"); err != nil {
		return "", fmt.Errorf("failed to add files: %w", err)
	}

	treeHash, err := runWithIndex("write-tree")
	if err != nil {
		return "", fmt.Errorf("failed to write tree: %w", err)
	}

	return treeHash, nil
}

func createCommit(repoPath, treeHash, taskID string) (string, error) {
	runGit := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("git %s: %s", args[0], string(exitErr.Stderr))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	headCommit, err := runGit("rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(err.Error(), "unknown revision") {
			return "", fmt.Errorf("repository has no commits - at least one commit is required")
		}
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	author, err := runGit("log", "-1", "--format=%an")
	if err != nil {
		return "", fmt.Errorf("failed to get author: %w", err)
	}
	email, err := runGit("log", "-1", "--format=%ae")
	if err != nil {
		return "", fmt.Errorf("failed to get email: %w", err)
	}

	cmd := exec.Command("git", "commit-tree", treeHash, "-p", headCommit, "-m", "nextask: "+taskID)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+author,
		"GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+author,
		"GIT_COMMITTER_EMAIL="+email,
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git commit-tree: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("failed to create commit: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// --- snapshot cache ---

const cacheFile = "nextask/snapshot-cache"

type snapshotCache struct {
	TreeHash   string
	CommitHash string
	Ref        string
}

func readCache(gitDir string) (snapshotCache, error) {
	f, err := os.Open(filepath.Join(gitDir, cacheFile))
	if err != nil {
		return snapshotCache{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return snapshotCache{}, fmt.Errorf("empty cache")
	}
	parts := strings.Fields(scanner.Text())
	if len(parts) != 3 {
		return snapshotCache{}, fmt.Errorf("invalid cache")
	}
	return snapshotCache{
		TreeHash:   parts[0],
		CommitHash: parts[1],
		Ref:        parts[2],
	}, nil
}

func writeCache(gitDir string, c snapshotCache) {
	dir := filepath.Join(gitDir, "nextask")
	os.MkdirAll(dir, 0755)
	data := fmt.Sprintf("%s %s %s\n", c.TreeHash, c.CommitHash, c.Ref)
	os.WriteFile(filepath.Join(gitDir, cacheFile), []byte(data), 0644)
}

// ResolveRemote resolves a git remote name (e.g. "origin") to its URL.
// If the value is already a URL or path, it is returned unchanged.
func ResolveRemote(repoPath, remote string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return remote, nil
	}
	return strings.TrimSpace(string(out)), nil
}

// PushSnapshot pushes a snapshot commit to a remote repository.
func PushSnapshot(repoPath, remoteName string, result *SnapshotResult) error {
	refSpec := result.Commit + ":" + result.Ref

	cmd := exec.Command("git", "push", remoteName, refSpec)
	cmd.Dir = repoPath
	if _, err := cmd.Output(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("git push: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}

// DeleteSnapshot removes a snapshot ref from a git remote.
func DeleteSnapshot(remote, ref string) error {
	cmd := exec.Command("git", "push", remote, "--delete", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push --delete: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// FetchSnapshot clones a repository and checks out a specific snapshot ref.
func FetchSnapshot(ctx context.Context, remote, ref, taskDir string) (commit string, err error) {
	defer func() {
		if err != nil {
			os.RemoveAll(taskDir)
		}
	}()

	runGit := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = taskDir
		out, err := cmd.Output()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("git %s: %s", args[0], string(exitErr.Stderr))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	branch := strings.TrimPrefix(ref, "refs/heads/")

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--single-branch", "--no-local", "--branch", branch, remote, taskDir)
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git clone: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("failed to clone: %w", err)
	}

	commit, err = runGit("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}

	return commit, nil
}
