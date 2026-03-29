package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

var (
	listStatuses []string
	listTags     []string
	listCommands []string
	listSince    string
	listLimit    int
	listOffset   int
	listJSON     bool
	listCSV      bool
	listWrap     bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks with optional filters",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.DB.URL == "" {
			return errDBRequired()
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx, cfg.DB.URL)
		if err != nil {
			return err
		}
		defer pool.Close()

		parsedTags := make(map[string]string)
		for _, tag := range listTags {
			parts := strings.SplitN(tag, "=", 2)
			if len(parts) != 2 {
				return errWithHints(fmt.Sprintf("invalid tag format: %s", tag),
					"Expected format: "+codeStyle.Render("key=value"),
				)
			}
			parsedTags[parts[0]] = parts[1]
		}

		var since time.Time
		if listSince != "" {
			dur, err := str2duration.ParseDuration(listSince)
			if err != nil {
				return errWithHints(fmt.Sprintf("invalid since format: %s", listSince),
					"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"),
				)
			}
			since = time.Now().Add(-dur)
		}

		if listLimit < 0 {
			return errWithHints("limit must be positive",
				"Example: "+codeStyle.Render("--limit 50"),
			)
		}

		for _, s := range listStatuses {
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
			Statuses:       listStatuses,
			Tags:           parsedTags,
			Commands:       listCommands,
			Since:          since,
			Limit:          uint64(listLimit),
			Offset:         uint64(listOffset),
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
			if listJSON {
				fmt.Println("[]")
			} else if listCSV {
				fmt.Println("ID,STATUS,COMMAND,TAGS,CREATED")
			} else {
				fmt.Fprintln(os.Stderr, "No tasks found")
			}
			return nil
		}

		plain := listJSON || listCSV
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

		return PrintTable(TableConfig{
			Headers: []string{"ID", "STATUS", "COMMAND", "TAGS", "CREATED"},
			Rows:    rows,
			Count:   total,
			Offset:  listOffset,
			JSON:    listJSON,
			CSV:     listCSV,
			Wrap:    listWrap,
		})
	},
}

func init() {
	listCmd.Flags().StringSliceVar(&listStatuses, "status", nil, "Filter by status (comma-separated)")
	listCmd.Flags().StringSliceVar(&listTags, "tag", nil, "Filter by tag key=value (repeatable)")
	listCmd.Flags().StringSliceVar(&listCommands, "command", nil, "Substring match in command (repeatable)")
	listCmd.Flags().StringVar(&listSince, "since", "", "Tasks created after (e.g., 1h, 24h, 7d)")
	listCmd.Flags().IntVar(&listLimit, "limit", 50, "Max results")
	listCmd.Flags().IntVar(&listOffset, "offset", 0, "Skip first N results")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output as JSON")
	listCmd.Flags().BoolVar(&listCSV, "csv", false, "Output as CSV")
	listCmd.Flags().BoolVar(&listWrap, "wrap", false, "Wrap long lines instead of truncating")
	RootCmd.AddCommand(listCmd)
}
