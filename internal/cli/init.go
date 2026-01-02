package cli

import (
	"context"
	"fmt"

	"github.com/nextask/nextask/internal/db"
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
		if dbURL == "" {
			return fmt.Errorf("--db-url is required")
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx, dbURL)
		if err != nil {
			return err
		}
		defer pool.Close()

		if err := db.Migrate(ctx, pool); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}

		fmt.Println("Database initialized successfully")
		return nil
	},
}

func init() {
	initCmd.AddCommand(initDBCmd)
	rootCmd.AddCommand(initCmd)
}
