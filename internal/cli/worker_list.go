package cli

import (
	"fmt"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

type workerListOptions struct {
	limit  int
	offset int
	since  string
	json   bool
	csv    bool
	wrap   bool
}

func newWorkerListCommand(cfg *config.Config) *cobra.Command {
	var opts workerListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered workers",
		Args:  cobra.NoArgs,
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

			statusFlag, _ := cmd.Flags().GetString("status")
			var statusFilter *db.WorkerStatus
			if statusFlag != "" {
				s := db.WorkerStatus(statusFlag)
				switch s {
				case db.WorkerStatusRunning, db.WorkerStatusStopped, db.WorkerStatusStale:
				default:
					return errWithHints(fmt.Sprintf("unknown status: %s", statusFlag),
						"Valid: "+codeStyle.Render("running")+", "+codeStyle.Render("stopped")+", "+codeStyle.Render("stale"),
					)
				}
				statusFilter = &s
			}

			var since time.Time
			if opts.since != "" {
				dur, err := str2duration.ParseDuration(opts.since)
				if err != nil {
					return errWithHints(fmt.Sprintf("invalid since format: %s", opts.since),
						"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"),
					)
				}
				since = time.Now().Add(-dur)
			}

			if opts.limit <= 0 {
				return errWithHints("limit must be positive",
					"Example: "+codeStyle.Render("--limit 50"),
				)
			}

			if opts.offset < 0 {
				return errWithHints("offset must not be negative",
					"Example: "+codeStyle.Render("--offset 50"),
				)
			}

			filter := db.WorkerListFilter{
				Status:         statusFilter,
				Since:          since,
				Limit:          uint64(opts.limit),
				Offset:         uint64(opts.offset),
				StaleThreshold: cfg.Worker.StaleDuration(),
			}

			workers, err := db.ListWorkers(ctx, pool, filter)
			if err != nil {
				return err
			}

			total, err := db.CountWorkers(ctx, pool, filter)
			if err != nil {
				return err
			}

			if len(workers) == 0 {
				if opts.json {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "[]")
				} else if opts.csv {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "ID,PID,HOSTNAME,STATUS,STARTED")
				} else {
					_, err = fmt.Fprintln(cmd.ErrOrStderr(), "No workers found")
				}
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
					w.StartedAt.Format("2006-01-02 15:04"),
				})
			}

			return PrintTable(outputFor(cmd), TableConfig{
				Headers: []string{"ID", "PID", "HOSTNAME", "STATUS", "STARTED"},
				Rows:    rows,
				Count:   total,
				Offset:  opts.offset,
				JSON:    opts.json,
				CSV:     opts.csv,
				Wrap:    opts.wrap,
			})
		},
	}
	cmd.Flags().String("status", "", "Filter by status (running, stopped, stale)")
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "Max results")
	cmd.Flags().IntVar(&opts.offset, "offset", 0, "Skip first N results")
	cmd.Flags().StringVar(&opts.since, "since", "", "Workers started after (e.g., 1h, 24h, 7d)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&opts.csv, "csv", false, "Output as CSV")
	cmd.Flags().BoolVar(&opts.wrap, "wrap", false, "Wrap long lines instead of truncating")
	return cmd
}
