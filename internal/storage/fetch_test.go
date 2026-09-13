package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type downloadFixture struct {
	keys    []string
	data    map[string]string
	listErr error
	get     func(context.Context, string) (io.ReadCloser, Object, error)
	gets    int
}

func (f *downloadFixture) List(_ context.Context, _ string, visit func(string) error) error {
	if f.listErr != nil {
		return f.listErr
	}
	for _, key := range f.keys {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
func (f *downloadFixture) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	f.gets++
	if f.get != nil {
		return f.get(ctx, key)
	}
	data := f.data[key]
	sum := sha256.Sum256([]byte(data))
	return io.NopCloser(strings.NewReader(data)), Object{Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sum)}, nil
}
func fetchOptions(t *testing.T) FetchOptions {
	t.Helper()
	return FetchOptions{Destination: filepath.Join(t.TempDir(), "downloads"), Timeout: time.Second}
}
func readFetch(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestFetchSelectionAndOverwrite(t *testing.T) {
	ctx := context.Background()
	o := fetchOptions(t)
	f := &downloadFixture{keys: []string{"project/task/", "project/task/out/", "project/task/out/a.txt", "project/task/out/b.tmp", "project/task/final/report.txt", "project/task/cache/data"}, data: map[string]string{"project/task/out/a.txt": "first", "project/task/final/report.txt": "report"}}
	o.Exclude = []string{"**/*.tmp", "cache"}
	o.DryRun = true
	var out strings.Builder
	if err := Fetch(ctx, f, "project", "task", o, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "final/report.txt\nout/a.txt\n" {
		t.Fatalf("dry-run: %q", out.String())
	}
	if _, err := os.Stat(o.Destination); !errors.Is(err, os.ErrNotExist) || f.gets != 0 {
		t.Fatal("dry-run wrote files or downloaded bodies")
	}
	o.DryRun = false
	out.Reset()
	if err := Fetch(ctx, f, "project", "task", o, &out); err != nil {
		t.Fatal(err)
	}
	if readFetch(t, o.Destination, "out/a.txt") != "first" || readFetch(t, o.Destination, "final/report.txt") != "report" {
		t.Fatal("wrong contents")
	}
	f.data["project/task/out/a.txt"] = "second"
	before := f.gets
	if err := Fetch(ctx, f, "project", "task", o, io.Discard); err == nil || !strings.Contains(err.Error(), "--overwrite") {
		t.Fatalf("conflict: %v", err)
	}
	if f.gets != before || readFetch(t, o.Destination, "out/a.txt") != "first" {
		t.Fatal("conflict modified files")
	}
	o.Overwrite = true
	o.Include = []string{"out/**", "missing/**"}
	if err := Fetch(ctx, f, "project", "task", o, io.Discard); err != nil {
		t.Fatal(err)
	}
	if readFetch(t, o.Destination, "out/a.txt") != "second" {
		t.Fatal("overwrite failed")
	}
	o.Include = []string{"no-match"}
	if err := Fetch(ctx, f, "project", "task", o, io.Discard); err == nil {
		t.Fatal("empty selection accepted")
	}
}
func TestFetchRejectsUnsafePaths(t *testing.T) {
	for _, key := range []string{"task/../escape", "task//absolute", "task/a/../../escape", "task/a\\b", "task/./dot", "task/a//b", "task/.git/config", "task/.GIT/config", "task/.NEXTASK/state", "task/sub/.nextask/state", "other/file", "task/bad\x00name"} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			o := fetchOptions(t)
			f := &downloadFixture{keys: []string{key}}
			if err := Fetch(context.Background(), f, "", "task", o, io.Discard); err == nil {
				t.Fatal("unsafe key accepted")
			}
			if f.gets != 0 {
				t.Fatal("unsafe key downloaded")
			}
			if _, err := os.Stat(o.Destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsafe key created destination")
			}
		})
	}
	for _, keys := range [][]string{{"task/a", "task/a/b"}, {"task/a", "task/a"}} {
		f := &downloadFixture{keys: keys}
		if err := Fetch(context.Background(), f, "", "task", fetchOptions(t), io.Discard); err == nil {
			t.Fatal("conflicting keys accepted")
		}
	}
}
func TestFetchRejectsSymlinksAndDirectories(t *testing.T) {
	for _, kind := range []string{"root", "root-trailing-slash", "parent", "file", "directory"} {
		for _, overwrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", kind, overwrite), func(t *testing.T) {
				o := fetchOptions(t)
				o.Overwrite = overwrite
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "file"), []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "root" || kind == "root-trailing-slash" {
					if err := os.Symlink(outside, o.Destination); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(o.Destination, 0755); err != nil {
						t.Fatal(err)
					}
					switch kind {
					case "parent":
						if err := os.Symlink(outside, filepath.Join(o.Destination, "sub")); err != nil {
							t.Fatal(err)
						}
					case "file", "directory":
						if err := os.Mkdir(filepath.Join(o.Destination, "sub"), 0755); err != nil {
							t.Fatal(err)
						}
						target := filepath.Join(o.Destination, "sub", "file")
						if kind == "file" {
							if err := os.Symlink(filepath.Join(outside, "file"), target); err != nil {
								t.Fatal(err)
							}
						} else if err := os.Mkdir(target, 0755); err != nil {
							t.Fatal(err)
						}
					}
				}
				if kind == "root-trailing-slash" {
					o.Destination += "/"
				}
				f := &downloadFixture{keys: []string{"task/sub/file"}}
				if err := Fetch(context.Background(), f, "", "task", o, io.Discard); err == nil {
					t.Fatal("unsafe destination accepted")
				}
				if f.gets != 0 || readFetch(t, outside, "file") != "untouched" {
					t.Fatal("unsafe destination written")
				}
			})
		}
	}
}

type interruptedBody struct {
	reader io.Reader
	fail   error
}

func (b *interruptedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		return 0, b.fail
	}
	return n, err
}
func (b *interruptedBody) Close() error { return nil }
func TestFetchIncompleteDownloads(t *testing.T) {
	for _, failure := range []string{"interrupted", "short", "long", "checksum", "cancel", "timeout", "get", "list"} {
		t.Run(failure, func(t *testing.T) {
			o := fetchOptions(t)
			o.Overwrite = true
			if err := os.MkdirAll(o.Destination, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(o.Destination, "file"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &downloadFixture{keys: []string{"task/file"}}
			f.get = func(ctx context.Context, _ string) (io.ReadCloser, Object, error) {
				body := io.NopCloser(strings.NewReader("replacement"))
				meta := Object{Size: 11}
				switch failure {
				case "interrupted":
					return &interruptedBody{strings.NewReader("part"), io.ErrUnexpectedEOF}, meta, nil
				case "short":
					meta.Size = 12
				case "long":
					meta.Size = 10
				case "checksum":
					meta.SHA256 = "bad"
				case "cancel":
					cancel()
				case "timeout":
					<-ctx.Done()
					return nil, Object{}, ctx.Err()
				case "get":
					return nil, Object{}, errors.New("AccessDenied")
				}
				return body, meta, nil
			}
			if failure == "timeout" {
				o.Timeout = 10 * time.Millisecond
			}
			if failure == "list" {
				f.listErr = errors.New("AccessDenied")
			}
			if err := Fetch(ctx, f, "", "task", o, io.Discard); err == nil {
				t.Fatal("failure accepted")
			}
			if readFetch(t, o.Destination, "file") != "original" {
				t.Fatal("partial download replaced file")
			}
			files, err := os.ReadDir(o.Destination)
			if err != nil || len(files) != 1 {
				t.Fatalf("temporary files leaked: %v %v", files, err)
			}
		})
	}
}
func TestFetchConcurrentDestinationAndPartialProgress(t *testing.T) {
	o := fetchOptions(t)
	f := &downloadFixture{keys: []string{"task/a", "task/b"}}
	f.get = func(_ context.Context, key string) (io.ReadCloser, Object, error) {
		if key == "task/b" {
			if err := os.WriteFile(filepath.Join(o.Destination, "b"), []byte("concurrent"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return io.NopCloser(strings.NewReader("download")), Object{Size: 8}, nil
	}
	err := Fetch(context.Background(), f, "", "task", o, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("partial progress: %v", err)
	}
	if readFetch(t, o.Destination, "a") != "download" || readFetch(t, o.Destination, "b") != "concurrent" {
		t.Fatal("concurrent write replaced")
	}
}
func TestFetchValidation(t *testing.T) {
	for _, patterns := range [][]string{{""}, {"/absolute"}, {"../parent"}, {"a/../b"}, {"["}, {"a\\b"}, {"bad\x00name"}} {
		if err := ValidatePatterns(patterns); err == nil {
			t.Fatalf("invalid pattern accepted: %q", patterns)
		}
	}
	o := fetchOptions(t)
	o.Destination = ""
	if err := o.Validate(); err == nil {
		t.Fatal("missing destination accepted")
	}
	o = fetchOptions(t)
	o.Timeout = 0
	if err := o.Validate(); err == nil {
		t.Fatal("missing timeout accepted")
	}
}

func TestFetchFilesystemAliases(t *testing.T) {
	for _, pair := range [][2]string{{"Case.txt", "case.txt"}, {"caf\u00e9.txt", "cafe\u0301.txt"}} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/existing=%t", pair, existing), func(t *testing.T) {
				names := []string{pair[0], pair[1]}
				sort.Strings(names)
				probe := t.TempDir()
				if err := os.WriteFile(filepath.Join(probe, names[0]), []byte("probe"), 0600); err != nil {
					t.Fatal(err)
				}
				_, err := os.Stat(filepath.Join(probe, names[1]))
				aliased := err == nil
				o := fetchOptions(t)
				o.Overwrite = true
				if err := os.MkdirAll(o.Destination, 0755); err != nil {
					t.Fatal(err)
				}
				if existing {
					for _, name := range names {
						if err := os.WriteFile(filepath.Join(o.Destination, name), []byte("original"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				f := &downloadFixture{keys: []string{"task/" + names[0], "task/" + names[1]}, data: map[string]string{"task/" + names[0]: "first", "task/" + names[1]: "second"}}
				err = Fetch(context.Background(), f, "", "task", o, io.Discard)
				if aliased {
					if err == nil || !strings.Contains(err.Error(), "aliases another artifact") {
						t.Fatalf("alias not reported: %v", err)
					}
					if readFetch(t, o.Destination, names[0]) != "first" {
						t.Fatal("second artifact silently replaced the first")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if readFetch(t, o.Destination, names[0]) != "first" || readFetch(t, o.Destination, names[1]) != "second" {
						t.Fatal("distinct filenames lost")
					}
				}
			})
		}
	}
}

func TestFetchOverwritePreservesConcurrentChanges(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			o := fetchOptions(t)
			o.Overwrite = true
			if err := os.MkdirAll(o.Destination, 0755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(o.Destination, "file")
			if existing {
				if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f := &downloadFixture{keys: []string{"task/file"}}
			f.get = func(context.Context, string) (io.ReadCloser, Object, error) {
				replacement := filepath.Join(o.Destination, "concurrent")
				if err := os.WriteFile(replacement, []byte("another writer"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, target); err != nil {
					t.Fatal(err)
				}
				return io.NopCloser(strings.NewReader("download")), Object{Size: 8}, nil
			}
			if err := Fetch(context.Background(), f, "", "task", o, io.Discard); err == nil || !strings.Contains(err.Error(), "destination changed") {
				t.Fatalf("concurrent replacement: %v", err)
			}
			if readFetch(t, o.Destination, "file") != "another writer" {
				t.Fatal("concurrent file overwritten")
			}
			entries, err := os.ReadDir(o.Destination)
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging files leaked: %v %v", entries, err)
			}
		})
	}
}
