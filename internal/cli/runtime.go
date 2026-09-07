package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/TolgaOk/nextask/internal/integrations"
	"github.com/spf13/cobra"
)

// Runtime dispatch is internal to prepared tasks. It deliberately uses only the
// queued options and worker environment, rather than reloading project config.
func newRuntimeCommand() *cobra.Command {
	return &cobra.Command{
		Use: "_run INTEGRATION OPTIONS COMMAND CLEANUP_MS", Hidden: true, Args: cobra.ExactArgs(4),
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			module, ok := integrations.Builtins()[args[0]]
			if !ok {
				return fmt.Errorf("unknown runtime integration %q", args[0])
			}
			runtime, ok := module.(integrations.Runtime)
			if !ok {
				return fmt.Errorf("integration %q has no runtime", args[0])
			}
			var raw integrations.Options
			decoder := json.NewDecoder(strings.NewReader(args[1]))
			decoder.UseNumber()
			if err := decoder.Decode(&raw); err != nil {
				return fmt.Errorf("invalid runtime options")
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return fmt.Errorf("invalid runtime options")
			}
			options, err := module.Options().Resolve(raw)
			if err != nil {
				return err
			}
			if err := module.Validate(options); err != nil {
				return err
			}
			cleanupMS, err := strconv.ParseInt(args[3], 10, 64)
			if err != nil || cleanupMS < 0 || cleanupMS > (24*time.Hour).Milliseconds() {
				return fmt.Errorf("invalid runtime cleanup timeout")
			}
			id := os.Getenv("NEXTASK_TASK_ID")
			if err := db.ValidateTaskID(id); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			result := runtime.Run(ctx, integrations.Task{ID: id, Command: args[2], CleanupTimeout: time.Duration(cleanupMS) * time.Millisecond}, options, integrations.IO{In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()})
			if result.Err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "nextask:", result.Err)
			}
			if result.Code != 0 {
				return &exitCodeError{code: result.ShellCode()}
			}
			return nil
		},
	}
}
