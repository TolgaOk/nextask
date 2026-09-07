package cli

import (
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

func newInitCommand(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize nextask resources",
	}
	cmd.AddCommand(newInitDBCommand(cfg))
	return cmd
}

func newInitDBCommand(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Create database tables",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			ctx := cmd.Context()

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
	return cmd
}
