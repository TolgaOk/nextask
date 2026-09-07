package endpoint

import (
	"fmt"
	"net/url"
)

// ValidateS3Endpoint checks an S3 service URL with credential references.
func ValidateS3Endpoint(value string) error { return validate(value, checkS3Template) }

// ResolveS3Endpoint resolves S3 access and secret keys from the worker environment.
func ResolveS3Endpoint(value string) (string, error) {
	return resolve(value, checkS3Template, func(value string) error {
		return checkResolvedURL(value, checkS3URL)
	})
}

func checkS3Template(t *template) error {
	if err := checkS3URL(t.url); err != nil {
		return err
	}
	if err := checkPasswordReference(t); err != nil {
		return err
	}
	if !isReference(t.url.User.Username(), t.refs) {
		return fmt.Errorf("S3 access key must be an environment reference such as ${VARIABLE}")
	}
	return nil
}

func checkS3URL(u *url.URL) error {
	if err := checkURL(u); err != nil {
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
