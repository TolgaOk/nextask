package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Git captures source at enqueue and wraps execution with an exact checkout.
type Git struct{ Repo string }

func (Git) Options() Schema {
	return Schema{"remote": {Kind: String, Default: "", Check: checkGitRemote}}
}
func (Git) Validate(options Options) error {
	if _, err := (Git{}).Options().Resolve(options); err != nil {
		return err
	}
	if strings.TrimSpace(options.String("remote")) == "" {
		return fmt.Errorf("remote is required: set integrations.git.remote or --set git.remote=REMOTE")
	}
	if strings.ContainsRune(options.String("remote"), 0) {
		return fmt.Errorf("remote contains a NUL byte")
	}
	return nil
}
func (g Git) Prepare(ctx context.Context, task Task, options Options) (Task, error) {
	snapshot, err := publishSnapshot(ctx, g.Repo, task.ID, options.String("remote"))
	if err != nil {
		return Task{}, err
	}
	task.Command, err = snapshot.wrap(task)
	return task, err
}

// GitSnapshot describes published content. The commit, rather than the branch
// tip at execution time, determines the files the task receives.
type GitSnapshot struct {
	Remote   string `json:"remote"`
	Endpoint string `json:"endpoint,omitempty"`
	Ref      string `json:"ref"`
	Commit   string `json:"commit"`
}

var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

const gitRoutingReset = "unset GIT_DIR GIT_COMMON_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES"

func (s GitSnapshot) validate() error {
	if err := checkGitRemote(s.Remote); err != nil {
		return err
	}
	if err := checkGitRemote(s.Endpoint); err != nil {
		return err
	}
	if s.Remote == "" || s.Ref == "" {
		return fmt.Errorf("git snapshot requires a remote and ref")
	}
	if !commitPattern.MatchString(s.Commit) {
		return fmt.Errorf("git snapshot requires an exact commit hash; re-enqueue legacy tasks that have no recorded commit")
	}
	if strings.ContainsRune(s.Remote+s.Ref, 0) {
		return fmt.Errorf("git snapshot contains a NUL byte")
	}
	return nil
}

func (s GitSnapshot) Wrap(command string) (string, error) {
	return s.wrap(Task{Command: command})
}

func (s GitSnapshot) wrap(task Task) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	command := task.Command
	if strings.ContainsRune(command, 0) {
		return "", fmt.Errorf("git command contains a NUL byte")
	}
	if s.Endpoint != "" {
		return runtimeCommand("git", task, Options{"remote": s.Remote, "endpoint": s.Endpoint, "ref": s.Ref, "commit": s.Commit})
	}
	// All operations happen in the task directory. Clear inherited repository
	// routing, scope setup options to a subshell, then exec the original command.
	format := "sha1"
	if len(s.Commit) == 64 {
		format = "sha256"
	}
	setup := gitRoutingReset + `
(
set -eu
export GIT_TERMINAL_PROMPT=0
command -v git >/dev/null 2>&1 || { printf '%s\n' 'nextask: git integration requires git on the worker' >&2; exit 127; }
git -c core.hooksPath=/dev/null init --quiet --template= --object-format=` + format + ` .
git -c core.hooksPath=/dev/null config --local remote.origin.url ` + Quote(s.Remote) + `
git -c core.hooksPath=/dev/null fetch --quiet --no-tags --no-recurse-submodules -- ` + Quote(s.Remote) + ` ` + Quote(s.Ref) + `
git -c core.hooksPath=/dev/null cat-file -e ` + Quote(s.Commit+"^{commit}") + `
git -c core.hooksPath=/dev/null checkout --quiet --detach --force ` + Quote(s.Commit) + `
) && exec sh -c ` + Quote(command)
	return setup, nil
}

// LegacyCommand converts pre-integration source descriptors into the same shell
// wrapper used by new tasks. It performs no Git operations in the worker itself.
func LegacyCommand(kind string, raw json.RawMessage, command string) (string, error) {
	switch kind {
	case "", "noop":
		return command, nil
	case "git":
		var snapshot GitSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return "", fmt.Errorf("invalid legacy git source: %w", err)
		}
		return snapshot.Wrap(command)
	default:
		return "", fmt.Errorf("unknown source type: %s", kind)
	}
}
