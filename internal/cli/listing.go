package cli

import (
	"time"

	"github.com/spf13/cobra"
)

// listingOptions defines the shared pagination, time filter, and output flags.
type listingOptions struct {
	limit, offset   int
	since           durationFlag
	json, csv, wrap bool
}

func (opts *listingOptions) addFlags(cmd *cobra.Command, sinceHelp string) {
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "Maximum number of results")
	cmd.Flags().IntVar(&opts.offset, "offset", 0, "Number of results to skip")
	opts.since.addFlag(cmd, "since", 0, sinceHelp)
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&opts.csv, "csv", false, "Output as CSV")
	cmd.Flags().BoolVar(&opts.wrap, "wrap", false, "Wrap table cells instead of truncating")
}

func (opts listingOptions) resolve(now time.Time) (time.Time, error) {
	if opts.json && opts.csv {
		return time.Time{}, errWithHints("cannot use both --json and --csv", "Choose one output format")
	}
	if opts.wrap && (opts.json || opts.csv) {
		return time.Time{}, errWithHints("--wrap requires table output", "Remove --wrap when using --json or --csv")
	}
	if opts.limit <= 0 {
		return time.Time{}, errWithHints("limit must be positive", "Example: "+codeStyle.Render("--limit 50"))
	}
	if opts.offset < 0 {
		return time.Time{}, errWithHints("offset must not be negative", "Example: "+codeStyle.Render("--offset 50"))
	}
	if !opts.since.set {
		return time.Time{}, nil
	}
	if opts.since.Duration <= 0 {
		return time.Time{}, errWithHints("since must be positive", "Examples: 1h, 24h, 7d")
	}
	return now.Add(-opts.since.Duration), nil
}
