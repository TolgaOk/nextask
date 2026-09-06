package integrations

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeIntegration struct {
	name  string
	calls *[]string
	fail  bool
}

func (f fakeIntegration) Options() Schema { return Schema{"value": {Kind: String, Default: ""}} }
func (f fakeIntegration) Validate(o Options) error {
	if o["value"] == "invalid" {
		return errors.New("invalid value")
	}
	return nil
}
func (f fakeIntegration) Prepare(_ context.Context, task Task, o Options) (Task, error) {
	*f.calls = append(*f.calls, f.name+":"+o.String("value"))
	if f.fail {
		return Task{}, errors.New("prepare failed")
	}
	task.Command = f.name + "(" + task.Command + ")"
	return task, nil
}

func TestSelectionAndComposition(t *testing.T) {
	calls := []string{}
	registry := Registry{"first": fakeIntegration{"first", &calls, false}, "second": fakeIntegration{"second", &calls, false}}
	settings := map[string]map[string]any{"first": {"value": "configured"}}
	plan, err := registry.Resolve([]string{"first", "first", "second"}, settings, []string{"first.value=override=with=equals"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.Prepare(context.Background(), Task{ID: "unchanged", Command: "job"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "unchanged" || task.Command != "first(second(job))" {
		t.Fatalf("task = %+v", task)
	}
	if !reflect.DeepEqual(calls, []string{"second:", "first:override=with=equals"}) {
		t.Fatalf("order = %v", calls)
	}
	if settings["first"]["value"] != "configured" {
		t.Fatal("selection mutated config")
	}
	plan, err = registry.Resolve(nil, settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = plan.Prepare(context.Background(), Task{ID: "plain", Command: "job"})
	if err != nil || task.Command != "job" {
		t.Fatalf("config enabled an integration: %+v %v", task, err)
	}
}

func TestValidateBeforePreparation(t *testing.T) {
	calls := []string{}
	registry := Registry{"git": fakeIntegration{"git", &calls, false}}
	for _, tc := range []struct {
		with, overrides []string
		config          map[string]map[string]any
	}{
		{with: []string{"unknown"}},
		{with: []string{"git"}, overrides: []string{"broken"}},
		{overrides: []string{"git.value=x"}},
		{with: []string{"git"}, overrides: []string{"git.unknown=x"}},
		{with: []string{"git"}, overrides: []string{"git.value=invalid"}},
		{config: map[string]map[string]any{"git": {"unknown": "x"}}},
	} {
		if _, err := registry.Resolve(tc.with, tc.config, tc.overrides); err == nil {
			t.Errorf("invalid selection accepted: %+v", tc)
		}
	}
	if len(calls) != 0 {
		t.Fatal("validation prepared resources")
	}
}

func TestPreparationFailureStopsComposition(t *testing.T) {
	calls := []string{}
	registry := Registry{"outer": fakeIntegration{"outer", &calls, false}, "inner": fakeIntegration{"inner", &calls, true}}
	plan, err := registry.Resolve([]string{"outer", "inner"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Prepare(context.Background(), Task{ID: "task", Command: "job"}); err == nil {
		t.Fatal("preparation error swallowed")
	}
	if !reflect.DeepEqual(calls, []string{"inner:"}) {
		t.Fatalf("continued after failure: %v", calls)
	}
}
