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
		globalPath, err := config.GlobalPath()
		if err != nil {
			return err
		}
		localPath := config.LocalPath()

		printConfigFile("Global config:", globalPath)
		fmt.Println()
		printConfigFile("Local config:", localPath)
		return nil
	},
}

func printConfigFile(label, path string) {
	fmt.Printf("%s %s\n", hintStyle.Render(label), codeStyle.Render(path))

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		fmt.Println(hintStyle.Render("  (file not found)"))
		return
	}
	if err != nil {
		fmt.Printf("  %s\n", errStyle.Render(err.Error()))
		return
	}
	defer file.Close()

	fmt.Println()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fmt.Println(formatTOMLLine(scanner.Text()))
	}
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
