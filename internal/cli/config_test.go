package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/TolgaOk/nextask/internal/config"
	"github.com/spf13/cobra"
)

func TestConfigShow(t *testing.T) {
	t.Setenv("NEXTASK_DB_URL", "postgres://alice:secret-value@localhost/db")
	cfg, err := config.LoadFrom("/nonexistent/nextask-test-config.toml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Worker.Workdir = "/tmp/with spaces"
	for _, sources := range []bool{false, true} {
		cmd := &cobra.Command{}
		cmd.Flags().Bool("sources", sources, "")
		var output bytes.Buffer
		cmd.SetOut(&output)
		if err := showConfig(cmd, *cfg); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), "secret-value") || strings.Contains(output.String(), "alice") {
			t.Fatal("config output leaked credentials")
		}
		if strings.Contains(output.String(), "env:NEXTASK_DB_URL") != sources {
			t.Fatalf("unexpected source display: %s", output.String())
		}
		var displayed config.Config
		if _, err := toml.Decode(output.String(), &displayed); err != nil {
			t.Fatalf("output is not TOML: %v", err)
		}
		if displayed.Worker.Workdir != "/tmp/with spaces" || !strings.Contains(output.String(), "localhost/db") {
			t.Fatal("effective values were lost")
		}
	}
}
