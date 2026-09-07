// Package endpoint validates shareable connection URLs and resolves environment
// references only at the point of use. Resolved credentials must not be persisted.
package endpoint

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Kind int

const (
	Database Kind = iota
	Git
	S3
)

var reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Reference selects a complete connection value from the environment.
func Reference(name string) string { return "${" + name + "}" }

func HasReferences(value string) bool { return reference.MatchString(value) }

type template struct {
	raw, whole string
	url        *url.URL
	local      string
	refs       []envReference
}

type envReference struct{ marker, name string }

// Validate checks syntax and rejects literal secrets without reading the
// environment. Unselected integrations do not require their credentials.
func Validate(value string, kind Kind) error {
	_, err := parse(value, kind)
	return err
}

// Resolve expands references once. Values substituted into URL components are
// escaped by net/url, so punctuation in a password cannot change the destination.
func Resolve(value string, kind Kind) (string, error) {
	t, err := parse(value, kind)
	if err != nil || value == "" {
		return "", err
	}
	if t.whole != "" {
		value, err := environment(t.whole)
		if err != nil {
			return "", err
		}
		// libpq keyword connection strings remain supported through a whole
		// environment value. pgx performs their final validation.
		if kind == Database && !strings.Contains(value, "://") {
			return value, nil
		}
		if kind == Git && !strings.Contains(value, "://") {
			return value, nil
		}
		u, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("%s contains an invalid connection URL", t.whole)
		}
		if err := validateURL(u, kind, nil, true); err != nil {
			return "", fmt.Errorf("%s: %w", t.whole, err)
		}
		return value, nil
	}
	var replacements []string
	for _, ref := range t.refs {
		value, err := environment(ref.name)
		if err != nil {
			return "", err
		}
		replacements = append(replacements, ref.marker, value)
	}
	expand := strings.NewReplacer(replacements...).Replace
	if t.url == nil {
		return expand(t.local), nil
	}
	u := *t.url
	u.Scheme, u.Host = expand(u.Scheme), expand(u.Host)
	if strings.ContainsAny(u.Host, "/?#@\\\r\n\x00") {
		return "", fmt.Errorf("environment reference produced an invalid URL host")
	}
	if u.User != nil {
		username := expand(u.User.Username())
		if password, ok := u.User.Password(); ok {
			u.User = url.UserPassword(username, expand(password))
		} else {
			u.User = url.User(username)
		}
	}
	u.Path, u.RawPath = expand(u.Path), ""
	query, _ := url.ParseQuery(u.RawQuery)
	for key, values := range query {
		for i := range values {
			values[i] = expand(values[i])
		}
		query[key] = values
	}
	u.RawQuery = query.Encode()
	if _, err := url.Parse(u.String()); err != nil {
		return "", fmt.Errorf("environment reference produced an invalid connection URL")
	}
	if err := validateURL(&u, kind, nil, true); err != nil {
		return "", err
	}
	return u.String(), nil
}

func environment(name string) (string, error) {
	value := os.Getenv(name)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("environment variable %s is required", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("environment variable %s contains a forbidden control character", name)
	}
	return value, nil
}

func parse(value string, kind Kind) (*template, error) {
	t := &template{raw: value}
	if value == "" {
		return t, nil
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return nil, fmt.Errorf("connection URL contains a forbidden control character")
	}
	if strings.Contains(reference.ReplaceAllString(value, ""), "${") {
		return nil, fmt.Errorf("invalid environment reference; use ${VARIABLE}")
	}
	if match := reference.FindStringSubmatch(value); len(match) != 0 && match[0] == value {
		t.whole = match[1]
		return t, nil
	}
	// Markers must not collide with literal text, including percent-encoded
	// literals, otherwise a literal password could masquerade as a reference.
	decoded, _ := url.PathUnescape(value)
	prefix := "nextaskenvref"
	for strings.Contains(value, prefix) || strings.Contains(decoded, prefix) {
		prefix += "x"
	}
	masked := reference.ReplaceAllStringFunc(value, func(token string) string {
		marker := prefix + strconv.Itoa(len(t.refs)) + "end"
		t.refs = append(t.refs, envReference{marker, token[2 : len(token)-1]})
		return marker
	})
	if !strings.Contains(masked, "://") && kind == Git {
		t.local = masked
		return t, nil
	}
	u, err := parseMaskedURL(masked, prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid connection URL")
	}
	if err := validateURL(u, kind, t.refs, false); err != nil {
		return nil, err
	}
	t.url = u
	return t, nil
}

// net/url requires a numeric port. Temporarily replace an unresolved port while
// parsing, then restore its markers for component-wise expansion.
func parseMaskedURL(value, prefix string) (*url.URL, error) {
	start := strings.Index(value, "://")
	if start < 0 {
		return nil, fmt.Errorf("URL scheme required")
	}
	start += 3
	end := len(value)
	if n := strings.IndexAny(value[start:], "/?#"); n >= 0 {
		end = start + n
	}
	host := value[start:end]
	host = host[strings.LastIndex(host, "@")+1:]
	colon := strings.LastIndex(host, ":")
	if colon >= 0 && strings.Contains(host[colon+1:], prefix) {
		port := host[colon+1:]
		value = value[:end-len(port)] + "0" + value[end:]
		u, err := url.Parse(value)
		if err != nil {
			return nil, err
		}
		u.Host = host
		return u, nil
	}
	return url.Parse(value)
}

func validateURL(u *url.URL, kind Kind, refs []envReference, resolved bool) error {
	if u.Opaque != "" || u.Fragment != "" {
		return fmt.Errorf("connection URL cannot contain an opaque value or fragment")
	}
	if kind == Database {
		if u.Scheme != "postgres" && u.Scheme != "postgresql" {
			return fmt.Errorf("database URL must use postgres:// or postgresql://")
		}
	} else if u.Hostname() == "" && u.Scheme != "file" {
		return fmt.Errorf("connection URL requires a host")
	}
	if kind != Database && (u.RawQuery != "" || u.ForceQuery) {
		return fmt.Errorf("connection URL cannot contain a query string")
	}
	if u.User != nil {
		password, hasPassword := u.User.Password()
		if kind == Git && hasPassword && u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("Git URL passwords are supported only with HTTP(S); use an SSH key for SSH")
		}
		if !resolved && hasPassword && !isReference(password, refs) {
			return fmt.Errorf("URL password must be an environment reference such as ${VARIABLE}")
		}
	}
	if kind == S3 {
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
		if !resolved && !isReference(u.User.Username(), refs) {
			return fmt.Errorf("S3 access key must be an environment reference such as ${VARIABLE}")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid connection URL query")
	}
	for key, values := range query {
		for _, ref := range refs {
			if strings.Contains(key, ref.marker) {
				return fmt.Errorf("URL query parameter names must be literal")
			}
		}
		if !resolved && secretParameter(key) {
			for _, value := range values {
				if !isReference(value, refs) {
					return fmt.Errorf("URL credential parameters must reference environment variables")
				}
			}
		}
	}
	return nil
}

func isReference(value string, refs []envReference) bool {
	for _, ref := range refs {
		if value == ref.marker {
			return true
		}
	}
	return false
}

func secretParameter(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "token") ||
		strings.Contains(key, "secret") || key == "access_key" || key == "access_key_id"
}
