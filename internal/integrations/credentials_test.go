package integrations

import (
	"context"
	"strings"
	"testing"
)

func TestRemoteCredentials(t *testing.T) {
	for _, remote := range []string{
		"https://user:secret-value@host/repo", "https://secret-value@host/repo",
		"https://host/repo?access_token=secret-value", "https://host/repo#secret-value",
		"ssh://git:secret-value@host/repo", "https://user:secret-value@host%ZZ/repo",
	} {
		for _, mode := range []string{"config", "override"} {
			var err error
			if mode == "config" {
				_, err = Builtins().Configure(map[string]map[string]any{"git": {"remote": remote}})
			} else {
				_, err = Builtins().Resolve([]string{"git"}, nil, []string{"git.remote=" + remote})
			}
			if err == nil || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("%s accepted or exposed credentials: %v", mode, err)
			}
		}
	}
	for _, remote := range []string{"origin", "/tmp/repo.git", "git@host:repo.git", "ssh://git@host/repo.git", "https://host/repo.git"} {
		if err := (Git{}).Validate(Options{"remote": remote}); err != nil {
			t.Fatalf("safe remote rejected: %v", err)
		}
	}
}

func TestNamedRemoteRejectsCredentials(t *testing.T) {
	for _, pushOnly := range []bool{false, true} {
		root, _ := fixture(t)
		args := []string{"remote", "set-url"}
		if pushOnly {
			args = append(args, "--push")
		}
		runGitTest(t, root, append(args, "snapshots", "https://secret-value@host.invalid/repo.git")...)
		_, err := publishSnapshot(context.Background(), root, "task", "snapshots")
		if err == nil || !strings.Contains(err.Error(), "credential helper") || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("resolved remote accepted or exposed credentials: %v", err)
		}
	}
}
