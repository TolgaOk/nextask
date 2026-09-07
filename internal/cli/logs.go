package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type logsOptions struct {
	stream string
	head   int
	tail   int
	attach bool
}

var (
	defaultLogStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	defaultLogPrefixStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	logsStreamStyles      = map[string]lipgloss.Style{
		"stdout":  lipgloss.NewStyle(),
		"stderr":  lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		"nextask": lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
	}
)

func newLogsCommand(cfg *config.Config) *cobra.Command {
	var opts logsOptions
	cmd := &cobra.Command{
		Use:   "log TASK_ID",
		Short: "View task output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
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

			task, err := db.GetTask(ctx, pool, args[0], cfg.Worker.StaleDuration())
			if err != nil {
				return err
			}
			if task == nil {
				return errWithHints(fmt.Sprintf("task not found: %s", args[0]),
					"Run "+codeStyle.Render("nextask list")+" to see available tasks",
				)
			}

			limit := opts.head
			tail := false
			if opts.tail > 0 {
				limit = opts.tail
				tail = true
			}

			logs, err := db.GetLogs(ctx, pool, args[0], opts.stream, limit, tail)
			if err != nil {
				return err
			}

			var lastLogID int
			if len(logs) == 0 {
				if opts.stream != "" {
					_, err = fmt.Fprintln(cmd.ErrOrStderr(), hintStyle.Render(fmt.Sprintf("No logs with stream %q", opts.stream)))
				} else if !opts.attach {
					_, err = fmt.Fprintln(cmd.ErrOrStderr(), hintStyle.Render("No logs available"))
				}
				if err != nil {
					return err
				}
			} else {
				for _, log := range logs {
					if err := printLog(cmd.OutOrStdout(), log); err != nil {
						return err
					}
					if log.ID > lastLogID {
						lastLogID = log.ID
					}
				}
			}

			if !opts.attach {
				return nil
			}

			// Only stream if task is still active
			if task.Status != db.StatusPending && task.Status != db.StatusRunning {
				return nil
			}

			return logsAndAttach(ctx, *cfg, outputFor(cmd), pool, task.ID, lastLogID, opts.stream)
		},
	}

	cmd.Flags().StringVarP(&opts.stream, "stream", "s", "", "Filter by stream (stdout, stderr, nextask)")
	cmd.Flags().IntVar(&opts.head, "head", 0, "Show first N lines")
	cmd.Flags().IntVar(&opts.tail, "tail", 0, "Show last N lines")
	cmd.Flags().BoolVarP(&opts.attach, "attach", "a", false, "Stream logs until task completes")
	return cmd
}

func printLog(out io.Writer, log db.TaskLog) error {
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

	_, err := fmt.Fprintln(out, prefix+style.Render(log.Data))
	return err
}

func logsAndAttach(ctx context.Context, cfg config.Config, out commandOutput, pool *pgxpool.Pool, taskID string, lastLogID int, stream string) error {
	ctx, stop := interruptContext(ctx)
	defer stop()
	if _, err := fmt.Fprintln(out.err, hintStyle.Render("Streaming logs (Ctrl+C to stop watching)...")); err != nil {
		return err
	}
	task, err := streamTask(ctx, cfg, out.err, pool, taskID, &lastLogID, func(log db.TaskLog) error {
		if stream == "" || log.Stream == stream {
			return printLog(out.out, log)
		}
		return nil
	})
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		_, err := fmt.Fprintln(out.err)
		return err
	}
	if err != nil {
		return err
	}
	return printAttachedCompletion(out.err, task)
}

func (opts logsOptions) validate() error {
	if opts.head < 0 {
		return errWithHints("--head must not be negative",
			"Example: "+codeStyle.Render("--head 50"),
		)
	}
	if opts.tail < 0 {
		return errWithHints("--tail must not be negative",
			"Example: "+codeStyle.Render("--tail 50"),
		)
	}

	if opts.head > 0 && opts.tail > 0 {
		return errWithHints("cannot use both --head and --tail",
			"Use "+codeStyle.Render("--head N")+" for first N lines",
			"Use "+codeStyle.Render("--tail N")+" for last N lines",
		)
	}

	if opts.attach && opts.head > 0 {
		return errWithHints("cannot use --attach with --head",
			"Use "+codeStyle.Render("--tail N --attach")+" to show last N lines then stream",
		)
	}
	switch opts.stream {
	case "", "stdout", "stderr", "nextask":
	default:
		return errWithHints(fmt.Sprintf("unknown stream: %s", opts.stream), "Valid: stdout, stderr, nextask")
	}
	return nil
}
