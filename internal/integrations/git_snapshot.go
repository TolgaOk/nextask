package integrations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/urltemplate"
)

// gitCommand isolates inherited repository routing. Read commands use the source
// repository; all mutating commands receive a separate temporary GIT_DIR.
func gitCommand(ctx context.Context, dir string, env []string, input io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.hooksPath=/dev/null", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") && key != "GIT_SSH" && key != "GIT_SSH_COMMAND" && key != "GIT_SSH_VARIANT" && key != "GIT_ASKPASS" && key != "GIT_CONFIG_GLOBAL" && key != "GIT_CONFIG_NOSYSTEM" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = input
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, redactGitError(strings.TrimSpace(stderr.String()), env))
	}
	return string(output), nil
}

func publishSnapshot(ctx context.Context, repo, taskID, remote string) (GitSnapshot, error) {
	if repo == "" {
		repo = "."
	}
	read := func(args ...string) (string, error) { return gitCommand(ctx, repo, nil, nil, args...) }
	root, err := read("rev-parse", "--show-toplevel")
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("git integration requires a repository: %w", err)
	}
	root = strings.TrimSuffix(root, "\n")
	fetchURL, pushURL, err := resolveRemote(ctx, repo, remote)
	if err != nil {
		return GitSnapshot{}, err
	}
	fetchConnection, err := resolveGitConnection(fetchURL)
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("fetch endpoint: %w", err)
	}
	pushConnection, err := resolveGitConnection(pushURL)
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("push endpoint: %w", err)
	}
	ref := "refs/heads/" + filepath.Base(root) + "/" + taskID
	if _, err := read("check-ref-format", ref); err != nil {
		return GitSnapshot{}, err
	}
	objects, err := read("rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return GitSnapshot{}, err
	}
	format, err := read("rev-parse", "--show-object-format")
	if err != nil {
		return GitSnapshot{}, err
	}
	head, err := read("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("repository must have an initial commit: %w", err)
	}
	paths, err := gitCommand(ctx, root, nil, nil, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return GitSnapshot{}, err
	}
	temporary, err := os.MkdirTemp("", "nextask-git-*")
	if err != nil {
		return GitSnapshot{}, err
	}
	defer os.RemoveAll(temporary)
	gitDir := filepath.Join(temporary, "repo.git")
	if _, err := gitCommand(ctx, temporary, nil, nil, "init", "--quiet", "--bare", "--template=", "--object-format="+strings.TrimSpace(format), gitDir); err != nil {
		return GitSnapshot{}, err
	}
	env := []string{
		"GIT_DIR=" + gitDir,
		"GIT_INDEX_FILE=" + filepath.Join(temporary, "index"),
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects"),
		// C-style quoting handles object paths containing colons or quotes.
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(strings.TrimSuffix(objects, "\n")),
	}
	write := func(input io.Reader, args ...string) (string, error) {
		return gitCommand(ctx, temporary, env, input, args...)
	}
	existing, err := gitCommand(ctx, temporary, append(append([]string{}, env...), pushConnection.env...), nil, "ls-remote", "--", pushConnection.remote, ref)
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("inspect snapshot remote: %w", err)
	}
	if strings.TrimSpace(existing) != "" {
		return GitSnapshot{}, fmt.Errorf("snapshot ref already exists: %s", ref)
	}
	var index bytes.Buffer
	files := strings.Split(strings.TrimSuffix(paths, "\x00"), "\x00")
	slices.Sort(files)
	files = slices.Compact(files)
	for _, name := range files {
		if name == "" {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		} // tracked deletion
		if err != nil {
			return GitSnapshot{}, err
		}
		mode := "100644"
		var contents io.Reader
		var file *os.File
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return GitSnapshot{}, err
			}
			contents, mode = strings.NewReader(target), "120000"
		case info.Mode().IsRegular():
			file, err = os.Open(path)
			if err != nil {
				return GitSnapshot{}, err
			}
			contents = file
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
		default:
			return GitSnapshot{}, fmt.Errorf("cannot snapshot %q: submodules and special files are unsupported", name)
		}
		hash, hashErr := write(contents, "hash-object", "-w", "--no-filters", "--stdin")
		if file != nil {
			file.Close()
		}
		if hashErr != nil {
			return GitSnapshot{}, hashErr
		}
		fmt.Fprintf(&index, "%s %s\t%s\x00", mode, strings.TrimSpace(hash), name)
	}
	if _, err := write(&index, "update-index", "-z", "--index-info"); err != nil {
		return GitSnapshot{}, err
	}
	tree, err := write(nil, "write-tree")
	if err != nil {
		return GitSnapshot{}, err
	}
	// Read identity without invoking user hooks or changing local configuration.
	author, err := read("log", "-1", "--format=%an%n%ae")
	if err != nil {
		return GitSnapshot{}, err
	}
	identity := strings.SplitN(strings.TrimSuffix(author, "\n"), "\n", 2)
	if len(identity) != 2 {
		return GitSnapshot{}, fmt.Errorf("could not read commit identity")
	}
	env = append(env, "GIT_AUTHOR_NAME="+identity[0], "GIT_AUTHOR_EMAIL="+identity[1], "GIT_COMMITTER_NAME="+identity[0], "GIT_COMMITTER_EMAIL="+identity[1])
	commit, err := write(nil, "commit-tree", strings.TrimSpace(tree), "-p", strings.TrimSpace(head), "-m", "nextask: "+taskID)
	if err != nil {
		return GitSnapshot{}, err
	}
	commit = strings.TrimSpace(commit)
	// An empty expected old value makes this a create-only remote ref update.
	if _, err := gitCommand(ctx, temporary, append(append([]string{}, env...), pushConnection.env...), nil, "push", "--no-verify", "--no-follow-tags", "--recurse-submodules=no", "--force-with-lease="+ref+":", "--", pushConnection.remote, commit+":"+ref); err != nil {
		return GitSnapshot{}, fmt.Errorf("publish snapshot: %w", err)
	}
	snapshot := GitSnapshot{Remote: fetchConnection.remote, Ref: ref, Commit: commit}
	if urltemplate.HasReferences(fetchURL) {
		snapshot.Endpoint = fetchURL
	}
	return snapshot, nil
}

func resolveRemote(ctx context.Context, repo, remote string) (fetch, push string, err error) {
	if urltemplate.HasReferences(remote) {
		resolved, err := ResolveGitRemote(remote)
		if err != nil {
			return "", "", err
		}
		// Preserve endpoint references for the worker. A reference to a local
		// remote name is resolved here to its configured fetch/push URLs.
		if !strings.ContainsAny(resolved, "/:") && !strings.HasPrefix(resolved, ".") {
			remote = resolved
		}
	}
	fetch, err = gitCommand(ctx, repo, nil, nil, "remote", "get-url", "--", remote)
	if err == nil {
		push, err = gitCommand(ctx, repo, nil, nil, "remote", "get-url", "--push", "--", remote)
		if err != nil {
			return "", "", err
		}
		fetch, push = strings.TrimSpace(fetch), strings.TrimSpace(push)
	} else {
		if !urltemplate.HasReferences(remote) && !strings.ContainsAny(remote, "/:") && !strings.HasPrefix(remote, ".") && !filepath.IsAbs(remote) {
			return "", "", fmt.Errorf("unknown Git remote; use an existing remote name or URL/path")
		}
		fetch, push = remote, remote
	}
	normalize := func(value string) (string, error) {
		if err := checkGitRemote(value); err != nil {
			return "", err
		}
		if urltemplate.HasReferences(value) || strings.Contains(value, ":") {
			return value, nil
		}
		if strings.HasPrefix(value, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			value = filepath.Join(homeDir, value[2:])
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(repo, value)
		}
		return filepath.Abs(value)
	}
	fetch, err = normalize(fetch)
	if err != nil {
		return "", "", err
	}
	push, err = normalize(push)
	return
}
