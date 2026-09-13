package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FetchOptions controls local selection and publication, independently of uploads.
type FetchOptions struct {
	Destination       string
	Include, Exclude  []string
	Overwrite, DryRun bool
	Timeout           time.Duration
}

func (o FetchOptions) Validate() error {
	if strings.TrimSpace(o.Destination) == "" {
		return fmt.Errorf("destination is required: set --to DIR")
	}
	if o.Timeout <= 0 || o.Timeout > 24*time.Hour {
		return fmt.Errorf("timeout must be positive and at most 24h")
	}
	return ValidatePatterns(o.Include, o.Exclude)
}

// Fetch lists and validates selected objects before writing. Each file is
// published atomically; completed files remain available if a later file fails.
func Fetch(ctx context.Context, store DownloadStore, prefix, taskID string, o FetchOptions, out io.Writer) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if !fs.ValidPath(taskID) || strings.ContainsAny(taskID, "/\\") || taskID == "." {
		return fmt.Errorf("invalid source task ID")
	}
	source := path.Join(prefix, taskID) + "/"
	var names []string
	listCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	err := store.List(listCtx, source, func(key string) error {
		name, ok := strings.CutPrefix(key, source)
		if !ok {
			return fmt.Errorf("object %q is outside the task prefix", key)
		}
		if name == "" {
			return nil
		}
		// S3 directory markers carry no file content.
		directory := strings.HasSuffix(name, "/")
		if directory {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "" && directory {
			return nil
		}
		if !fs.ValidPath(name) || name == "." || strings.ContainsRune(name, '\\') || reserved(name) {
			return fmt.Errorf("unsafe artifact path %q", name)
		}
		if _, err := filepath.Localize(name); err != nil {
			return fmt.Errorf("unsafe artifact path %q", name)
		}
		if directory {
			return nil
		}
		if (len(o.Include) == 0 || matches(o.Include, name)) && !excluded(o.Exclude, name) {
			names = append(names, name)
		}
		return nil
	})
	cancel()
	if err != nil {
		return fmt.Errorf("list artifacts for %s: %w", taskID, err)
	}
	if len(names) == 0 {
		return fmt.Errorf("no artifacts match task %s and the requested filters", taskID)
	}
	sort.Strings(names)
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		if selected[name] {
			return fmt.Errorf("duplicate artifact path %q", name)
		}
		selected[name] = true
	}
	for _, name := range names {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if selected[parent] {
				return fmt.Errorf("artifact paths conflict: %q is also a file", parent)
			}
		}
	}
	originals := make(map[string]os.FileInfo, len(names))
	root, err := openFetchRoot(o.Destination, false)
	if err != nil {
		return err
	}
	if root != nil {
		defer root.Close()
		for _, name := range names {
			dir, err := fetchParent(root, name, false)
			if err != nil {
				return err
			}
			if dir == nil {
				continue
			}
			originals[name], err = checkFetchTarget(dir, path.Base(name), o.Overwrite)
			dir.Close()
			if err != nil {
				return fmt.Errorf("%q: %w", name, err)
			}
		}
	}
	if o.DryRun {
		for _, name := range names {
			if _, err := fmt.Fprintln(out, name); err != nil {
				return err
			}
		}
		return nil
	}
	if root == nil {
		root, err = openFetchRoot(o.Destination, true)
		if err != nil {
			return err
		}
		defer root.Close()
	}
	for i, name := range names {
		if err := fetchFile(ctx, store, root, source, name, originals[name], o); err != nil {
			return fmt.Errorf("fetch stopped after %d of %d files; %q: %w", i, len(names), name, err)
		}
		if _, err := fmt.Fprintln(out, name); err != nil {
			return err
		}
	}
	return nil
}

func fetchFile(parent context.Context, store DownloadStore, root *os.Root, source, name string, original os.FileInfo, o FetchOptions) error {
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	dir, err := fetchParent(root, name, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	base := path.Base(name)
	if err := checkFetchUnchanged(dir, base, original); err != nil {
		return err
	}
	body, meta, err := store.Get(ctx, source+name)
	if err != nil {
		return err
	}
	defer body.Close()
	if meta.Size < 0 {
		return fmt.Errorf("object has an invalid size")
	}
	temporary := ".nextask-fetch-" + rand.Text()
	file, err := dir.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer dir.Remove(temporary)
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), &contextReader{ctx, io.LimitReader(body, meta.Size)})
	if err != nil {
		return err
	}
	// Consume EOF as well, so a short or unexpectedly growing response is rejected.
	var extra [1]byte
	n, endErr := body.Read(extra[:])
	if size != meta.Size || n != 0 || !errors.Is(endErr, io.EOF) {
		return fmt.Errorf("incomplete or changed object (expected %d bytes, received %d): %w", meta.Size, size+int64(n), io.ErrUnexpectedEOF)
	}
	if meta.SHA256 != "" && !strings.EqualFold(meta.SHA256, fmt.Sprintf("%x", hash.Sum(nil))) {
		return fmt.Errorf("object checksum mismatch")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := checkFetchUnchanged(dir, base, original); err != nil {
		return err
	}
	if original != nil {
		return dir.Rename(temporary, base)
	}
	// A hard link publishes the complete file without replacing a concurrent writer.
	return dir.Link(temporary, base)
}
