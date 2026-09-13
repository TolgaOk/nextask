package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The caller chooses the destination's parent. Object paths only traverse roots
// anchored by directory handles, with symlinks rejected at each component.
func openFetchRoot(destination string, create bool) (*os.Root, error) {
	destination = filepath.Clean(destination)
	info, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return nil, nil
		}
		if err := os.MkdirAll(destination, 0755); err != nil {
			return nil, err
		}
		info, err = os.Lstat(destination)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("destination must be a directory without a symlink: %s", destination)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("destination changed while opening")
	}
	return root, nil
}

func fetchParent(root *os.Root, name string, create bool) (*os.Root, error) {
	dir, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	parent := path.Dir(name)
	if parent == "." {
		return dir, nil
	}
	for _, part := range strings.Split(parent, "/") {
		info, err := dir.Lstat(part)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				dir.Close()
				return nil, nil
			}
			if err := dir.Mkdir(part, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
				dir.Close()
				return nil, err
			}
			info, err = dir.Lstat(part)
		}
		if err != nil {
			dir.Close()
			return nil, err
		}
		if !info.IsDir() {
			dir.Close()
			return nil, fmt.Errorf("%q: parent must be a directory without symlinks", name)
		}
		next, err := dir.OpenRoot(part)
		dir.Close()
		if err != nil {
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, fmt.Errorf("%q: parent changed while opening", name)
		}
		dir = next
	}
	return dir, nil
}

// checkFetchTarget captures the file an explicit overwrite is allowed to replace.
func checkFetchTarget(dir *os.Root, name string, overwrite bool) (os.FileInfo, error) {
	info, err := dir.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("destination is not a regular file; symlinks are forbidden")
	}
	if !overwrite {
		return nil, fmt.Errorf("destination already exists; use --overwrite to replace it")
	}
	return info, nil
}

// A path may now refer to another downloaded object on filesystems that fold
// case or Unicode. It may also have been replaced by a concurrent writer.
func checkFetchUnchanged(dir *os.Root, name string, original os.FileInfo) error {
	current, err := checkFetchTarget(dir, name, true)
	if err != nil {
		return err
	}
	if original == nil && current == nil {
		return nil
	}
	if original != nil && current != nil && os.SameFile(original, current) && original.Size() == current.Size() && original.ModTime().Equal(current.ModTime()) {
		return nil
	}
	return fmt.Errorf("destination changed during fetch or aliases another artifact; refusing to replace it")
}
