// Package cli implements the nextask command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	hintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	codeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
)

type exitCodeError struct {
	code int
}

func (e *exitCodeError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

type hintedError struct {
	err   error
	hints []string
}

func (e *hintedError) Error() string {
	return e.err.Error()
}

func (e *hintedError) Unwrap() error {
	return e.err
}

func withHints(err error, hints ...string) error {
	return &hintedError{err: err, hints: hints}
}

func errWithHints(msg string, hints ...string) error {
	return &hintedError{err: errors.New(msg), hints: hints}
}

func errDBRequired() error {
	return errWithHints("database URL is required",
		"Set db.url with environment references or supply NEXTASK_DB_URL on the CLI host and each worker",
	)
}

// NewRootCommand constructs an independent command tree. Create a fresh tree
// for each execution; Cobra command instances themselves are not concurrent.
func NewRootCommand(version string) *cobra.Command {
	return newRootCommand(version, config.Load)
}

func newRootCommand(version string, loadConfig func() (*config.Config, error)) *cobra.Command {
	// Commands read this tree's configuration. Per-command overrides use copies.
	cfg := new(config.Config)
	root := &cobra.Command{
		Use:           "nextask",
		Short:         "Distributed task queue with optional integrations and full log capture",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := loadConfig()
			if err != nil {
				return withHints(err,
					"Check TOML syntax and values in your config files",
					"Shared: "+codeStyle.Render("~/.config/tasktools/config.toml or .tasktools.toml"),
					"Global: "+codeStyle.Render("~/.config/nextask/global.toml"),
					"Local:  "+codeStyle.Render(".nextask.toml"))
			}
			*cfg = *loaded
			return nil
		},
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.AddCommand(newEnqueueCommand(cfg), newWaitCommand(cfg), newLogsCommand(cfg),
		newCancelCommand(cfg), newListCommand(cfg), newShowCommand(cfg), newRemoveCommand(cfg),
		newWorkerCommand(cfg), newConfigCommand(cfg), newInitCommand(cfg), newRuntimeCommand())
	return root
}

// Execute constructs and runs the CLI with the supplied build version.
func Execute(version string) {
	if err := NewRootCommand(version).Execute(); err != nil {
		var ec *exitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		printError(os.Stderr, err)
		os.Exit(1)
	}
}

func printError(out io.Writer, err error) error {
	if _, writeErr := fmt.Fprintln(out, errStyle.Render("Error: ")+err.Error()); writeErr != nil {
		return writeErr
	}

	hints := getErrorHints(err)
	for _, hint := range hints {
		if _, err := fmt.Fprintln(out, hintStyle.Render("  → ")+hint); err != nil {
			return err
		}
	}
	return nil
}

func getErrorHints(err error) []string {
	var he *hintedError
	if errors.As(err, &he) {
		return he.hints
	}

	switch {
	case errors.Is(err, db.ErrDBNotExist):
		return []string{
			"Create database: " + codeStyle.Render("createdb <dbname>"),
			"Set NEXTASK_DB_URL, then run " + codeStyle.Render("nextask init db"),
		}
	case errors.Is(err, db.ErrConnRefused):
		return []string{
			"Is PostgreSQL running?",
			"macOS: " + codeStyle.Render("brew services start postgresql"),
			"Linux: " + codeStyle.Render("sudo systemctl start postgresql"),
		}
	case errors.Is(err, db.ErrAuthFailed):
		return []string{"Check your database credentials"}
	case errors.Is(err, db.ErrNotInitialized):
		return []string{
			"Set NEXTASK_DB_URL, then run " + codeStyle.Render("nextask init db"),
		}
	}

	return nil
}
