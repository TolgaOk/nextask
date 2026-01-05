package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Source defines the interface for fetching task source code.
type Source interface {
	Type() string
	Fetch(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error
}

// GetSource returns a Source implementation for the given type.
func GetSource(sourceType string) (Source, error) {
	switch sourceType {
	case "noop":
		return NoopSource{}, nil
	case "git":
		return GitSource{}, nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceType)
	}
}

// NoopSource creates an empty task directory without fetching any source.
type NoopSource struct{}

func (NoopSource) Type() string { return "noop" }

func (NoopSource) Fetch(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error {
	return os.MkdirAll(taskDir, 0755)
}
