package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/spf13/cobra"
)

var (
	workdir    string
	workerName string
	once       bool
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Start a worker to process tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		// Apply command-specific flag
		if workdir != "" {
			cfg.Worker.Workdir = workdir
		}
		// Use default if still empty
		if cfg.Worker.Workdir == "" {
			cfg.Worker.Workdir = "/tmp/nextask"
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			sig := <-sigCh
			fmt.Printf("\nReceived %s, shutting down...\n", sig)
			cancel()
		}()

		w, err := worker.New(ctx, worker.Config{
			DBURL:   cfg.DB.URL,
			Workdir: cfg.Worker.Workdir,
			Name:    workerName,
			Once:    once,
		})
		if err != nil {
			return err
		}
		defer w.Close(context.Background())

		return w.Run(ctx)
	},
}

func init() {
	workerCmd.Flags().StringVar(&workdir, "workdir", "", "Base directory for task execution (default /tmp/nextask)")
	workerCmd.Flags().StringVar(&workerName, "name", "", "Worker identifier (default: random)")
	workerCmd.Flags().BoolVar(&once, "once", false, "Run single task and exit")
	RootCmd.AddCommand(workerCmd)
}
