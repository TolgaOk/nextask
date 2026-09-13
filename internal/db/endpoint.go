package db

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/TolgaOk/nextask/internal/urltemplate"
)

// ValidateURL checks a shareable PostgreSQL URL without reading credentials.
func ValidateURL(value string) error {
	return urltemplate.Validate(value, checkDatabaseTemplate)
}

// ResolveURL resolves credentials; whole environment values may also use
// libpq keyword connection strings, which pgx validates when connecting.
func ResolveURL(value string) (string, error) {
	return urltemplate.Resolve(value, checkDatabaseTemplate, func(value string) error {
		if !strings.Contains(value, "://") {
			return nil
		}
		return urltemplate.CheckResolvedURL(value, checkDatabaseURL)
	})
}

func checkDatabaseTemplate(t *urltemplate.Template) error {
	if err := checkDatabaseURL(t.URL); err != nil {
		return err
	}
	if err := t.CheckPasswordReference(); err != nil {
		return err
	}
	query, _ := url.ParseQuery(t.URL.RawQuery)
	for key, values := range query {
		if t.ContainsReference(key) {
			return fmt.Errorf("URL query parameter names must be literal")
		}
		// PostgreSQL credential parameters have an explicit environment-only rule.
		if key == "password" || key == "sslpassword" {
			for _, value := range values {
				if !t.IsReference(value) {
					return fmt.Errorf("URL credential parameters must reference environment variables")
				}
			}
		}
	}
	return nil
}

func checkDatabaseURL(u *url.URL) error {
	if err := urltemplate.CheckURL(u); err != nil {
		return err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("database URL must use postgres:// or postgresql://")
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return fmt.Errorf("invalid connection URL query")
	}
	return nil
}
