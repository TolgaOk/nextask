package endpoint

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateDatabaseURL checks a shareable PostgreSQL URL without reading credentials.
func ValidateDatabaseURL(value string) error { return validate(value, checkDatabaseTemplate) }

// ResolveDatabaseURL resolves credentials; whole environment values may also use
// libpq keyword connection strings, which pgx validates when connecting.
func ResolveDatabaseURL(value string) (string, error) {
	return resolve(value, checkDatabaseTemplate, func(value string) error {
		if !strings.Contains(value, "://") {
			return nil
		}
		return checkResolvedURL(value, checkDatabaseURL)
	})
}

func checkDatabaseTemplate(t *template) error {
	if err := checkDatabaseURL(t.url); err != nil {
		return err
	}
	if err := checkPasswordReference(t); err != nil {
		return err
	}
	query, _ := url.ParseQuery(t.url.RawQuery)
	for key, values := range query {
		for _, ref := range t.refs {
			if strings.Contains(key, ref.marker) {
				return fmt.Errorf("URL query parameter names must be literal")
			}
		}
		// PostgreSQL credential parameters have an explicit environment-only rule.
		if key == "password" || key == "sslpassword" {
			for _, value := range values {
				if !isReference(value, t.refs) {
					return fmt.Errorf("URL credential parameters must reference environment variables")
				}
			}
		}
	}
	return nil
}

func checkDatabaseURL(u *url.URL) error {
	if err := checkURL(u); err != nil {
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
