package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
)

func TestDurationFlags(t *testing.T) {
	for _, tc := range []struct {
		path []string
		flag string
	}{
		{[]string{"wait"}, "timeout"},
		{[]string{"cancel"}, "timeout"},
		{[]string{"worker"}, "timeout"},
		{[]string{"worker"}, "exit-if-idle"},
		{[]string{"worker", "stop"}, "timeout"},
		{[]string{"list"}, "since"},
		{[]string{"worker", "list"}, "since"},
	} {
		t.Run(strings.Join(tc.path, " ")+"/"+tc.flag, func(t *testing.T) {
			cmd := findCommand(t, NewRootCommand("test"), tc.path...)
			for raw, want := range map[string]time.Duration{"30s": 30 * time.Second, "1h30m": 90 * time.Minute, "7d": 7 * 24 * time.Hour} {
				if err := cmd.Flags().Set(tc.flag, raw); err != nil {
					t.Fatalf("%s rejected: %v", raw, err)
				}
				if got, err := cmd.Flags().GetDuration(tc.flag); err != nil || got != want {
					t.Fatalf("%s: got %v, want %v: %v", raw, got, want, err)
				}
			}
			if err := cmd.Flags().Set(tc.flag, "typo"); err == nil {
				t.Fatal("accepted malformed duration")
			}
		})
	}
}

func TestWorkerTagAliases(t *testing.T) {
	for _, flags := range [][]string{{"tag", "filter"}, {"filter", "tag"}} {
		cmd := newWorkerCommand(&config.Config{})
		if err := cmd.ParseFlags([]string{"--" + flags[0], `"batch=one,two"`, "--" + flags[1], `"owner=a""b"`}); err != nil {
			t.Fatal(err)
		}
		got, err := cmd.Flags().GetStringSlice("tag")
		if err != nil || !reflect.DeepEqual(got, []string{"batch=one,two", `owner=a"b`}) {
			t.Fatalf("mixed aliases %v lost literal values: %v %v", flags, got, err)
		}
	}
}

func TestCLIArgumentContracts(t *testing.T) {
	cases := [][]string{
		{"init", "db", "extra"}, {"list", "extra"}, {"worker", "extra"},
		{"worker", "list", "extra"}, {"config", "extra"}, {"config", "show", "extra"},
		{"enqueue"}, {"enqueue", "one", "two"}, {"wait"}, {"wait", "task", "--tag", "batch=one"},
	}
	for _, path := range [][]string{{"show"}, {"log"}, {"cancel"}, {"remove"}, {"worker", "stop"}} {
		cases = append(cases, path, append(append([]string{}, path...), "one", "two"))
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			loaded := false
			root := newRootCommand("test", func() (*config.Config, error) {
				loaded = true
				return &config.Config{}, nil
			})
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			if err := root.Execute(); err == nil || loaded {
				t.Fatalf("invalid arguments reached startup: loaded=%t err=%v", loaded, err)
			}
		})
	}
}

func TestCLIOutputContracts(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	cfg.Worker.Workdir = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	run := func(args ...string) (string, string) {
		t.Helper()
		root := newRootCommand("test", func() (*config.Config, error) { return &cfg, nil })
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs(args)
		if err := root.ExecuteContext(ctx); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	action := func(args ...string) string {
		t.Helper()
		out, diagnostic := run(args...)
		if out != "" || diagnostic == "" {
			t.Fatalf("%v: expected diagnostics on stderr: stdout=%q stderr=%q", args, out, diagnostic)
		}
		return diagnostic
	}
	for _, path := range [][]string{{"list"}, {"worker", "list"}} {
		out, diagnostic := run(append(append([]string{}, path...), "--json")...)
		if out != "[]\n" || diagnostic != "" {
			t.Fatalf("empty JSON: stdout=%q stderr=%q", out, diagnostic)
		}
		out, diagnostic = run(append(append([]string{}, path...), "--csv")...)
		if !strings.HasPrefix(out, "ID,") || strings.Count(out, "\n") != 1 || diagnostic != "" {
			t.Fatalf("empty CSV: stdout=%q stderr=%q", out, diagnostic)
		}
		action(path...)
	}
	action("enqueue", "true", "--id", "other", "--tag", "batch=other,owner=match")
	action("enqueue", "echo payload", "--id", "selected", "--tag", "owner=match,batch=selected")
	action("worker", "--once", "--_id", "contract-worker", "--tag", "batch=selected", "--filter", "owner=match")
	other, err := db.GetTask(ctx, pool, "other", time.Minute)
	if err != nil || other == nil || other.Status != db.StatusPending {
		t.Fatalf("mixed tag aliases lost a filter: task=%+v err=%v", other, err)
	}
	action("wait", "selected", "--timeout", "7d")
	out, diagnostic := run("list", "--json", "--status", "completed")
	var tasks []map[string]string
	if err := json.Unmarshal([]byte(out), &tasks); err != nil || len(tasks) != 1 || tasks[0]["id"] != "selected" || tasks[0]["tags"] != "batch=selected owner=match" || diagnostic != "" {
		t.Fatalf("list output: %v stdout=%q stderr=%q", err, out, diagnostic)
	}
	out, diagnostic = run("show", "selected")
	if !strings.Contains(out, "batch=selected, owner=match") || diagnostic != "" {
		t.Fatalf("show output: stdout=%q stderr=%q", out, diagnostic)
	}
	out, _ = run("log", "selected")
	if !strings.Contains(out, "payload") {
		t.Fatalf("missing task output: %q", out)
	}
	action("cancel", "other", "--timeout", "1d")
	action("remove", "other")
	action("worker", "stop", "contract-worker", "--timeout", "1d")
}
