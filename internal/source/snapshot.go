package source

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5"
)

type SnapshotResult struct {
	Commit string // Full commit SHA
	Ref    string // refs/nextask/<taskID>
}

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

	// If clean, use HEAD commit directly
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

	// Create snapshot commit using git CLI with temporary index
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
	// Create temporary index file
	tmpIndex, err := os.CreateTemp("", "nextask-index-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp index: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	tmpIndex.Close()
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

	// 1. Add all files to temp index (respects .gitignore)
	_, err = runGit(true, "add", "-A")
	if err != nil {
		return "", fmt.Errorf("failed to add files: %w", err)
	}

	// 2. Write tree from temp index
	treeHash, err := runGit(true, "write-tree")
	if err != nil {
		return "", fmt.Errorf("failed to write tree: %w", err)
	}

	// 3. Get HEAD commit as parent
	headCommit, err := runGit(false, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	// 4. Get author info from HEAD commit
	// TODO: use nextask config when available
	author, err := runGit(false, "log", "-1", "--format=%an")
	if err != nil {
		return "", fmt.Errorf("failed to get author: %w", err)
	}
	email, err := runGit(false, "log", "-1", "--format=%ae")
	if err != nil {
		return "", fmt.Errorf("failed to get email: %w", err)
	}

	// 5. Create commit object (writes to .git/objects only, does NOT update HEAD)
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

func PushSnapshot(repoPath, remoteName string, result *SnapshotResult) error {
	// Push commit to remote at refs/nextask/<taskID>
	// Format: git push <remote> <commit>:<ref>
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
