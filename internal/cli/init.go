package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize nextask resources",
}

var initDBCmd = &cobra.Command{
	Use:   "db",
	Short: "Create database tables",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer pool.Close()

		if err := db.Migrate(ctx, pool); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}

		fmt.Fprintln(os.Stderr, "Database initialized successfully")
		return nil
	},
}

func init() {
	initCmd.AddCommand(initDBCmd)
	RootCmd.AddCommand(initCmd)
}
