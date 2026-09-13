package cli

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func buildTestCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "nextask")
	build := exec.Command("go", "build", "-race", "-o", binary, "../../cmd/nextask")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	return binary
}
