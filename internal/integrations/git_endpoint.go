package integrations

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/TolgaOk/nextask/internal/urltemplate"
)

// ValidateGitRemote checks a Git URL, path, or remote name without reading credentials.
func ValidateGitRemote(value string) error { return urltemplate.Validate(value, checkGitTemplate) }

// ResolveGitRemote resolves a Git remote from the current process's environment.
func ResolveGitRemote(value string) (string, error) {
	return urltemplate.Resolve(value, checkGitTemplate, func(value string) error {
		if !strings.Contains(value, "://") {
			return nil
		}
		return urltemplate.CheckResolvedURL(value, checkGitURL)
	})
}

func checkGitTemplate(t *urltemplate.Template) error {
	// Local paths, remote names, and user@host:path need no URL checks.
	if t.URL == nil {
		return nil
	}
	if err := checkGitURL(t.URL); err != nil {
		return err
	}
	return t.CheckPasswordReference()
}

func checkGitURL(u *url.URL) error {
	if err := urltemplate.CheckURL(u); err != nil {
		return err
	}
	if u.Hostname() == "" && u.Scheme != "file" {
		return fmt.Errorf("connection URL requires a host")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("connection URL cannot contain a query string")
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok && u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("Git URL passwords are supported only with HTTP(S); use an SSH key for SSH")
		}
	}
	return nil
}
