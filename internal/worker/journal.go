package worker

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

const journalVersion = 1

type completionRecord struct {
	Version int `json:"version"`
	db.TaskCompletion
}

type completionJournal struct{ dir string }

func newCompletionJournal(workdir string) completionJournal {
	return completionJournal{dir: filepath.Join(workdir, ".nextask", "completions")}
}

func completionName(c db.TaskCompletion) string {
	claim := strings.Join([]string{c.TaskID, c.WorkerID, c.CreatedAt.UTC().Format(time.RFC3339Nano), c.StartedAt.UTC().Format(time.RFC3339Nano)}, "\x00")
	return fmt.Sprintf("%s-%x.json", c.TaskID, sha256.Sum256([]byte(claim)))
}

// init syncs each newly created directory's parent so the journal path itself
// survives a crash, including when the worker workdir did not previously exist.
func (j completionJournal) init() error { return durableDirectory(j.dir) }

func durableDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("journal path is not a directory: %s", dir)
		}
		// Another worker may have just created this directory. Sync its parent
		// ourselves before relying on that worker's unfinished initialization.
		return syncDirectory(filepath.Dir(dir))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := durableDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDirectory(parent)
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

// save publishes a fully synced file without replacing an earlier outcome for
// the same claim. Readers only see immutable .json records, never partial writes.
func (j completionJournal) save(c db.TaskCompletion) (db.TaskCompletion, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if err := j.init(); err != nil {
		return c, err
	}
	file, err := os.CreateTemp(j.dir, ".pending-")
	if err != nil {
		return c, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := json.NewEncoder(file).Encode(completionRecord{Version: journalVersion, TaskCompletion: c}); err != nil {
		return c, err
	}
	if err := file.Sync(); err != nil {
		return c, err
	}
	if err := file.Close(); err != nil {
		return c, err
	}
	name := completionName(c)
	for {
		err := os.Link(file.Name(), filepath.Join(j.dir, name))
		if errors.Is(err, os.ErrExist) {
			previous, readErr := j.read(name)
			if errors.Is(readErr, os.ErrNotExist) {
				continue // Another worker acknowledged the existing record.
			}
			if readErr != nil {
				return c, readErr
			}
			return previous, syncDirectory(j.dir)
		}
		if err != nil {
			return c, err
		}
		return c, syncDirectory(j.dir)
	}
}

func (j completionJournal) pending() ([]string, error) {
	if err := j.init(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

func (j completionJournal) read(name string) (db.TaskCompletion, error) {
	var record completionRecord
	if filepath.Base(name) != name {
		return record.TaskCompletion, fmt.Errorf("invalid journal filename")
	}
	// Nonblocking/no-follow opens also reject FIFOs and symlinks without hanging
	// or reading a file outside the journal if a directory entry changes.
	file, err := os.OpenFile(filepath.Join(j.dir, name), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return record.TaskCompletion, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return record.TaskCompletion, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024 {
		return record.TaskCompletion, fmt.Errorf("journal record must be a regular file of at most 16 KiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record.TaskCompletion, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return record.TaskCompletion, fmt.Errorf("journal record has trailing data")
	}
	if record.Version != journalVersion {
		return record.TaskCompletion, fmt.Errorf("unsupported completion journal version %d", record.Version)
	}
	if err := record.TaskCompletion.Validate(); err != nil {
		return record.TaskCompletion, err
	}
	if completionName(record.TaskCompletion) != name {
		return record.TaskCompletion, fmt.Errorf("journal filename does not match its claim")
	}
	return record.TaskCompletion, nil
}

func (j completionJournal) acknowledge(c db.TaskCompletion) error {
	if err := os.Remove(filepath.Join(j.dir, completionName(c))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(j.dir)
}
