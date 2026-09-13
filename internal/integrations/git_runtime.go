package integrations

import (
	"context"
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/taskexec"
)

// RuntimeOptions describe a published snapshot, separate from enqueue options.
func (Git) RuntimeOptions() Schema {
	return Schema{
		"remote":   {Kind: String, Default: "", Check: checkGitRemote},
		"endpoint": {Kind: String, Default: "", Check: checkGitRemote},
		"ref":      {Kind: String, Default: ""},
		"commit":   {Kind: String, Default: ""},
	}
}

func (Git) Run(ctx context.Context, task Task, options Options, streams IO) *taskexec.Result {
	snapshot := GitSnapshot{
		Remote: options.String("remote"), Endpoint: options.String("endpoint"),
		Ref: options.String("ref"), Commit: options.String("commit"),
	}
	if err := snapshot.restore(ctx); err != nil {
		return &taskexec.Result{Code: 1, Err: err}
	}
	return taskexec.Run(ctx, taskexec.Command{
		Text: gitRoutingReset + "\nexec sh -c " + Quote(task.Command),
		Env:  os.Environ(), CleanupTimeout: task.CleanupTimeout,
		Stdin: streams.In, Stdout: streams.Out, Stderr: streams.Err,
	})
}

func (s GitSnapshot) restore(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	connection := s.Endpoint
	if connection == "" {
		connection = s.Remote
	}
	c, err := resolveGitConnection(connection)
	if err != nil {
		return err
	}
	if c.remote != s.Remote {
		return fmt.Errorf("Git endpoint resolves to a different repository than the queued snapshot")
	}
	format := "sha1"
	if len(s.Commit) == 64 {
		format = "sha256"
	}
	for _, command := range [][]string{
		{"init", "--quiet", "--template=", "--object-format=" + format, "."},
		{"config", "--local", "remote.origin.url", s.Remote},
	} {
		if _, err := gitCommand(ctx, ".", nil, nil, command...); err != nil {
			return err
		}
	}
	if _, err := gitCommand(ctx, ".", c.env, nil, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--", s.Remote, s.Ref); err != nil {
		return err
	}
	if _, err := gitCommand(ctx, ".", nil, nil, "cat-file", "-e", s.Commit+"^{commit}"); err != nil {
		return err
	}
	_, err = gitCommand(ctx, ".", nil, nil, "checkout", "--quiet", "--detach", "--force", s.Commit)
	return err
}
