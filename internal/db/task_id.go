package db

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrTaskExists means the caller must choose a different task ID.
var ErrTaskExists = errors.New("task ID already exists")

// PostgreSQL identifiers allow 63 bytes; "from_task_" uses ten of them.
// Restricting characters also keeps IDs safe as directory and Git ref names.
var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,52}$`)

// ValidateTaskID checks the shared constraints on caller-supplied task IDs.
func ValidateTaskID(id string) error {
	if !taskIDPattern.MatchString(id) {
		return fmt.Errorf("invalid task ID %q: use 1–53 ASCII letters, digits, underscores or hyphens, starting with a letter or digit", id)
	}
	return nil
}
