package db

import (
	"strings"
	"testing"
)

func TestValidateTaskID(t *testing.T) {
	for _, id := range []string{"a", "task_123-ABC", "550e8400-e29b-41d4-a716-446655440000", strings.Repeat("a", 53)} {
		if err := ValidateTaskID(id); err != nil {
			t.Errorf("valid ID %q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"", ".", "..", "../task", "a/b", "a\\b", "-task", "_task", "a.lock", "a b", "a\n", `a"`, "é", strings.Repeat("a", 54)} {
		if err := ValidateTaskID(id); err == nil {
			t.Errorf("unsafe ID %q accepted", id)
		}
	}
}
