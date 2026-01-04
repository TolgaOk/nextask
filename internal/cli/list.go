package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
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
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks with optional filters",
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

		// Parse tags
		parsedTags := make(map[string]string)
		for _, tag := range listTags {
			parts := strings.SplitN(tag, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid tag format: %s (expected key=value)", tag)
			}
			parsedTags[parts[0]] = parts[1]
		}

		// Parse since
		var since time.Time
		if listSince != "" {
			dur, err := str2duration.ParseDuration(listSince)
			if err != nil {
				return fmt.Errorf("invalid since format: %s", listSince)
			}
			since = time.Now().Add(-dur)
		}

		filter := db.ListFilter{
			Statuses: listStatuses,
			Tags:     parsedTags,
			Commands: listCommands,
			Since:    since,
			Limit:    uint64(listLimit),
		}

		tasks, err := db.ListTasks(ctx, pool, filter)
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return nil
		}

		// Build table rows
		rows := [][]string{}
		for _, t := range tasks {
			// Format tags
			var tagParts []string
			for k, v := range t.Tags {
				tagParts = append(tagParts, fmt.Sprintf("%s=%s", k, v))
			}
			tagsStr := strings.Join(tagParts, " ")

			rows = append(rows, []string{
				t.ID,
				string(t.Status),
				t.Command,
				tagsStr,
				t.CreatedAt.Format("2006-01-02 15:04"),
			})
		}

		// Style definitions
		headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
		rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

		t := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
			Headers("ID", "STATUS", "COMMAND", "TAGS", "CREATED").
			Rows(rows...).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == 0 {
					return headerStyle
				}
				return rowStyle
			})

		fmt.Fprintln(os.Stdout, t)
		return nil
	},
}

func init() {
	listCmd.Flags().StringSliceVar(&listStatuses, "status", nil, "Filter by status (comma-separated)")
	listCmd.Flags().StringSliceVar(&listTags, "tag", nil, "Filter by tag key=value (repeatable)")
	listCmd.Flags().StringSliceVar(&listCommands, "command", nil, "Substring match in command (repeatable)")
	listCmd.Flags().StringVar(&listSince, "since", "", "Tasks created after (e.g., 1h, 24h, 7d)")
	listCmd.Flags().IntVar(&listLimit, "limit", 50, "Max results")
	RootCmd.AddCommand(listCmd)
}
