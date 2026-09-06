package db

import (
	"context"
	"fmt"
	"maps"
	"testing"
)

func TestTagFiltersPreserveJSONValues(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	ctx := context.Background()
	cases := []map[string]string{
		{`quoted"key`: `a"b`},
		{`path\key`: `C:\temp\file`},
		{"line": "first\nsecond\tthird"},
		{"unicode-雪": "λ café"},
		{"fragment": `x", "extra": "injected`},
		{"empty": ""},
	}
	for i, tags := range cases {
		tags["group"] = "selected"
		id := fmt.Sprintf("escaped-%d", i)
		wanted := &Task{ID: id, Command: "true", Status: StatusPending, Tags: tags}
		if err := CreateTask(ctx, pool, wanted); err != nil {
			t.Fatal(err)
		}
		// The entire filter must match, even when one literal value resembles JSON.
		decoy := maps.Clone(tags)
		decoy["group"] = "another"
		if err := CreateTask(ctx, pool, &Task{ID: id + "-decoy", Command: "true", Status: StatusPending, Tags: decoy}); err != nil {
			t.Fatal(err)
		}
		filter := ListFilter{Tags: tags, Statuses: []string{"pending"}}
		listed, err := ListTasks(ctx, pool, filter)
		if err != nil || len(listed) != 1 || listed[0].ID != id {
			t.Fatalf("literal filter %q: tasks=%+v err=%v", tags, listed, err)
		}
		count, err := CountTasks(ctx, pool, filter)
		if err != nil || count != 1 {
			t.Fatalf("count for %q: %d %v", tags, count, err)
		}
		claimed, err := ClaimTask(ctx, pool, "tag-worker", nil, tags)
		if err != nil || claimed == nil || claimed.ID != id {
			t.Fatalf("list and claim disagree for %q: %+v %v", tags, claimed, err)
		}
		if count, err := CountTasks(ctx, pool, filter); err != nil || count != 0 {
			t.Fatalf("status and tag filters did not combine: %d %v", count, err)
		}
	}
	if err := CreateTask(ctx, pool, &Task{ID: "untagged", Command: "true", Status: StatusPending}); err != nil {
		t.Fatal(err)
	}
	for _, tags := range []map[string]string{nil, {}} {
		filter := ListFilter{Tags: tags}
		listed, err := ListTasks(ctx, pool, filter)
		if err != nil || len(listed) != 2*len(cases)+1 {
			t.Fatalf("empty filter excluded tasks: %d %v", len(listed), err)
		}
		count, err := CountTasks(ctx, pool, filter)
		if err != nil || count != len(listed) {
			t.Fatalf("empty-filter list/count disagree: %d %v", count, err)
		}
	}
}
