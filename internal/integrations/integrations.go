// Package integrations prepares ordinary shell commands using independent tools.
package integrations

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Task is the execution information an integration may prepare.
type Task struct{ ID, Command string }

// Options contains string arguments owned by one integration.
type Options map[string]string

// Integration validates options and prepares a command. Runtime setup and
// finalization belong in that command; the worker has no integration hooks.
type Integration interface {
	Options() []string
	Validate(Options) error
	Prepare(context.Context, Task, Options) (Task, error)
}

type Registry map[string]Integration

func Builtins() Registry { return Registry{"git": Git{Repo: "."}} }

type step struct {
	name    string
	module  Integration
	options Options
}
type Plan struct{ steps []step }

// Resolve validates explicitly selected integrations and their options.
// Configuration supplies options; it never enables an integration.
func (r Registry) Resolve(with []string, configured map[string]map[string]string, overrides []string) (*Plan, error) {
	names := []string{}
	for _, name := range with {
		if _, ok := r[name]; !ok {
			return nil, fmt.Errorf("unknown integration %q", name)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	options := map[string]Options{}
	for name, values := range configured {
		module, ok := r[name]
		if !ok {
			return nil, fmt.Errorf("unknown integration %q in config", name)
		}
		options[name] = Options{}
		for key, value := range values {
			if !slices.Contains(module.Options(), key) {
				return nil, fmt.Errorf("unknown integration option %s.%s", name, key)
			}
			options[name][key] = value
		}
	}
	for _, override := range overrides {
		qualified, value, ok := strings.Cut(override, "=")
		name, key, qualifiedOK := strings.Cut(qualified, ".")
		if !ok || !qualifiedOK || name == "" || key == "" {
			return nil, fmt.Errorf("invalid --set %q: use TOOL.KEY=VALUE", override)
		}
		module, ok := r[name]
		if !ok {
			return nil, fmt.Errorf("unknown integration %q", name)
		}
		if !slices.Contains(names, name) {
			return nil, fmt.Errorf("--set %s requires enabled integration %q", qualified, name)
		}
		if !slices.Contains(module.Options(), key) {
			return nil, fmt.Errorf("unknown integration option %s", qualified)
		}
		if options[name] == nil {
			options[name] = Options{}
		}
		options[name][key] = value
	}
	plan := &Plan{}
	for _, name := range names {
		if options[name] == nil {
			options[name] = Options{}
		}
		if err := r[name].Validate(options[name]); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		plan.steps = append(plan.steps, step{name, r[name], options[name]})
	}
	return plan, nil
}

// Prepare applies wrappers in reverse so the first selected integration runs
// outermost. Published resources remain owned by their tools on later failure.
func (p *Plan) Prepare(ctx context.Context, task Task) (Task, error) {
	id := task.ID
	for i := len(p.steps) - 1; i >= 0; i-- {
		s := p.steps[i]
		var err error
		task, err = s.module.Prepare(ctx, task, s.options)
		if err != nil {
			return Task{}, fmt.Errorf("%s: %w", s.name, err)
		}
		if task.ID != id {
			return Task{}, fmt.Errorf("%s changed task identity", s.name)
		}
	}
	return task, nil
}

// Quote preserves a literal argument through a POSIX shell, including newlines.
func Quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
