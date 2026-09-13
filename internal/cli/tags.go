package cli

import (
	"fmt"
	"slices"
	"strings"
)

func parseTags(tags []string) (map[string]string, error) {
	parsed := make(map[string]string, len(tags))
	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) != 2 {
			return nil, errWithHints(fmt.Sprintf("invalid tag format: %s", tag),
				"Expected format: "+codeStyle.Render("key=value"),
			)
		}
		if parts[0] == "" || parts[1] == "" {
			return nil, errWithHints(fmt.Sprintf("invalid tag format: %s", tag),
				"Tag key and value must not be empty",
				"Expected format: "+codeStyle.Render("key=value"),
			)
		}
		parsed[parts[0]] = parts[1]
	}
	return parsed, nil
}

func sortedTags(tags map[string]string) []string {
	parts := make([]string, 0, len(tags))
	for key, value := range tags {
		parts = append(parts, key+"="+value)
	}
	slices.Sort(parts)
	return parts
}
