package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGetSource_Noop(t *testing.T) {
	src, err := GetSource("noop")
	if err != nil {
		t.Fatalf("GetSource(noop) error = %v", err)
	}
	if src.Type() != "noop" {
		t.Errorf("Type() = %s, want noop", src.Type())
	}
}

func TestGetSource_Git(t *testing.T) {
	src, err := GetSource("git")
	if err != nil {
		t.Fatalf("GetSource(git) error = %v", err)
	}
	if src.Type() != "git" {
		t.Errorf("Type() = %s, want git", src.Type())
	}
}

func TestGetSource_Unknown(t *testing.T) {
	_, err := GetSource("unknown")
	if err == nil {
		t.Error("GetSource(unknown) expected error, got nil")
	}
}

func TestNoopSource_Fetch(t *testing.T) {
	tmpDir := t.TempDir()
	taskDir := filepath.Join(tmpDir, "task1")

	src := NoopSource{}
	log := &testLogger{}

	err := src.Fetch(context.Background(), nil, taskDir, log)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(taskDir); os.IsNotExist(err) {
		t.Error("taskDir was not created")
	}
}

func TestGitSource_Fetch_InvalidConfig(t *testing.T) {
	src := GitSource{}
	log := &testLogger{}

	err := src.Fetch(context.Background(), json.RawMessage(`invalid`), "/tmp/test", log)
	if err == nil {
		t.Error("Fetch() with invalid JSON expected error, got nil")
	}
}

func TestGitSource_Fetch_MissingRemote(t *testing.T) {
	src := GitSource{}
	log := &testLogger{}

	cfg := json.RawMessage(`{"ref":"refs/nextask/test"}`)
	err := src.Fetch(context.Background(), cfg, "/tmp/test", log)
	if err == nil {
		t.Error("Fetch() with missing remote expected error, got nil")
	}
}

func TestGitSource_Fetch_MissingRef(t *testing.T) {
	src := GitSource{}
	log := &testLogger{}

	cfg := json.RawMessage(`{"remote":"origin"}`)
	err := src.Fetch(context.Background(), cfg, "/tmp/test", log)
	if err == nil {
		t.Error("Fetch() with missing ref expected error, got nil")
	}
}

type testLogger struct {
	logs []string
}

func (l *testLogger) Log(stream, data string) {
	l.logs = append(l.logs, stream+": "+data)
}
