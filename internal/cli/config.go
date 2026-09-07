package cli

import (
	"encoding/json"
	"fmt"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand(cfg *config.Config) *cobra.Command {
	run := func(cmd *cobra.Command, args []string) error { return showConfig(cmd, *cfg) }
	cmd := &cobra.Command{Use: "config", Short: "Show effective configuration with credentials redacted", Args: cobra.NoArgs, RunE: run}
	cmd.PersistentFlags().Bool("sources", false, "Include the origin of each effective value")
	cmd.AddCommand(&cobra.Command{Use: "show", Short: cmd.Short, Args: cobra.NoArgs, RunE: run})
	return cmd
}

func showConfig(cmd *cobra.Command, cfg config.Config) error {
	sources, _ := cmd.Flags().GetBool("sources")
	for _, setting := range cfg.Settings() {
		var value string
		if v, ok := setting.Value.(string); ok {
			value = fmt.Sprintf("%q", v)
		} else {
			encoded, err := json.Marshal(setting.Value)
			if err != nil {
				return err
			}
			value = string(encoded)
		}
		line := setting.Key + " = " + value
		if sources {
			line += fmt.Sprintf(" # source: %q", setting.Source)
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
			return err
		}
	}
	return nil
}
