package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/term"
)

const minTermWidth = 80

// TableConfig holds configuration for rendering a table.
type TableConfig struct {
	EmptyMessage string // human output only; JSON and CSV keep their schemas
	Headers      []string
	Rows         [][]string
	Count        int  // total matching rows (for summary line)
	Offset       int  // current offset (for summary line)
	JSON         bool // output as JSON array
	CSV          bool // output as CSV
	Wrap         bool // wrap long lines instead of truncating
}

// PrintTable renders a table according to the config.
// For table output, it auto-detects terminal width and truncates columns to fit.
// Callers should pre-render any styled cell content (e.g. colored status) before passing rows;
// lipgloss table preserves ANSI codes through truncation and rendering.
func PrintTable(out commandOutput, tc TableConfig) error {
	if tc.JSON {
		return printJSON(out.out, tc.Headers, tc.Rows)
	}
	if tc.CSV {
		return printCSV(out.out, tc.Headers, tc.Rows)
	}
	if len(tc.Rows) == 0 && tc.EmptyMessage != "" {
		_, err := fmt.Fprintln(out.err, tc.EmptyMessage)
		return err
	}
	return printStyledTable(out, tc)
}

func printJSON(out io.Writer, headers []string, rows [][]string) error {
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
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(objects)
}

func printCSV(out io.Writer, headers []string, rows [][]string) error {
	w := csv.NewWriter(out)
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

func getTermWidth(out io.Writer) int {
	file, ok := out.(interface{ Fd() uintptr })
	if !ok {
		return minTermWidth
	}
	w, _, err := term.GetSize(file.Fd())
	if err != nil || w < minTermWidth {
		return minTermWidth
	}
	return w
}

func printStyledTable(out commandOutput, tc TableConfig) error {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Width(getTermWidth(out.out)).
		Wrap(tc.Wrap).
		Headers(tc.Headers...).
		Rows(tc.Rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return rowStyle
		})

	if _, err := fmt.Fprintln(out.out, t); err != nil {
		return err
	}

	if tc.Count > 0 && len(tc.Rows) < tc.Count {
		if tc.Offset > 0 {
			_, err := fmt.Fprintf(out.err, "%d-%d/%d (use --offset to page through results)\n", tc.Offset+1, tc.Offset+len(tc.Rows), tc.Count)
			return err
		}
		_, err := fmt.Fprintf(out.err, "%d/%d (use --offset to page through results)\n", len(tc.Rows), tc.Count)
		return err
	}

	return nil
}
