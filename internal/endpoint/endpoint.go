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

var reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Reference selects a complete connection value from the environment.
func Reference(name string) string { return "${" + name + "}" }

func HasReferences(value string) bool { return reference.MatchString(value) }

type template struct {
	whole string
	url   *url.URL
	local string
	refs  []envReference
}

type envReference struct{ marker, name string }

// validate checks syntax and rejects literal secrets without reading the
// environment. Unselected integrations do not require their credentials.
func validate(value string, check func(*template) error) error {
	t, err := parse(value)
	if err != nil || value == "" || t.whole != "" {
		return err
	}
	return check(t)
}

// resolve expands references once. Values substituted into URL components are
// escaped by net/url, so punctuation in a password cannot change the destination.
func resolve(value string, check func(*template) error, checkResolved func(string) error) (string, error) {
	t, err := parse(value)
	if err != nil || value == "" {
		return "", err
	}
	if t.whole != "" {
		value, err := environment(t.whole)
		if err != nil {
			return "", err
		}
		if err := checkResolved(value); err != nil {
			return "", fmt.Errorf("%s: %w", t.whole, err)
		}
		return value, nil
	}
	if err := check(t); err != nil {
		return "", err
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
		value := expand(t.local)
		if err := checkResolved(value); err != nil {
			return "", err
		}
		return value, nil
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
	if err := checkResolved(u.String()); err != nil {
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

func parse(value string) (*template, error) {
	t := &template{}
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
	if !strings.Contains(masked, "://") {
		t.local = masked
		return t, nil
	}
	u, err := parseMaskedURL(masked, prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid connection URL")
	}
	t.url = u
	return t, nil
}

// net/url requires a numeric port. Temporarily replace an unresolved port while
// parsing, then restore its markers for component-wise expansion.
func parseMaskedURL(value, prefix string) (*url.URL, error) {
	start := strings.Index(value, "://") + 3
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
		// Preserve net/url's decoding of IPv6 zone identifiers.
		u.Host = strings.TrimSuffix(u.Host, ":0") + ":" + port
		return u, nil
	}
	return url.Parse(value)
}

// checkURL validates URL structure shared by connection types.
func checkURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("invalid connection URL")
	}
	if u.Opaque != "" || u.Fragment != "" {
		return fmt.Errorf("connection URL cannot contain an opaque value or fragment")
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if strings.ContainsAny(u.User.Username()+password, "\x00\r\n") {
			return fmt.Errorf("URL credentials contain a forbidden control character")
		}
	}
	return nil
}

func checkResolvedURL(value string, check func(*url.URL) error) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid connection URL")
	}
	return check(u)
}

func checkPasswordReference(t *template) error {
	if t.url.User != nil {
		if password, ok := t.url.User.Password(); ok && !isReference(password, t.refs) {
			return fmt.Errorf("URL password must be an environment reference such as ${VARIABLE}")
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
