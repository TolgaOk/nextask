package cli

import (
	"fmt"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

type workerListOptions struct {
	listingOptions
	statuses []string
}

func newWorkerListCommand(cfg *config.Config) *cobra.Command {
	var opts workerListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered workers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := opts.filter(cfg.Worker.StaleDuration(), time.Now())
			if err != nil {
				return err
			}
			if cfg.DB.URL == "" {
				return errDBRequired()
			}
			ctx := cmd.Context()
			pool, err := db.Connect(ctx, cfg.DB.URL)
			if err != nil {
				return err
			}
			defer pool.Close()

			workers, err := db.ListWorkers(ctx, pool, filter)
			if err != nil {
				return err
			}

			total, err := db.CountWorkers(ctx, pool, filter)
			if err != nil {
				return err
			}

			plain := opts.json || opts.csv

			rows := [][]string{}
			for _, w := range workers {
				status := string(w.Status)
				displayStatus := status
				if !plain {
					displayStatus = statusStyle(db.TaskStatus(status)).Render(status)
				}
				rows = append(rows, []string{
					w.ID,
					fmt.Sprintf("%d", w.PID),
					w.Hostname,
					displayStatus,
					w.StartedAt.Local().Format("2006-01-02 15:04"),
				})
			}

			return PrintTable(outputFor(cmd), TableConfig{
				EmptyMessage: "No workers found",
				Headers:      []string{"ID", "PID", "HOSTNAME", "STATUS", "STARTED"},
				Rows:         rows,
				Count:        total,
				Offset:       opts.offset,
				JSON:         opts.json,
				CSV:          opts.csv,
				Wrap:         opts.wrap,
			})
		},
	}
	cmd.Flags().StringSliceVar(&opts.statuses, "status", nil, "Filter by status: running, stopped, stale (comma-separated)")
	opts.listingOptions.addFlags(cmd, "Workers started within the past duration")
	return cmd
}

func (opts workerListOptions) filter(stale time.Duration, now time.Time) (db.WorkerListFilter, error) {
	since, err := opts.listingOptions.resolve(now)
	if err != nil {
		return db.WorkerListFilter{}, err
	}
	statuses := make([]db.WorkerStatus, 0, len(opts.statuses))
	for _, status := range opts.statuses {
		value := db.WorkerStatus(status)
		switch value {
		case db.WorkerStatusRunning, db.WorkerStatusStopped, db.WorkerStatusStale:
		default:
			return db.WorkerListFilter{}, errWithHints(fmt.Sprintf("unknown status: %s", status),
				"Valid: running, stopped, stale")
		}
		statuses = append(statuses, value)
	}
	return db.WorkerListFilter{Statuses: statuses, Since: since, Limit: uint64(opts.limit),
		Offset: uint64(opts.offset), StaleThreshold: stale}, nil
}
