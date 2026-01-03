package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var dbURL string

var rootCmd = &cobra.Command{
	Use:   "nextask",
	Short: "Distributed task queue for ML experiments",
	Long:  `A non-intrusive, distributed task queue for ML experiments with full reproducibility.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbURL, "db-url", "", "PostgreSQL connection URL")
}
