package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/config"
)

func TestValidationBeforeDatabase(t *testing.T) {
	type testCase struct {
		args    []string
		message string
	}
	for _, connection := range []string{"", "postgres://127.0.0.1:1/unreachable"} {
		cases := []testCase{
			{[]string{"log", "task", "--head=-1"}, "--head must not be negative"},
			{[]string{"log", "task", "--tail=-1"}, "--tail must not be negative"},
			{[]string{"log", "task", "--head=2", "--tail=2"}, "cannot use both --head and --tail"},
			{[]string{"log", "task", "--head=2", "--attach"}, "cannot use --attach with --head"},
			{[]string{"log", "task", "--stream=typo"}, "unknown stream"},
			{[]string{"list", "--tag=bad"}, "invalid tag format"},
			{[]string{"enqueue", "true", "--tag=bad"}, "invalid tag format"},
			{[]string{"wait", "--tag=bad"}, "invalid tag format"},
			{[]string{"wait", "task", "--timeout=-1s"}, "timeout must not be negative"},
			{[]string{"worker", "--tag=bad"}, "invalid tag format"},
			{[]string{"worker", "--timeout=-1s"}, "timeout must be positive"},
			{[]string{"worker", "--exit-if-idle=-1s"}, "exit-if-idle must not be negative"},
			{[]string{"cancel", "task", "--timeout=0s"}, "timeout must be positive"},
			{[]string{"worker", "stop", "worker", "--timeout=0s"}, "timeout must be positive"},
		}
		for _, command := range [][]string{{"list"}, {"worker", "list"}} {
			for _, option := range []testCase{
				{[]string{"--limit=0"}, "limit must be positive"},
				{[]string{"--offset=-1"}, "offset must not be negative"},
				{[]string{"--since=bad"}, "for \"--since\" flag"},
				{[]string{"--since=-1h"}, "since must be positive"},
				{[]string{"--since=0s"}, "since must be positive"},
				{[]string{"--status=typo"}, "unknown status"},
				{[]string{"--json", "--csv"}, "cannot use both --json and --csv"},
				{[]string{"--wrap", "--json"}, "--wrap requires table output"},
				{[]string{"--wrap", "--csv"}, "--wrap requires table output"},
			} {
				cases = append(cases, testCase{append(append([]string{}, command...), option.args...), option.message})
			}
		}
		for _, tc := range cases {
			t.Run(connection+strings.Join(tc.args, " "), func(t *testing.T) {
				cfg := &config.Config{DB: config.DBConfig{URL: connection}}
				cmd := newRootCommand("test", func() (*config.Config, error) { return cfg, nil })
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(tc.args)
				if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("expected option error %q, got %v", tc.message, err)
				}
			})
		}
	}
}
