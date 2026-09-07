package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type workerStopOptions struct {
	timeout time.Duration
}

func newWorkerStopCommand(cfg *config.Config) *cobra.Command {
	var opts workerStopOptions
	cmd := &cobra.Command{
		Use:   "stop WORKER_ID",
		Short: "Stop a running worker",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			if opts.timeout <= 0 {
				return errWithHints("timeout must be positive",
					"Example: "+codeStyle.Render("--timeout 10s"),
				)
			}

			ctx, stop := interruptContext(cmd.Context())
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, opts.timeout)
			defer cancel()
			pool, err := db.Connect(ctx, cfg.DB.URL)
			if err != nil {
				return err
			}
			defer pool.Close()
			return stopWorker(ctx, *cfg, outputFor(cmd).err, pool, args[0])
		},
	}
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 10*time.Second, "Timeout waiting for stop confirmation")
	return cmd
}

// stopWorker checks the registry before sending a hint and confirms stored state
// after subscribing, including when the worker's confirmation hint is lost.
func stopWorker(ctx context.Context, cfg config.Config, stderr io.Writer, pool *pgxpool.Pool, id string) error {
	status, err := workerStatus(ctx, pool, id)
	if err != nil {
		return err
	}
	if status == db.WorkerStatusStopped {
		_, err := fmt.Fprintf(stderr, "Worker %s is already stopped\n", id)
		return err
	}
	watch, err := newStateWatcher(ctx, cfg, stderr, db.FromWorkerChannel(id))
	if err != nil {
		return err
	}
	defer watch.Close()
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, '')", db.ToWorkerChannel(id)); err != nil {
		return fmt.Errorf("failed to send stop signal: %w", err)
	}
	err = watch.Run(ctx, func(ctx context.Context) (bool, error) {
		status, err := workerStatus(ctx, pool, id)
		return status == db.WorkerStatusStopped, err
	})
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		return errWithHints("stop signal sent but worker did not confirm",
			"Worker may be unresponsive or processing a task",
			"Check worker status with "+codeStyle.Render("nextask worker list"))
	}
	if err == nil {
		_, err = fmt.Fprintf(stderr, "Worker %s stopped\n", id)
	}
	return err
}

func workerStatus(ctx context.Context, pool *pgxpool.Pool, id string) (db.WorkerStatus, error) {
	workers, err := db.ListWorkers(ctx, pool, db.WorkerListFilter{ID: id, Limit: 1})
	if err != nil {
		return "", err
	}
	if len(workers) == 0 {
		return "", errWithHints(fmt.Sprintf("worker not found: %s", id),
			"Run "+codeStyle.Render("nextask worker list")+" to see available workers")
	}
	return workers[0].Status, nil
}
