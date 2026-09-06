package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// Syncer owns policy and reporting, independently of its storage transport.
type Syncer struct {
	Config          Config
	Store           Store
	TaskID, TaskDir string
	Log             func(string, ...any)
}

func reserved(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".git" || part == ".nextask" {
			return true
		}
	}
	return false
}
func matches(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, _ := doublestar.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// Sync uploads a single pass. Only selected files consume staging space, bounded
// by Concurrency; staging files are removed after their transfers finish.
func (s *Syncer) Sync(ctx context.Context, final bool) error {
	taskRoot, err := os.OpenRoot(s.TaskDir)
	if err != nil {
		return err
	}
	defer taskRoot.Close()
	// Root itself must not route through a symlink or an internal directory.
	current := ""
	for _, component := range strings.Split(s.Config.Root, "/") {
		current = path.Join(current, component)
		info, err := taskRoot.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || reserved(current) {
			return fmt.Errorf("upload root must be a data directory without symlinks")
		}
	}
	root, err := taskRoot.OpenRoot(s.Config.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	includes := append([]string{}, s.Config.Include...)
	if final {
		includes = append(includes, s.Config.FinalInclude...)
	}
	var mu sync.Mutex
	var firstErr error
	failures, uploaded := 0, 0
	record := func(err error) {
		if err != nil {
			mu.Lock()
			defer mu.Unlock()
			failures++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < s.Config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				sent, err := s.upload(ctx, root, name, final)
				if err != nil {
					record(fmt.Errorf("%q: %w", name, err))
				}
				if sent {
					mu.Lock()
					uploaded++
					mu.Unlock()
				}
			}
		}()
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if reserved(name) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if matches(s.Config.Exclude, name) {
				return fs.SkipDir
			}
			return nil
		}
		if !matches(includes, name) || matches(s.Config.Exclude, name) {
			return nil
		}
		select {
		case jobs <- name:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	close(jobs)
	wg.Wait()
	record(err)
	if firstErr != nil {
		return fmt.Errorf("%d file/scan errors: %w", failures, firstErr)
	}
	s.Log("sync complete (%d uploaded, final=%t)", uploaded, final)
	return nil
}

func (s *Syncer) upload(parent context.Context, root *os.Root, name string, final bool) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, s.Config.UploadTimeout)
	defer cancel()
	before, err := root.Lstat(name)
	if err != nil {
		return false, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		if s.Config.Symlinks == "error" {
			return false, fmt.Errorf("symbolic link is excluded by policy")
		}
		s.Log("skipped symlink %q", name)
		return false, nil
	}
	if !before.Mode().IsRegular() {
		return false, fmt.Errorf("special files cannot be uploaded")
	}
	if s.Config.MaxFileSize > 0 && before.Size() > s.Config.MaxFileSize {
		s.Log("skipped oversized file %q", name)
		return false, nil
	}
	if !final && s.Config.MinAge > 0 && time.Since(before.ModTime()) < s.Config.MinAge {
		return false, nil
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return false, fmt.Errorf("file changed before reading")
	}
	staged, err := os.CreateTemp("", "nextask-upload-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(staged, hash), &contextReader{ctx, io.LimitReader(file, before.Size()+1)})
	if err != nil {
		return false, err
	}
	after, err := file.Stat()
	if err != nil {
		return false, err
	}
	if size != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return false, fmt.Errorf("file changed while reading; retry on the next pass")
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	key := path.Join(s.Config.Prefix, s.TaskID, name)
	object, err := s.Store.Stat(ctx, key)
	if err == nil && object.Size == size && object.SHA256 == digest {
		return false, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if _, err = staged.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := s.Store.Put(ctx, key, staged, size, digest, contentType); err != nil {
		return false, err
	}
	s.Log("uploaded %q (%d bytes)", name, size)
	return true, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
