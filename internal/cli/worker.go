package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

type workerOptions struct {
	workdir    string
	once       bool
	daemon     bool
	rm         bool
	exitIfIdle string
	id         string
	timeout    string
	filters    []string
}

func newWorkerCommand(cfg *config.Config) *cobra.Command {
	var opts workerOptions
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Start a worker to process tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			resolved, timeout, err := opts.resolve(*cfg)
			if err != nil {
				return err
			}
			out := outputFor(cmd)
			resolved.Stdout, resolved.Stderr = out.out, out.err
			if opts.daemon {
				return daemonize(resolved, timeout)
			}

			ctx, stop := interruptContext(cmd.Context())
			defer stop()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			w, err := worker.New(ctx, resolved)
			if err != nil {
				return err
			}
			defer w.Close()

			return w.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&opts.workdir, "workdir", "", "Base directory for task execution (default /tmp/nextask)")
	cmd.Flags().BoolVar(&opts.once, "once", false, "Run single task and exit")
	cmd.Flags().BoolVar(&opts.rm, "rm", false, "Remove task workdir after completion")
	cmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Run as background daemon")
	cmd.Flags().StringVar(&opts.timeout, "timeout", "", "Stop worker after duration (e.g., 1h, 24h, 7d)")
	cmd.Flags().StringVar(&opts.exitIfIdle, "exit-if-idle", "", "Exit if no tasks claimed within duration (e.g., 0s, 1m, 5m)")
	cmd.Flags().StringSliceVar(&opts.filters, "filter", nil, "Only claim tasks with tag (key=value, repeatable)")
	cmd.Flags().StringVar(&opts.id, "_id", "", "Worker ID (internal use)")
	cmd.Flags().MarkHidden("_id")
	cmd.AddCommand(newWorkerListCommand(cfg), newWorkerStopCommand(cfg))
	return cmd
}

// resolve validates flags before either starting a worker or spawning a daemon.
func (opts workerOptions) resolve(cfg config.Config) (worker.Config, time.Duration, error) {
	resolved := worker.Config{
		DBURL: cfg.DB.URL, Workdir: cfg.Worker.Workdir, Name: opts.id,
		Once: opts.once, Rm: opts.rm,
		HeartbeatInterval: cfg.Worker.HeartbeatInterval,
		BackoffInitial:    cfg.Retry.InitialInterval, BackoffMax: cfg.Retry.MaxInterval,
		LogFlushLines: cfg.Worker.LogFlushLines, LogFlushInterval: cfg.Worker.LogFlushInterval,
		LogBufferSize: cfg.Worker.LogBufferSize,
	}
	if opts.workdir != "" {
		resolved.Workdir = opts.workdir
	}
	var timeout time.Duration
	if opts.timeout != "" {
		var err error
		timeout, err = str2duration.ParseDuration(opts.timeout)
		if err != nil {
			return worker.Config{}, 0, errWithHints(fmt.Sprintf("invalid timeout: %s", opts.timeout),
				"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"))
		}
		if timeout <= 0 {
			return worker.Config{}, 0, errWithHints("timeout must be positive",
				"Examples: "+codeStyle.Render("1h")+", "+codeStyle.Render("24h")+", "+codeStyle.Render("7d"))
		}
	}
	if opts.exitIfIdle != "" {
		duration, err := str2duration.ParseDuration(opts.exitIfIdle)
		if err != nil {
			return worker.Config{}, 0, errWithHints(fmt.Sprintf("invalid exit-if-idle: %s", opts.exitIfIdle),
				"Examples: "+codeStyle.Render("0s")+", "+codeStyle.Render("1m")+", "+codeStyle.Render("5m"))
		}
		if duration < 0 {
			return worker.Config{}, 0, errWithHints("exit-if-idle must not be negative",
				"Examples: "+codeStyle.Render("0s")+", "+codeStyle.Render("1m")+", "+codeStyle.Render("5m"))
		}
		resolved.ExitIfIdle = &duration
	}
	var err error
	resolved.TagFilter, err = parseTags(opts.filters)
	if err != nil {
		return worker.Config{}, 0, err
	}
	return resolved, timeout, nil
}
