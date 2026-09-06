// Package integrations prepares ordinary shell commands using independent tools.
package integrations

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Task is the execution information an integration may prepare.
type Task struct {
	ID, Command string
}

// Integration validates options and prepares a command. Runtime setup and
// finalization belong in that command; the worker has no integration hooks.
type Integration interface {
	Options() Schema
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
func (r Registry) Resolve(with []string, configured map[string]map[string]any, overrides []string) (*Plan, error) {
	names := []string{}
	for _, name := range with {
		if _, ok := r[name]; !ok {
			return nil, fmt.Errorf("unknown integration %q", name)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	options, err := r.Configure(configured)
	if err != nil {
		return nil, err
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
		spec, exists := module.Options()[key]
		if !exists {
			return nil, fmt.Errorf("unknown integration option %s", qualified)
		}
		if options[name] == nil {
			options[name] = Options{}
		}
		parsed, err := spec.parse(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", qualified, err)
		}
		options[name][key] = parsed
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

// Configure resolves defaults and validates supplied values without enabling tools.
func (r Registry) Configure(configured map[string]map[string]any) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any)
	for name := range configured {
		if _, ok := r[name]; !ok {
			return nil, fmt.Errorf("unknown integration %q in config", name)
		}
	}
	for name, module := range r {
		options, err := module.Options().Resolve(configured[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		result[name] = options
	}
	return result, nil
}
