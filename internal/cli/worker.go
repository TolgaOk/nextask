package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/worker"
	"github.com/spf13/cobra"
)

type workerOptions struct {
	workdir    string
	once       bool
	daemon     bool
	rm         bool
	exitIfIdle durationFlag
	id         string
	timeout    durationFlag
	readyFD    int
	filters    []string
}

func newWorkerCommand(cfg *config.Config) *cobra.Command {
	var opts workerOptions
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Start a worker to process tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, timeout, err := opts.resolve(*cfg)
			if err != nil {
				return err
			}
			if cfg.DB.URL == "" {
				return errDBRequired()
			}

			out := outputFor(cmd)
			resolved.Stderr = out.err
			ctx, stop := interruptContext(cmd.Context())
			defer stop()
			if opts.daemon {
				return daemonize(ctx, resolved, timeout)
			}
			if opts.readyFD >= 0 {
				pipe := os.NewFile(uintptr(opts.readyFD), "daemon-ready")
				defer pipe.Close()
				resolved.Ready = func() error {
					_, err := pipe.Write([]byte{1})
					pipe.Close() // Close before any payload can inherit the descriptor.
					return err
				}
			}
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
	cmd.Flags().StringVar(&opts.workdir, "workdir", "", "Override the configured base directory for task execution")
	cmd.Flags().BoolVar(&opts.once, "once", false, "Run at most one task and exit")
	cmd.Flags().BoolVar(&opts.rm, "rm", false, "Remove task workdir after completion")
	cmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Start in background; wait up to 30s for readiness")
	opts.timeout.addFlag(cmd, "timeout", 0, "Stop worker after duration; omit to disable")
	opts.exitIfIdle.addFlag(cmd, "exit-if-idle", 0, "Exit after duration without a claim; omit to disable, 0s exits when idle")
	cmd.Flags().StringSliceVar(&opts.filters, "tag", nil, "Only claim tasks matching tag key=value (repeatable)")
	// Both names share one slice value, so mixing aliases preserves all tags.
	cmd.Flags().Var(cmd.Flags().Lookup("tag").Value, "filter", "Alias for --tag")
	cmd.Flags().MarkHidden("filter")
	cmd.Flags().StringVar(&opts.id, "_id", "", "Worker ID (internal use)")
	cmd.Flags().MarkHidden("_id")
	cmd.Flags().IntVar(&opts.readyFD, "_ready-fd", -1, "Daemon startup pipe (internal use)")
	cmd.Flags().MarkHidden("_ready-fd")
	cmd.AddCommand(newWorkerListCommand(cfg), newWorkerStopCommand(cfg))
	return cmd
}

// resolve validates flags before either starting a worker or spawning a daemon.
func (opts workerOptions) resolve(cfg config.Config) (worker.Config, time.Duration, error) {
	if opts.readyFD != -1 && (opts.readyFD != 3 || opts.daemon) {
		return worker.Config{}, 0, fmt.Errorf("invalid daemon startup descriptor")
	}
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
	if opts.timeout.set && opts.timeout.Duration <= 0 {
		return worker.Config{}, 0, errWithHints("timeout must be positive", "Omit --timeout to disable the worker deadline")
	}
	if opts.exitIfIdle.set {
		if opts.exitIfIdle.Duration < 0 {
			return worker.Config{}, 0, errWithHints("exit-if-idle must not be negative", "Use 0s to exit immediately when idle")
		}
		resolved.ExitIfIdle = &opts.exitIfIdle.Duration
	}
	var err error
	resolved.TagFilter, err = parseTags(opts.filters)
	if err != nil {
		return worker.Config{}, 0, err
	}
	return resolved, opts.timeout.Duration, nil
}
