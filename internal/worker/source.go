package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type Source interface {
	Type() string
	Fetch(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error
}

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

type NoopSource struct{}

func (NoopSource) Type() string { return "noop" }

func (NoopSource) Fetch(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error {
	return os.MkdirAll(taskDir, 0755)
}
