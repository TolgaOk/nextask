package cli

import (
	"context"
	"fmt"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	logsStream string
	logsHead   int
	logsTail   int
)

var (
	defaultLogStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	defaultLogPrefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	logsStreamStyles      = map[string]lipgloss.Style{
		"stdout":  lipgloss.NewStyle(),
		"stderr":  lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		"nextask": lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
	}
)

var logsCmd = &cobra.Command{
	Use:   "log TASK_ID",
	Short: "View task output",
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

		task, err := db.GetTask(ctx, pool, args[0])
		if err != nil {
			return err
		}
		if task == nil {
			return errWithHints(fmt.Sprintf("task not found: %s", args[0]),
				"Run "+codeStyle.Render("nextask list")+" to see available tasks",
			)
		}

		if logsHead > 0 && logsTail > 0 {
			return errWithHints("cannot use both --head and --tail",
				"Use "+codeStyle.Render("--head N")+" for first N lines",
				"Use "+codeStyle.Render("--tail N")+" for last N lines",
			)
		}

		limit := logsHead
		tail := false
		if logsTail > 0 {
			limit = logsTail
			tail = true
		}

		logs, err := db.GetLogs(ctx, pool, args[0], logsStream, limit, tail)
		if err != nil {
			return err
		}

		if len(logs) == 0 {
			if logsStream != "" {
				fmt.Println(hintStyle.Render(fmt.Sprintf("No logs with stream %q", logsStream)))
			} else {
				fmt.Println(hintStyle.Render("No logs available"))
			}
			return nil
		}

		for _, log := range logs {
			printLog(log)
		}

		return nil
	},
}

func printLog(log db.TaskLog) {
	style, ok := logsStreamStyles[log.Stream]
	if !ok {
		style = defaultLogStyle
	}

	prefix := ""
	if log.Stream == "stderr" {
		prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("[error]") + " "
	} else if log.Stream != "stdout" {
		prefix = defaultLogPrefixStyle.Bold(true).Render("["+log.Stream+"]") + " "
	}

	fmt.Println(prefix + style.Render(log.Data))
}

func init() {
	logsCmd.Flags().StringVarP(&logsStream, "stream", "s", "", "Filter by stream (stdout, stderr, nextask)")
	logsCmd.Flags().IntVar(&logsHead, "head", 0, "Show first N lines")
	logsCmd.Flags().IntVar(&logsTail, "tail", 0, "Show last N lines")
	RootCmd.AddCommand(logsCmd)
}
