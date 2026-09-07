package integrations

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/TolgaOk/nextask/internal/endpoint"
)

type gitConnection struct {
	remote string
	env    []string
}

// Credentials are passed only in this Git process's environment. The helper is
// scoped to the exact protocol, host, and repository path and never stores them.
const gitCredentialHelper = `!f() {
[ "$1" = get ] || exit 0
protocol= host= path=
while IFS='=' read -r key value; do
  case "$key" in
    protocol) protocol=$value ;;
    host) host=$value ;;
    path) path=$value ;;
  esac
done
[ "$protocol" = "$NEXTASK_GIT_AUTH_PROTOCOL" ] || exit 0
[ "$host" = "$NEXTASK_GIT_AUTH_HOST" ] || exit 0
[ "$path" = "$NEXTASK_GIT_AUTH_PATH" ] || exit 0
printf 'username=%s\npassword=%s\n' "$NEXTASK_GIT_AUTH_USERNAME" "$NEXTASK_GIT_AUTH_PASSWORD"
}; f`

func resolveGitConnection(value string) (gitConnection, error) {
	resolved, err := endpoint.ResolveGitRemote(value)
	if err != nil {
		return gitConnection{}, err
	}
	c := gitConnection{remote: resolved}
	if !strings.Contains(resolved, "://") {
		return c, nil
	}
	u, err := url.Parse(resolved)
	if err != nil {
		return c, fmt.Errorf("invalid Git endpoint")
	}
	if u.User == nil {
		return c, nil
	}
	password, ok := u.User.Password()
	if !ok {
		return c, nil // An ordinary username can select an existing credential helper.
	}
	if u.User.Username() == "" {
		return c, fmt.Errorf("Git HTTP authentication requires a username")
	}
	c.env = []string{
		"NEXTASK_GIT_AUTH_PROTOCOL=" + u.Scheme,
		"NEXTASK_GIT_AUTH_HOST=" + u.Host,
		"NEXTASK_GIT_AUTH_PATH=" + strings.TrimPrefix(u.Path, "/"),
		"NEXTASK_GIT_AUTH_USERNAME=" + u.User.Username(),
		"NEXTASK_GIT_AUTH_PASSWORD=" + password,
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.helper", "GIT_CONFIG_VALUE_1=" + gitCredentialHelper,
		"GIT_CONFIG_KEY_2=credential.useHttpPath", "GIT_CONFIG_VALUE_2=true",
	}
	u.User = nil
	c.remote = u.String()
	return c, nil
}

func redactGitError(message string, env []string) string {
	for _, entry := range env {
		password, ok := strings.CutPrefix(entry, "NEXTASK_GIT_AUTH_PASSWORD=")
		if !ok || password == "" {
			continue
		}
		for _, value := range []string{password, url.QueryEscape(password), url.PathEscape(password), strings.TrimPrefix(url.UserPassword("", password).String(), ":")} {
			message = strings.ReplaceAll(message, value, "REDACTED")
		}
	}
	return message
}
