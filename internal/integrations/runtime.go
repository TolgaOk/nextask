package integrations

import (
	"context"
	"encoding/json"
	"io"
	"strconv"

	"github.com/TolgaOk/nextask/internal/taskexec"
)

// Runtime is implemented by integrations whose prepared command uses this binary.
// Credentials are resolved inside Run, on the worker, and never serialized here.
type Runtime interface {
	RuntimeOptions() Schema
	Run(context.Context, Task, Options, IO) *taskexec.Result
}
type IO struct {
	In       io.Reader
	Out, Err io.Writer
}

func runtimeCommand(name string, task Task, options Options) (string, error) {
	encoded, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	return `exec "${NEXTASK_EXECUTABLE:?nextask worker runtime is required}" _run ` + Quote(name) + " " + Quote(string(encoded)) + " " + Quote(task.Command) + " " + Quote(strconv.FormatInt(task.CleanupTimeout.Milliseconds(), 10)), nil
}
