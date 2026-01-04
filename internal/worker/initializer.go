package worker

import (
	"context"
	"encoding/json"
	"fmt"
)

// Initializer defines the interface for setting up the task environment.
type Initializer interface {
	Type() string
	Run(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error
}

// GetInitializer returns an Initializer implementation for the given type.
func GetInitializer(initType string) (Initializer, error) {
	switch initType {
	case "noop":
		return NoopInitializer{}, nil
	case "bash":
		return BashInitializer{}, nil
	default:
		return nil, fmt.Errorf("unknown init type: %s", initType)
	}
}

// NoopInitializer performs no initialization.
type NoopInitializer struct{}

func (NoopInitializer) Type() string { return "noop" }

func (NoopInitializer) Run(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error {
	return nil
}
