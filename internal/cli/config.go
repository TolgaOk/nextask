package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show effective configuration with credentials redacted",
	Args:  cobra.NoArgs,
	RunE:  showConfig,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show effective configuration with credentials redacted",
	Args:  cobra.NoArgs,
	RunE:  showConfig,
}

func showConfig(cmd *cobra.Command, args []string) error {
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

func init() {
	configCmd.PersistentFlags().Bool("sources", false, "Include the origin of each effective value")
	configCmd.AddCommand(configShowCmd)
	RootCmd.AddCommand(configCmd)
}
