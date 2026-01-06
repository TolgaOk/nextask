// Package source provides git-based source code snapshotting for task reproducibility.
package source

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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

	ref := "refs/nextask/" + taskID

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

// PushSnapshot pushes a snapshot commit to a remote repository.
func PushSnapshot(repoPath, remoteName string, result *SnapshotResult) error {
	refSpec := result.Commit + ":" + result.Ref

	cmd := exec.Command("git", "push", remoteName, refSpec)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("git push: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}

// DeleteSnapshot removes a snapshot ref from a bare repository.
func DeleteSnapshot(remote, ref string) error {
	repo, err := git.PlainOpen(remote)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	return repo.Storer.RemoveReference(plumbing.ReferenceName(ref))
}

// FetchSnapshot clones a repository and checks out a specific snapshot ref.
func FetchSnapshot(remote, ref, taskDir string) (commit string, err error) {
	defer func() {
		if err != nil {
			os.RemoveAll(taskDir)
		}
	}()

	runGit := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = taskDir
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("git %s: %s", args[0], string(exitErr.Stderr))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	cmd := exec.Command("git", "clone", remote, taskDir)
	if err = cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git clone: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("failed to clone: %w", err)
	}

	if _, err = runGit("fetch", "origin", ref); err != nil {
		return "", fmt.Errorf("failed to fetch ref: %w", err)
	}

	if _, err = runGit("checkout", "FETCH_HEAD"); err != nil {
		return "", fmt.Errorf("failed to checkout: %w", err)
	}

	commit, err = runGit("rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get commit: %w", err)
	}

	return commit, nil
}
