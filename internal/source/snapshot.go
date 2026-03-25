// Package source provides git-based source code snapshotting for task reproducibility.
package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
)

// SnapshotResult contains the commit hash and ref of a created snapshot.
type SnapshotResult struct {
	Commit string
	Ref    string
}

// CreateSnapshot creates a git commit capturing the current working tree state,
// including uncommitted changes, without modifying the repository's HEAD.
func CreateSnapshot(repoPath, taskID string) (*SnapshotResult, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	toplevel, err := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get repo root: %w", err)
	}
	project := filepath.Base(strings.TrimSpace(string(toplevel)))

	ref := "refs/heads/" + project + "/" + taskID

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	if status.IsClean() {
		head, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("failed to get HEAD: %w", err)
		}
		return &SnapshotResult{
			Commit: head.Hash().String(),
			Ref:    ref,
		}, nil
	}

	commitHash, err := createSnapshotCommit(repoPath, taskID)
	if err != nil {
		return nil, err
	}

	return &SnapshotResult{
		Commit: commitHash,
		Ref:    ref,
	}, nil
}

func createSnapshotCommit(repoPath, taskID string) (string, error) {
	tmpIndex, err := os.CreateTemp("", "nextask-index-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp index: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	tmpIndex.Close()
	os.Remove(tmpIndexPath)
	defer os.Remove(tmpIndexPath)

	runGit := func(useTempIndex bool, args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		if useTempIndex {
			cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tmpIndexPath)
		}
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("git %s: %s", args[0], string(exitErr.Stderr))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	_, err = runGit(true, "add", "-A")
	if err != nil {
		return "", fmt.Errorf("failed to add files: %w", err)
	}

	treeHash, err := runGit(true, "write-tree")
	if err != nil {
		return "", fmt.Errorf("failed to write tree: %w", err)
	}

	headCommit, err := runGit(false, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(err.Error(), "unknown revision") {
			return "", fmt.Errorf("repository has no commits - at least one commit is required")
		}
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	// TODO: use nextask config when available
	author, err := runGit(false, "log", "-1", "--format=%an")
	if err != nil {
		return "", fmt.Errorf("failed to get author: %w", err)
	}
	email, err := runGit(false, "log", "-1", "--format=%ae")
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

// ResolveRemote resolves a git remote name (e.g. "origin") to its URL.
// If the value is already a URL or path, it is returned unchanged.
func ResolveRemote(repoPath, remote string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		// Not a remote name — already a URL or path
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
