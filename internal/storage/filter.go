package storage

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ValidatePatterns checks portable, relative doublestar patterns.
func ValidatePatterns(groups ...[]string) error {
	for _, patterns := range groups {
		for _, pattern := range patterns {
			if pattern == "" || path.IsAbs(pattern) || strings.ContainsAny(pattern, "\x00\\") || !doublestar.ValidatePattern(pattern) {
				return fmt.Errorf("invalid relative file pattern %q", pattern)
			}
			for _, part := range strings.Split(pattern, "/") {
				if part == ".." {
					return fmt.Errorf("file patterns cannot traverse parent directories")
				}
			}
		}
	}
	return nil
}

func reserved(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".nextask") {
			return true
		}
	}
	return false
}
func matches(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, _ := doublestar.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

func excluded(patterns []string, name string) bool {
	for {
		if matches(patterns, name) {
			return true
		}
		name = path.Dir(name)
		if name == "." {
			return false
		}
	}
}
