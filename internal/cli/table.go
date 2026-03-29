package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/term"
)

const minTermWidth = 80

// TableConfig holds configuration for rendering a table.
type TableConfig struct {
	Headers []string
	Rows    [][]string
	Count   int  // total matching rows (for summary line)
	Offset  int  // current offset (for summary line)
	JSON    bool // output as JSON array
	CSV     bool // output as CSV
	Wrap    bool // wrap long lines instead of truncating
}

// PrintTable renders a table according to the config.
// For table output, it auto-detects terminal width and truncates columns to fit.
// Callers should pre-render any styled cell content (e.g. colored status) before passing rows;
// lipgloss table preserves ANSI codes through truncation and rendering.
func PrintTable(tc TableConfig) error {
	if tc.JSON {
		return printJSON(tc.Headers, tc.Rows)
	}
	if tc.CSV {
		return printCSV(tc.Headers, tc.Rows)
	}
	return printStyledTable(tc)
}

func printJSON(headers []string, rows [][]string) error {
	keys := make([]string, len(headers))
	for i, h := range headers {
		keys[i] = strings.ToLower(h)
	}
	objects := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		obj := make(map[string]string, len(keys))
		for i, k := range keys {
			if i < len(row) {
				obj[k] = row[i]
			}
		}
		objects = append(objects, obj)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(objects)
}

func printCSV(headers []string, rows [][]string) error {
	w := csv.NewWriter(os.Stdout)
	if err := w.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func getTermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w < minTermWidth {
		return minTermWidth
	}
	return w
}

func printStyledTable(tc TableConfig) error {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Width(getTermWidth()).
		Wrap(tc.Wrap).
		Headers(tc.Headers...).
		Rows(tc.Rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return rowStyle
		})

	fmt.Fprintln(os.Stdout, t)

	if tc.Count > 0 && len(tc.Rows) < tc.Count {
		fmt.Fprintf(os.Stderr, "%d/%d (use --limit to show more)\n", len(tc.Rows), tc.Count)
	}

	return nil
}
