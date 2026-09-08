package taskexec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWaitsForBackgroundChildren(t *testing.T) {
	dir := t.TempDir()
	result := Run(context.Background(), Command{Text: "(sleep 0.1; echo done > artifact) &", Dir: dir})
	if result.Code != 0 {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "artifact"))
	if err != nil || string(data) != "done\n" {
		t.Fatalf("child not finished: %s %v", data, err)
	}
}
func TestRunCancellationWithCleanup(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	done := make(chan *Result, 1)
	go func() {
		done <- Run(ctx, Command{Text: "trap 'sleep 0.1; echo saved; exit 0' INT; echo ready > ready; while :; do sleep 1; done", Dir: dir, Stdout: &out, CleanupTimeout: time.Second})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("command not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case result := <-done:
		if result.Code == 0 || !strings.Contains(out.String(), "saved") {
			t.Fatalf("cleanup missing: %v %s", result, &out)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancel did not finish")
	}
}
