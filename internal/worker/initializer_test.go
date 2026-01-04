package worker

import (
	"context"
	"encoding/json"
	"testing"
)

func TestGetInitializer_Noop(t *testing.T) {
	init, err := GetInitializer("noop")
	if err != nil {
		t.Fatalf("GetInitializer(noop) error = %v", err)
	}
	if init.Type() != "noop" {
		t.Errorf("Type() = %s, want noop", init.Type())
	}
}

func TestGetInitializer_Bash(t *testing.T) {
	init, err := GetInitializer("bash")
	if err != nil {
		t.Fatalf("GetInitializer(bash) error = %v", err)
	}
	if init.Type() != "bash" {
		t.Errorf("Type() = %s, want bash", init.Type())
	}
}

func TestGetInitializer_Unknown(t *testing.T) {
	_, err := GetInitializer("unknown")
	if err == nil {
		t.Error("GetInitializer(unknown) expected error, got nil")
	}
}

func TestNoopInitializer_Run(t *testing.T) {
	init := NoopInitializer{}
	log := &testLogger{}

	err := init.Run(context.Background(), nil, "/tmp/test", log)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestBashInitializer_Run_InvalidConfig(t *testing.T) {
	init := BashInitializer{}
	log := &testLogger{}

	err := init.Run(context.Background(), json.RawMessage(`invalid`), "/tmp/test", log)
	if err == nil {
		t.Error("Run() with invalid JSON expected error, got nil")
	}
}

func TestBashInitializer_Run_MissingScript(t *testing.T) {
	init := BashInitializer{}
	log := &testLogger{}

	cfg := json.RawMessage(`{}`)
	err := init.Run(context.Background(), cfg, "/tmp/test", log)
	if err == nil {
		t.Error("Run() with missing script expected error, got nil")
	}
}
