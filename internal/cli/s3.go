package cli

import (
	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
	"github.com/TolgaOk/nextask/internal/storage"
	"github.com/spf13/cobra"
	"time"
)

func newS3Command(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{Use: "s3", Short: "Manage task artifacts in S3-compatible storage", Args: cobra.NoArgs}
	cmd.AddCommand(newS3FetchCommand(cfg))
	return cmd
}

func newS3FetchCommand(cfg *config.Config) *cobra.Command {
	options := storage.FetchOptions{}
	var timeout durationFlag
	cmd := &cobra.Command{
		Use:   "fetch TASK_ID --to DIR",
		Short: "Download a task's artifacts without database access",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return err
			}
			if err := db.ValidateTaskID(args[0]); err != nil {
				return err
			}
			options.Timeout = timeout.Duration
			return options.Validate()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := interruptContext(cmd.Context())
			defer stop()
			return integrations.FetchS3(ctx, cfg.Integrations["s3"], args[0], options, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&options.Destination, "to", "", "Destination directory (required)")
	cmd.Flags().StringArrayVar(&options.Include, "include", nil, "Download matching relative paths (repeatable; default all)")
	cmd.Flags().StringArrayVar(&options.Exclude, "exclude", nil, "Exclude matching relative paths (repeatable)")
	cmd.Flags().BoolVar(&options.DryRun, "dry-run", false, "List selected paths without writing files")
	cmd.Flags().BoolVar(&options.Overwrite, "overwrite", false, "Replace existing regular files after complete downloads")
	timeout.addFlag(cmd, "timeout", 5*time.Minute, "Time limit for listing and each file download (up to 24h)")
	return cmd
}
