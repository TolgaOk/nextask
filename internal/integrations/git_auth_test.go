package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGitAuthenticatedSnapshot(t *testing.T) {
	root, remote := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_ASKPASS", "/usr/bin/false")
	backend := filepath.Join(runGitTest(t, root, "--exec-path"), "git-http-backend")
	handler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remote), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=nextask"}}
	writer, reader := "writer@:/?#%&value", "reader@:/?#%&value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		push := strings.Contains(r.URL.String(), "git-receive-pack")
		if !ok || user != "nextask" || password != writer && (password != reader || push) {
			w.Header().Set("WWW-Authenticate", `Basic realm="snapshots"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	connection := strings.Replace(server.URL, "://", "://nextask:${SNAPSHOT_TOKEN}@", 1) + "/remote.git"
	t.Setenv("SNAPSHOT_TOKEN", writer)
	before := repoState(t, root)
	snapshot, err := publishSnapshot(context.Background(), root, "authenticated", connection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, repoState(t, root)) {
		t.Fatal("authenticated publication changed local repository")
	}
	if snapshot.Remote != server.URL+"/remote.git" || snapshot.Endpoint != connection {
		t.Fatal("snapshot did not retain the clean remote and unresolved endpoint")
	}
	command, err := snapshot.Wrap("cat tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(command+string(encoded), writer) || !strings.Contains(command, "${SNAPSHOT_TOKEN}") {
		t.Fatal("queued snapshot contains resolved credentials or lost its reference")
	}
	t.Setenv("SNAPSHOT_TOKEN", reader)
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	result := (Git{}).Run(context.Background(), Task{Command: "cat tracked.txt"}, Options{
		"remote": snapshot.Remote, "endpoint": snapshot.Endpoint, "ref": snapshot.Ref, "commit": snapshot.Commit,
	}, IO{Out: &output, Err: &output})
	if result.Err != nil || result.Code != 0 || output.String() != "original\n" {
		t.Fatalf("worker restore: code=%d err=%v output=%s", result.Code, result.Err, output.String())
	}
	if got := runGitTest(t, ".", "config", "--local", "remote.origin.url"); got != snapshot.Remote {
		t.Fatal("worker persisted credentials in origin")
	}
	for _, token := range []string{"", "wrong-password"} {
		t.Setenv("SNAPSHOT_TOKEN", token)
		err := snapshot.restore(context.Background())
		if err == nil || token != "" && strings.Contains(err.Error(), token) {
			t.Fatalf("missing/invalid worker credentials accepted or exposed: %v", err)
		}
		if token == "" && !strings.Contains(err.Error(), "SNAPSHOT_TOKEN") {
			t.Fatal("missing variable was not named")
		}
	}
	t.Setenv("SNAPSHOT_ENDPOINT", server.URL+"/other.git")
	snapshot.Endpoint = "${SNAPSHOT_ENDPOINT}"
	if err := snapshot.restore(context.Background()); err == nil || !strings.Contains(err.Error(), "different repository") {
		t.Fatalf("changed worker destination accepted: %v", err)
	}
}

func TestGitCredentialHelperScope(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_ASKPASS", "/usr/bin/false")
	t.Setenv("HELPER_TOKEN", "private-test-password")
	c, err := resolveGitConnection("https://nextask:${HELPER_TOKEN}@git.example/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"https://git.example/repo.git", "https://other.example/repo.git", "https://git.example/other.git", "http://git.example/repo.git"} {
		out, err := gitCommand(context.Background(), t.TempDir(), c.env, strings.NewReader("url="+target+"\n\n"), "credential", "fill")
		if target == c.remote {
			if err != nil || !strings.Contains(out, "password=private-test-password") {
				t.Fatal("helper did not supply credentials for the intended repository")
			}
		} else if err == nil || strings.Contains(out, "private-test-password") {
			t.Fatal("helper supplied credentials outside its repository")
		}
	}
}
