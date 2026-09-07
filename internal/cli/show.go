package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
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
	case "stopped":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	}
}

func newShowCommand(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show TASK_ID",
		Short: "Show task details",
		Args:  cobra.ExactArgs(1),
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

			task, err := db.GetTask(ctx, pool, args[0], cfg.Worker.StaleDuration())
			if err != nil {
				return err
			}
			if task == nil {
				return errWithHints(fmt.Sprintf("task not found: %s", args[0]),
					"Run "+codeStyle.Render("nextask list")+" to see available tasks",
				)
			}

			return printTask(cmd.OutOrStdout(), task)
		},
	}

	return cmd
}

func printTask(out io.Writer, task *db.Task) error {
	var buffer strings.Builder
	formatTask(&buffer, task)
	_, err := io.WriteString(out, buffer.String())
	return err
}

func formatTask(out io.Writer, task *db.Task) {
	printField(out, showLabelStyle, "ID", task.ID)
	printField(out, showLabelStyle, "Status", statusStyle(task.Status).Render(string(task.Status)))
	printField(out, showLabelStyle, "Command", task.Command)

	if len(task.Tags) > 0 {
		var tagParts []string
		for k, v := range task.Tags {
			tagParts = append(tagParts, fmt.Sprintf("%s=%s", k, v))
		}
		printField(out, showLabelStyle, "Tags", strings.Join(tagParts, ", "))
	}

	printField(out, showLabelStyle, "Created", formatTime(task.CreatedAt))
	if task.StartedAt != nil {
		printField(out, showLabelStyle, "Started", formatTime(*task.StartedAt))
	}
	if task.FinishedAt != nil {
		printField(out, showLabelStyle, "Finished", formatTime(*task.FinishedAt))
		if task.StartedAt != nil {
			duration := task.FinishedAt.Sub(*task.StartedAt)
			printField(out, showLabelStyle, "Duration", formatDuration(duration))
		}
	}

	if task.ExitCode != nil {
		exitStyle := showValueStyle
		if *task.ExitCode != 0 {
			exitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		}
		fmt.Fprintf(out, "%s  %s\n", showLabelStyle.Render("Exit Code:"), exitStyle.Render(fmt.Sprintf("%d", *task.ExitCode)))
	}

	if task.WorkerID != nil || task.WorkerInfo != nil {
		fmt.Fprintln(out, showSectionStyle.Render("Worker"))
		if task.WorkerID != nil {
			printField(out, showIndentLabel, "ID", *task.WorkerID)
		}
		if task.WorkerInfo != nil {
			printField(out, showIndentLabel, "Hostname", task.WorkerInfo.Hostname)
			printField(out, showIndentLabel, "OS", task.WorkerInfo.OS)
			printField(out, showIndentLabel, "PID", fmt.Sprintf("%d", task.WorkerInfo.PID))
		}
	}

	if task.SourceType != "noop" && task.SourceType != "" && task.SourceType != "command" {
		fmt.Fprintln(out, showSectionStyle.Render("Source"))
		printField(out, showIndentLabel, "Type", task.SourceType)
		printSourceConfig(out, task.SourceType, task.SourceConfig)
	}
}

func printField(out io.Writer, labelStyle lipgloss.Style, label, value string) {
	fmt.Fprintf(out, "%s  %s\n", labelStyle.Render(label+":"), showValueStyle.Render(value))
}

func printSourceConfig(out io.Writer, sourceType string, data json.RawMessage) {
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
				printField(out, showIndentLabel, "Remote", cfg.Remote)
			}
			if cfg.Ref != "" {
				printField(out, showIndentLabel, "Ref", cfg.Ref)
			}
			if cfg.Commit != "" {
				printField(out, showIndentLabel, "Commit", cfg.Commit)
			}
			return
		}
	}

	printRawConfig(out, data)
}

func printRawConfig(out io.Writer, data json.RawMessage) {
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	for k, v := range parsed {
		printField(out, showIndentLabel, k, fmt.Sprintf("%v", v))
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
