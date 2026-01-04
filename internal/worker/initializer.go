package worker

import (
	"context"
	"encoding/json"
	"fmt"
)

type Initializer interface {
	Type() string
	Run(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error
}

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

type NoopInitializer struct{}

func (NoopInitializer) Type() string { return "noop" }

func (NoopInitializer) Run(ctx context.Context, config json.RawMessage, taskDir string, log Logger) error {
	return nil
}
