package integrations

import (
	"fmt"
	"net/url"

	"github.com/TolgaOk/nextask/internal/storage"
	"github.com/TolgaOk/nextask/internal/urltemplate"
)

// ValidateS3Endpoint checks an S3 service URL with credential references.
func ValidateS3Endpoint(value string) error { return urltemplate.Validate(value, checkS3Template) }

// ResolveS3Endpoint resolves S3 access and secret keys from the worker environment.
func ResolveS3Endpoint(value string) (string, error) {
	return urltemplate.Resolve(value, checkS3Template, func(value string) error {
		return urltemplate.CheckResolvedURL(value, checkS3ServiceURL)
	})
}

func checkS3Template(t *urltemplate.Template) error {
	if err := checkS3ServiceURL(t.URL); err != nil {
		return err
	}
	if err := t.CheckPasswordReference(); err != nil {
		return err
	}
	if !t.IsReference(t.URL.User.Username()) {
		return fmt.Errorf("S3 access key must be an environment reference such as ${VARIABLE}")
	}
	return nil
}

func checkS3ServiceURL(u *url.URL) error {
	if err := urltemplate.CheckURL(u); err != nil {
		return err
	}
	if u.Hostname() == "" {
		return fmt.Errorf("connection URL requires a host")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("connection URL cannot contain a query string")
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Path != "" && u.Path != "/" {
		return fmt.Errorf("S3 endpoint must be an http(s) service URL without a path")
	}
	if u.User == nil {
		return fmt.Errorf("S3 endpoint requires access-key and secret-key environment references")
	}
	password, ok := u.User.Password()
	if !ok || password == "" || u.User.Username() == "" {
		return fmt.Errorf("S3 endpoint requires an access key and secret key")
	}
	return nil
}

func newS3Store(cfg storage.Config) (storage.Store, error) {
	value, err := ResolveS3Endpoint(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("s3 endpoint: %w", err)
	}
	if value == "" {
		return nil, fmt.Errorf("s3 endpoint is required")
	}
	return storage.NewS3(cfg, value)
}
