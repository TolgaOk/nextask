package cli

import (
	"time"

	"github.com/spf13/cobra"
	str2duration "github.com/xhit/go-str2duration/v2"
)

// durationFlag uses the same units across commands and distinguishes an omitted
// flag from an explicit zero, which can have different lifecycle semantics.
type durationFlag struct {
	time.Duration
	set bool
}

func (v *durationFlag) Set(raw string) error {
	d, err := str2duration.ParseDuration(raw)
	if err != nil {
		return err
	}
	v.Duration, v.set = d, true
	return nil
}

func (v *durationFlag) String() string { return v.Duration.String() }
func (*durationFlag) Type() string     { return "duration" }

func (v *durationFlag) addFlag(cmd *cobra.Command, name string, def time.Duration, help string) {
	v.Duration = def
	cmd.Flags().Var(v, name, help+" (e.g., 30s, 1h, 7d)")
	if def == 0 {
		// Omission can differ from explicit 0s; describe that in each flag's help.
		cmd.Flags().Lookup(name).DefValue = ""
	}
}
