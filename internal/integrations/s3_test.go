package integrations

import (
	"context"
	"strings"
	"testing"
	"time"
)

func s3Options() Options {
	return Options{"endpoint": "https://fsn1.your-objectstorage.com", "remote": "s3://bucket/project", "include": []string{"outputs/**"}}
}
func TestS3ValidationAndOverrides(t *testing.T) {
	for key, values := range map[string][]any{
		"endpoint":    {"", "https://name:secret@host", "https://host/path", "https://host?secret=value", "host"},
		"remote":      {"", "s3://", "s3://user:secret@bucket/path", "s3://bucket/../other"},
		"root":        {"/absolute", "../parent", ".git", "outputs/.nextask"},
		"include":     {[]string{}, []string{"[broken"}, []string{"../outside"}, []any{"good", true}, "outputs/**"},
		"concurrency": {int64(0), "4", true}, "interval": {"-1s", "forever"}, "final_timeout": {"0s", "25h"},
		"final_sync": {"true"}, "max_file_size": {"-1", "0", "invalid"}, "symlinks": {"follow"}, "on_final_error": {"ignore"},
	} {
		for _, value := range values {
			o := s3Options()
			o[key] = value
			if err := (S3{}).Validate(o); err == nil {
				t.Errorf("accepted invalid %s: %v", key, value)
			}
		}
	}
	r := Builtins()
	configured := map[string]map[string]any{"s3": s3Options()}
	plan, err := r.Resolve([]string{"s3"}, configured, []string{`s3.include=["reports/**","file with spaces"]`, "s3.interval=0s", "s3.concurrency=2", "s3.final_sync=true"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Prepare(context.Background(), Task{ID: "task", Command: `echo 'a'; printf '%s' "$NEXTASK_TASK_ID"`})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.CleanupTimeout != 2*time.Minute || !strings.Contains(prepared.Command, "NEXTASK_EXECUTABLE") {
		t.Fatalf("unprepared: %+v", prepared)
	}
	if configured["s3"]["include"].([]string)[0] != "outputs/**" {
		t.Fatal("override mutated config")
	}
	for _, override := range []string{`s3.include=null`, `s3.include=[1]`, "s3.concurrency=no", "s3.final_sync=yes", "s3.secret_key=secret"} {
		if _, err := r.Resolve([]string{"s3"}, configured, []string{override}); err == nil {
			t.Errorf("accepted %s", override)
		}
	}
	plain, err := r.Resolve(nil, configured, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := plain.Prepare(context.Background(), Task{ID: "plain", Command: "true"})
	if err != nil || task.Command != "true" || task.CleanupTimeout != 0 {
		t.Fatal("config activated S3")
	}
}
func TestS3PreparationDoesNotReadCredentials(t *testing.T) {
	t.Setenv("S3_ACCESS_KEY", "")
	t.Setenv("S3_SECRET_KEY", "")
	plan, err := Builtins().Resolve([]string{"s3"}, map[string]map[string]any{"s3": s3Options()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Prepare(context.Background(), Task{ID: "task", Command: "true"}); err != nil {
		t.Fatal(err)
	}
}
