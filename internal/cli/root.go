// Package cli implements the nextask command-line interface.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var dbURL string
var cfg *config.Config

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
		"Provide "+codeStyle.Render("--db-url \"postgres://user@localhost:5432/dbname\""),
		"Or set "+codeStyle.Render("NEXTASK_DB_URL")+" environment variable",
		"Or set "+codeStyle.Render("db.url")+" in "+codeStyle.Render(".nextask.toml")+" (project) or global config",
	)
}

// SetVersion configures the version string displayed by --version.
func SetVersion(v string) {
	RootCmd.Version = v
}

// RootCmd is the base command for the nextask CLI.
var RootCmd = &cobra.Command{
	Use:   "nextask",
	Short: "Distributed task queue with source snapshotting and full log capture",
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load()
		if err != nil {
			return withHints(err,
				"Check TOML syntax in your config files",
				"Global: "+codeStyle.Render("~/.config/nextask/global.toml"),
				"Local:  "+codeStyle.Render(".nextask.toml"),
			)
		}
		// Apply persistent flag
		if dbURL != "" {
			cfg.DB.URL = dbURL
		}
		return nil
	},
}

// Execute runs the root command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		var ec *exitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		printError(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVar(&dbURL, "db-url", "", "PostgreSQL connection URL")
	RootCmd.CompletionOptions.HiddenDefaultCmd = true
}

func printError(err error) {
	fmt.Fprintln(os.Stderr, errStyle.Render("Error: ")+err.Error())

	hints := getErrorHints(err)
	for _, hint := range hints {
		fmt.Fprintln(os.Stderr, hintStyle.Render("  → ")+hint)
	}
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
			"Then: " + codeStyle.Render("nextask init db --db-url ..."),
			"Or:   " + codeStyle.Render("nextask init db") + " (with config file)",
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
			"Run: " + codeStyle.Render("nextask init db --db-url ..."),
			"Or:  " + codeStyle.Render("nextask init db") + " (with config file)",
		}
	}

	return nil
}
