package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
)

type listOptions struct {
	statuses []string
	tags     []string
	commands []string
	listingOptions
}

func newListCommand(cfg *config.Config) *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks with optional filters",
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

			tasks, err := db.ListTasks(ctx, pool, filter)
			if err != nil {
				return err
			}

			total, err := db.CountTasks(ctx, pool, filter)
			if err != nil {
				return err
			}

			if len(tasks) == 0 {
				if opts.json {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "[]")
				} else if opts.csv {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "ID,STATUS,COMMAND,TAGS,CREATED")
				} else {
					_, err = fmt.Fprintln(cmd.ErrOrStderr(), "No tasks found")
				}
				return err
			}

			plain := opts.json || opts.csv
			rows := [][]string{}
			for _, t := range tasks {
				var tagParts []string
				for k, v := range t.Tags {
					tagParts = append(tagParts, fmt.Sprintf("%s=%s", k, v))
				}
				tagsStr := strings.Join(tagParts, " ")

				status := string(t.Status)
				if !plain {
					status = statusStyle(t.Status).Render(status)
				}

				rows = append(rows, []string{
					t.ID,
					status,
					t.Command,
					tagsStr,
					t.CreatedAt.Format("2006-01-02 15:04"),
				})
			}

			return PrintTable(outputFor(cmd), TableConfig{
				Headers: []string{"ID", "STATUS", "COMMAND", "TAGS", "CREATED"},
				Rows:    rows,
				Count:   total,
				Offset:  opts.offset,
				JSON:    opts.json,
				CSV:     opts.csv,
				Wrap:    opts.wrap,
			})
		},
	}

	cmd.Flags().StringSliceVar(&opts.statuses, "status", nil, "Filter by status (comma-separated)")
	cmd.Flags().StringSliceVar(&opts.tags, "tag", nil, "Filter by tag key=value (repeatable)")
	cmd.Flags().StringSliceVar(&opts.commands, "command", nil, "Substring match in command (repeatable)")
	opts.listingOptions.addFlags(cmd, "Tasks created within the past duration")
	return cmd
}

func (opts listOptions) filter(stale time.Duration, now time.Time) (db.ListFilter, error) {
	since, err := opts.listingOptions.resolve(now)
	if err != nil {
		return db.ListFilter{}, err
	}
	tags, err := parseTags(opts.tags)
	if err != nil {
		return db.ListFilter{}, err
	}
	for _, status := range opts.statuses {
		switch db.TaskStatus(status) {
		case db.StatusPending, db.StatusRunning, db.StatusCompleted, db.StatusFailed, db.StatusCancelled, db.StatusStale:
		default:
			return db.ListFilter{}, errWithHints(fmt.Sprintf("unknown status: %s", status),
				"Valid: pending, running, completed, failed, cancelled, stale")
		}
	}
	return db.ListFilter{Statuses: opts.statuses, Tags: tags, Commands: opts.commands, Since: since,
		Limit: uint64(opts.limit), Offset: uint64(opts.offset), StaleThreshold: stale}, nil
}
