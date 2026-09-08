package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/spf13/cobra"
)

func findCommand(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	cmd, rest, err := root.Find(path)
	if err != nil || len(rest) != 0 {
		t.Fatalf("find %v: %v (%v)", path, err, rest)
	}
	return cmd
}

func TestCommandFlagIsolation(t *testing.T) {
	first, second := NewRootCommand("first"), NewRootCommand("second")
	for _, tc := range []struct {
		path        []string
		flag, value string
	}{
		{[]string{"enqueue"}, "tag", "batch=one"},
		{[]string{"enqueue"}, "attach", "true"},
		{[]string{"enqueue"}, "snapshot", "true"},
		{[]string{"enqueue"}, "remote", "snapshots"},
		{[]string{"enqueue"}, "with", "git"},
		{[]string{"enqueue"}, "set", "git.remote=snapshots"},
		{[]string{"enqueue"}, "id", "first-id"},
		{[]string{"wait"}, "any", "true"},
		{[]string{"wait"}, "timeout", "2s"},
		{[]string{"wait"}, "tag", "batch=one"},
		{[]string{"list"}, "status", "failed"},
		{[]string{"list"}, "limit", "3"},
		{[]string{"list"}, "json", "true"},
		{[]string{"log"}, "stream", "stderr"},
		{[]string{"log"}, "tail", "7"},
		{[]string{"cancel"}, "timeout", "3s"},
		{[]string{"worker"}, "workdir", "/tmp/isolated-worker"},
		{[]string{"worker"}, "once", "true"},
		{[]string{"worker"}, "filter", "batch=one"},
		{[]string{"worker", "list"}, "limit", "4"},
		{[]string{"worker", "list"}, "status", "stale"},
		{[]string{"worker", "stop"}, "timeout", "4s"},
	} {
		t.Run(strings.Join(tc.path, "-")+"-"+tc.flag, func(t *testing.T) {
			a, b := findCommand(t, first, tc.path...), findCommand(t, second, tc.path...)
			before := b.Flags().Lookup(tc.flag).Value.String()
			if err := a.Flags().Set(tc.flag, tc.value); err != nil {
				t.Fatal(err)
			}
			other := b.Flags().Lookup(tc.flag)
			if other.Changed || other.Value.String() != before {
				t.Fatalf("flag leaked into another tree: %s", other.Value.String())
			}
			// Constructing a third tree must not reset flags already parsed on the first.
			after := a.Flags().Lookup(tc.flag).Value.String()
			NewRootCommand("third")
			if a.Flags().Lookup(tc.flag).Value.String() != after {
				t.Fatal("construction reset an existing command")
			}
		})
	}
	if first.Version != "first" || second.Version != "second" {
		t.Fatal("version leaked across command trees")
	}
	if err := findCommand(t, second, "wait").Args(findCommand(t, second, "wait"), []string{"task-id"}); err != nil {
		t.Fatalf("other tree's tag flag changed validation: %v", err)
	}
}

func TestConcurrentConfigIsolation(t *testing.T) {
	const count = 8
	ready := make(chan struct{}, count)
	release := make(chan struct{})
	type result struct {
		index  int
		output string
		err    error
	}
	results := make(chan result, count)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := range count {
		settings := &config.Config{
			DB:           config.DBConfig{URL: fmt.Sprintf("postgres://localhost/database-%d", i)},
			Worker:       config.WorkerConfig{Workdir: fmt.Sprintf("/tmp/worker-%d", i)},
			Integrations: map[string]map[string]any{"git": {"remote": fmt.Sprintf("remote-%d", i)}},
		}
		root := newRootCommand(fmt.Sprint(i), func() (*config.Config, error) { return settings, nil })
		show := findCommand(t, root, "config", "show")
		run := show.RunE
		show.RunE = func(cmd *cobra.Command, args []string) error {
			ready <- struct{}{}
			select {
			case <-release:
				return run(cmd, args)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetArgs([]string{"config", "show"})
		go func() {
			err := root.ExecuteContext(ctx)
			results <- result{i, output.String(), err}
		}()
	}
	// Every tree has loaded its configuration before any tree reads it back.
	for range count {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("commands did not reach their handlers")
		}
	}
	close(release)
	for range count {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		for _, want := range []string{fmt.Sprintf("database-%d", got.index), fmt.Sprintf("/tmp/worker-%d", got.index), fmt.Sprintf("remote-%d", got.index)} {
			if !strings.Contains(got.output, want) {
				t.Fatalf("configuration crossed command trees, missing %s: %s", want, got.output)
			}
		}
	}
}

func TestRootConfigLoading(t *testing.T) {
	failure := errors.New("test config failure")
	for _, args := range [][]string{{"--help"}, {"--version"}, {"enqueue", "--help"}, {"_run", "missing", "{}", "true", "0"}, {"config", "show"}} {
		t.Run(strings.Join(args[:1], ""), func(t *testing.T) {
			calls := 0
			root := newRootCommand("test-build", func() (*config.Config, error) { calls++; return nil, failure })
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(args)
			err := root.Execute()
			if args[0] == "config" {
				if calls != 1 || !errors.Is(err, failure) {
					t.Fatalf("config error lost: calls=%d, err=%v", calls, err)
				}
			} else {
				if calls != 0 {
					t.Fatalf("help/version/runtime unexpectedly loaded project config: %v", args)
				}
				if args[0] == "_run" {
					if err == nil || !strings.Contains(err.Error(), "unknown runtime integration") {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWaitCommandIsolation(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	createWatchTask(t, pool, "isolated-finished", nil)
	createWatchTask(t, pool, "isolated-pending", nil)
	completeWatchTask(t, pool, "isolated-finished", 0, false)
	any := newWaitCommand(&cfg)
	all := newWaitCommand(&cfg)
	any.SetArgs([]string{"isolated-pending", "isolated-finished", "--any", "--timeout", "2s"})
	all.SetArgs([]string{"isolated-pending", "isolated-finished", "--timeout", "200ms"})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	anyDone, allDone := make(chan error, 1), make(chan error, 1)
	go func() { anyDone <- any.ExecuteContext(ctx) }()
	go func() { allDone <- all.ExecuteContext(ctx) }()
	if err := <-anyDone; err != nil {
		t.Fatalf("any inherited another command's policy: %v", err)
	}
	var exit *exitCodeError
	if err := <-allDone; !errors.As(err, &exit) || exit.code != 124 {
		t.Fatalf("all inherited another command's policy: %v", err)
	}
}

func TestWorkerConfigIsolation(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	cfg.Worker.Workdir = t.TempDir()
	override := t.TempDir()
	commands := []*cobra.Command{newWorkerCommand(&cfg), newWorkerCommand(&cfg)}
	commands[0].SetArgs([]string{"--once", "--_id", "isolated-override", "--workdir", override})
	commands[1].SetArgs([]string{"--once", "--_id", "isolated-default"})
	results := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, cmd := range commands {
		go func() { results <- cmd.ExecuteContext(ctx) }()
	}
	for range commands {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{"isolated-override": override, "isolated-default": cfg.Worker.Workdir} {
		var workdir string
		if err := pool.QueryRow(ctx, "SELECT workdir FROM workers WHERE id = $1", id).Scan(&workdir); err != nil {
			t.Fatal(err)
		}
		if workdir != want {
			t.Fatalf("%s workdir = %s, want %s", id, workdir, want)
		}
	}
	if cfg.Worker.Workdir == override {
		t.Fatal("worker override changed its input configuration")
	}
}
