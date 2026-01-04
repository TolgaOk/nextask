package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

var sourcePath string

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

var initSourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Create a local bare git remote for source snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := sourcePath
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			path = filepath.Join(home, ".nextask", "source.git")
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("remote already exists: %s", path)
		}

		_, err := git.PlainInit(path, true)
		if err != nil {
			return fmt.Errorf("failed to initialize remote: %w", err)
		}

		fmt.Printf("Source remote initialized: %s\n", path)
		return nil
	},
}

func init() {
	initSourceCmd.Flags().StringVar(&sourcePath, "path", "", "Path for bare remote (default: ~/.nextask/source.git)")

	initCmd.AddCommand(initDBCmd)
	initCmd.AddCommand(initSourceCmd)
	RootCmd.AddCommand(initCmd)
}
