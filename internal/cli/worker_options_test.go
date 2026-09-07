package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/config"
)

func TestWorkerValidationBeforeStartup(t *testing.T) {
	for _, mode := range []string{"foreground", "daemon"} {
		for _, tc := range []struct {
			flag, value, message string
		}{
			{"--timeout", "bad", "invalid timeout"},
			{"--timeout", "0s", "timeout must be positive"},
			{"--exit-if-idle", "bad", "invalid exit-if-idle"},
			{"--exit-if-idle", "-1s", "exit-if-idle must not be negative"},
			{"--filter", "bad", "invalid tag format"},
		} {
			t.Run(mode+tc.flag+tc.value, func(t *testing.T) {
				workdir := filepath.Join(t.TempDir(), "worker")
				cfg := config.Config{DB: config.DBConfig{URL: "postgres://127.0.0.1:1/unreachable"}}
				cmd := newWorkerCommand(&cfg)
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				args := []string{"--workdir", workdir, tc.flag, tc.value}
				if mode == "daemon" {
					args = append(args, "--daemon")
				}
				cmd.SetArgs(args)
				if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("expected validation error %q, got %v", tc.message, err)
				}
				if _, err := os.Stat(workdir); !os.IsNotExist(err) {
					t.Fatalf("invalid options created worker files: %v", err)
				}
			})
		}
	}
}
