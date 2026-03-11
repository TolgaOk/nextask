package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	showLabelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Width(12).Align(lipgloss.Right)
	showValueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	showSectionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true).MarginTop(1)
	showIndentLabel  = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Width(12).Align(lipgloss.Right).PaddingLeft(2)
)

func statusStyle(status db.TaskStatus) lipgloss.Style {
	switch status {
	case db.StatusCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	case db.StatusFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	case db.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	case db.StatusCancelled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	case db.StatusStale:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	}
}

var showCmd = &cobra.Command{
	Use:   "show TASK_ID",
	Short: "Show task details",
	Args:  cobra.ExactArgs(1),
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

		task, err := db.GetTask(ctx, pool, args[0], cfg.Worker.StaleDuration())
		if err != nil {
			return err
		}
		if task == nil {
			return errWithHints(fmt.Sprintf("task not found: %s", args[0]),
				"Run "+codeStyle.Render("nextask list")+" to see available tasks",
			)
		}

		printTask(task)
		return nil
	},
}

func printTask(task *db.Task) {
	printField(showLabelStyle, "ID", task.ID)
	printField(showLabelStyle, "Status", statusStyle(task.Status).Render(string(task.Status)))
	printField(showLabelStyle, "Command", task.Command)

	if len(task.Tags) > 0 {
		var tagParts []string
		for k, v := range task.Tags {
			tagParts = append(tagParts, fmt.Sprintf("%s=%s", k, v))
		}
		printField(showLabelStyle, "Tags", strings.Join(tagParts, ", "))
	}

	printField(showLabelStyle, "Created", formatTime(task.CreatedAt))
	if task.StartedAt != nil {
		printField(showLabelStyle, "Started", formatTime(*task.StartedAt))
	}
	if task.FinishedAt != nil {
		printField(showLabelStyle, "Finished", formatTime(*task.FinishedAt))
		if task.StartedAt != nil {
			duration := task.FinishedAt.Sub(*task.StartedAt)
			printField(showLabelStyle, "Duration", formatDuration(duration))
		}
	}

	if task.ExitCode != nil {
		exitStyle := showValueStyle
		if *task.ExitCode != 0 {
			exitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		}
		fmt.Printf("%s  %s\n", showLabelStyle.Render("Exit Code:"), exitStyle.Render(fmt.Sprintf("%d", *task.ExitCode)))
	}

	if task.WorkerID != nil || task.WorkerInfo != nil {
		fmt.Println(showSectionStyle.Render("Worker"))
		if task.WorkerID != nil {
			printField(showIndentLabel, "ID", *task.WorkerID)
		}
		if task.WorkerInfo != nil {
			printField(showIndentLabel, "Hostname", task.WorkerInfo.Hostname)
			printField(showIndentLabel, "OS", task.WorkerInfo.OS)
			printField(showIndentLabel, "PID", fmt.Sprintf("%d", task.WorkerInfo.PID))
		}
	}

	if task.SourceType != "noop" && task.SourceType != "" {
		fmt.Println(showSectionStyle.Render("Source"))
		printField(showIndentLabel, "Type", task.SourceType)
		printSourceConfig(task.SourceType, task.SourceConfig)
	}
}

func printField(labelStyle lipgloss.Style, label, value string) {
	fmt.Printf("%s  %s\n", labelStyle.Render(label+":"), showValueStyle.Render(value))
}

func printSourceConfig(sourceType string, data json.RawMessage) {
	if len(data) == 0 {
		return
	}

	if sourceType == "git" {
		var cfg struct {
			Remote string `json:"remote"`
			Ref    string `json:"ref"`
			Commit string `json:"commit"`
		}
		if err := json.Unmarshal(data, &cfg); err == nil {
			if cfg.Remote != "" {
				printField(showIndentLabel, "Remote", cfg.Remote)
			}
			if cfg.Ref != "" {
				printField(showIndentLabel, "Ref", cfg.Ref)
			}
			if cfg.Commit != "" {
				printField(showIndentLabel, "Commit", cfg.Commit)
			}
			return
		}
	}

	printRawConfig(data)
}

func printRawConfig(data json.RawMessage) {
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	for k, v := range parsed {
		printField(showIndentLabel, k, fmt.Sprintf("%v", v))
	}
}

func formatTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, mins)
}

func init() {
	RootCmd.AddCommand(showCmd)
}
