package cli

import (
	"io"
	"sync"

	"github.com/spf13/cobra"
)

// commandOutput shares one lock across both streams, including when callers use
// the same buffer for stdout and stderr. Worker diagnostics may arrive concurrently.
type commandOutput struct {
	out io.Writer
	err io.Writer
}

func outputFor(cmd *cobra.Command) commandOutput {
	mu := new(sync.Mutex)
	return commandOutput{
		out: &lockedWriter{mu, cmd.OutOrStdout()},
		err: &lockedWriter{mu, cmd.ErrOrStderr()},
	}
}

type lockedWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

func (w *lockedWriter) Fd() uintptr {
	if file, ok := w.writer.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return ^uintptr(0)
}
