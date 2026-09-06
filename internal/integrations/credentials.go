package integrations

import (
	"fmt"
	"net/url"
	"strings"
)

// Connection options are serialized with tasks. Authentication belongs to the
// worker environment or Git's SSH/credential helpers, so these values must be
// usable in shared configuration without carrying passwords or tokens.
func checkGitRemote(value any) error {
	if err := checkURLCredentials(value.(string), true); err != nil {
		return fmt.Errorf("%w; use SSH or a Git credential helper on the submitter and worker", err)
	}
	return nil
}

func checkS3URL(value any) error {
	if err := checkURLCredentials(value.(string), false); err != nil {
		return fmt.Errorf("%w; supply S3_ACCESS_KEY and S3_SECRET_KEY in the worker environment", err)
	}
	return nil
}

func checkURLCredentials(value string, allowSSH bool) error {
	if !strings.Contains(value, "://") {
		return nil // Local paths, remote names, and Git's user@host:path syntax.
	}
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid connection URL")
	}
	if u.User != nil {
		_, password := u.User.Password()
		ssh := allowSSH && (u.Scheme == "ssh" || u.Scheme == "git+ssh" || u.Scheme == "ssh+git")
		if password || !ssh {
			return fmt.Errorf("connection URLs cannot contain credentials")
		}
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("connection URLs cannot contain query strings or fragments")
	}
	return nil
}
