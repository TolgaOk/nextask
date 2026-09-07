package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

type listOptions struct {
	statuses []string
	tags     []string
	commands []string
	since    string
	limit    int
	offset   int
	json     bool
	csv      bool
	wrap     bool
}

func newListCommand(cfg *config.Config) *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks with optional filters",
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

			parsedTags, err := parseTags(opts.tags)
			if err != nil {
				return err
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

			for _, s := range opts.statuses {
				switch db.TaskStatus(s) {
				case db.StatusPending, db.StatusRunning, db.StatusCompleted,
					db.StatusFailed, db.StatusCancelled, db.StatusStale:
				default:
					return errWithHints(fmt.Sprintf("unknown status: %s", s),
						"Valid: "+codeStyle.Render("pending")+", "+codeStyle.Render("running")+", "+codeStyle.Render("completed")+", "+codeStyle.Render("failed")+", "+codeStyle.Render("cancelled")+", "+codeStyle.Render("stale"),
					)
				}
			}

			filter := db.ListFilter{
				Statuses:       opts.statuses,
				Tags:           parsedTags,
				Commands:       opts.commands,
				Since:          since,
				Limit:          uint64(opts.limit),
				Offset:         uint64(opts.offset),
				StaleThreshold: cfg.Worker.StaleDuration(),
			}

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
	cmd.Flags().StringVar(&opts.since, "since", "", "Tasks created after (e.g., 1h, 24h, 7d)")
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "Max results")
	cmd.Flags().IntVar(&opts.offset, "offset", 0, "Skip first N results")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&opts.csv, "csv", false, "Output as CSV")
	cmd.Flags().BoolVar(&opts.wrap, "wrap", false, "Wrap long lines instead of truncating")
	return cmd
}
