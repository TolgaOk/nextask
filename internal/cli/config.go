package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/TolgaOk/nextask/internal/config"
	"github.com/spf13/cobra"
)

var tomlSectionStyle = lipgloss.NewStyle().Bold(true)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.GlobalPath()
		if err != nil {
			return err
		}

		fmt.Printf("%s %s\n", hintStyle.Render("Config file:"), codeStyle.Render(path))

		file, err := os.Open(path)
		if os.IsNotExist(err) {
			fmt.Println(hintStyle.Render("(file not found)"))
			return nil
		}
		if err != nil {
			return err
		}
		defer file.Close()

		fmt.Println()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fmt.Println(formatTOMLLine(scanner.Text()))
		}
		return scanner.Err()
	},
}

func formatTOMLLine(line string) string {
	trimmed := strings.TrimSpace(line)

	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return tomlSectionStyle.Render(line)
	}

	return line
}

func init() {
	RootCmd.AddCommand(configCmd)
}
