package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nextask/nextask/internal/worker"
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
		if dbURL == "" {
			return fmt.Errorf("--db-url is required")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle signals for graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			sig := <-sigCh
			fmt.Printf("\nReceived %s, shutting down...\n", sig)
			cancel()
		}()

		w, err := worker.New(ctx, worker.Config{
			DBURL:   dbURL,
			Workdir: workdir,
			Name:    workerName,
			Once:    once,
		})
		if err != nil {
			cmd.SilenceUsage = true
			return err
		}
		defer w.Close(ctx)

		return w.Run(ctx)
	},
}

func init() {
	workerCmd.Flags().StringVar(&workdir, "workdir", "/tmp/nextask", "Base directory for task execution")
	workerCmd.Flags().StringVar(&workerName, "name", "", "Worker identifier (default: random)")
	workerCmd.Flags().BoolVar(&once, "once", false, "Run single task and exit")
	RootCmd.AddCommand(workerCmd)
}
