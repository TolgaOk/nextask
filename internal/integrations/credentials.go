package integrations

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/TolgaOk/nextask/internal/endpoint"
)

// Connection options are serialized with tasks. Authentication belongs to the
// worker environment or Git's SSH/credential helpers, so these values must be
// usable in shared configuration without carrying passwords or tokens.
func checkGitRemote(value any) error {
	if err := endpoint.ValidateGitRemote(value.(string)); err != nil {
		return fmt.Errorf("%w; use environment references or a Git credential helper", err)
	}
	return nil
}

func checkS3Endpoint(value any) error {
	return endpoint.ValidateS3Endpoint(value.(string))
}

func checkS3URL(value any) error {
	if err := checkURLCredentials(value.(string)); err != nil {
		return fmt.Errorf("%w; put credential environment references in the S3 endpoint", err)
	}
	return nil
}

func checkURLCredentials(value string) error {
	if !strings.Contains(value, "://") {
		return nil // Local paths, remote names, and Git's user@host:path syntax.
	}
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid connection URL")
	}
	if u.User != nil {
		return fmt.Errorf("connection URLs cannot contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("connection URLs cannot contain query strings or fragments")
	}
	return nil
}
