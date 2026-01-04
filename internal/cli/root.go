// Package cli implements the nextask command-line interface.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var dbURL string

// SetVersion configures the version string displayed by --version.
func SetVersion(v string) {
	RootCmd.Version = v
}

// RootCmd is the base command for the nextask CLI.
var RootCmd = &cobra.Command{
	Use:   "nextask",
	Short: "Distributed task queue providing full reproducibility with non-intrusive source snapshotting",
	Long: `Distributed task queue providing full reproducibility with non-intrusive source snapshotting.

Tasks are stored and managed in PostgreSQL with full stdout/stderr capture from workers.
During enqueue, nextask can snapshot the working repository—including unstaged changes—to
a remote git server, preserving the exact source code for execution by available workers.`,
}

// Execute runs the root command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVar(&dbURL, "db-url", "", "PostgreSQL connection URL")
}
