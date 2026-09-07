package endpoint

import (
	"net/url"
	"strings"
	"testing"
)

// The same behavioral cases exercise each independent public API.
type connectionAPI struct {
	name, scheme string
	validate     func(string) error
	resolve      func(string) (string, error)
}

var (
	databaseAPI = connectionAPI{"database", "postgres", ValidateDatabaseURL, ResolveDatabaseURL}
	gitAPI      = connectionAPI{"git", "https", ValidateGitRemote, ResolveGitRemote}
	s3API       = connectionAPI{"s3", "https", ValidateS3Endpoint, ResolveS3Endpoint}
)

func TestTemplates(t *testing.T) {
	for _, tc := range []struct {
		api   connectionAPI
		value string
	}{
		{databaseAPI, "postgresql://nextask:${PASSWORD}@db:5432/nextask?sslmode=require"},
		{databaseAPI, "postgres:///nextask?host=/var/run/postgresql"},
		{databaseAPI, "postgres://user@db/db?password=${PASSWORD}&port=5432"},
		{databaseAPI, "${MY_DATABASE}"},
		{gitAPI, "https://user:${TOKEN}@git.example/repo.git"},
		{gitAPI, "https://user@git.example/repo.git"},
		{gitAPI, "ssh://git@host:2222/repo.git"},
		{gitAPI, "git@host:repo.git"},
		{gitAPI, "${MY_GIT}"},
		{gitAPI, "${ROOT}/repo.git"},
		{s3API, "https://${ACCESS}:${SECRET}@storage.example:9000"},
		{s3API, "${MY_STORAGE}"},
	} {
		if err := tc.api.validate(tc.value); err != nil {
			t.Errorf("template %q: %v", tc.value, err)
		}
	}
}

func TestLiteralSecretsRejected(t *testing.T) {
	for _, tc := range []struct {
		api   connectionAPI
		value string
	}{
		{databaseAPI, "postgres://user:file-secret@db/db"},
		{databaseAPI, "postgres://db/db?password=file-secret"},
		{databaseAPI, "postgres://db/db?sslpassword=file-secret"},
		{databaseAPI, "postgres://db/db?password=${PASSWORD}&password=file-secret"},
		{databaseAPI, "postgres://db/db?${KEY}=file-secret"},
		{databaseAPI, "host=db password=file-secret"},
		{gitAPI, "https://user:file-secret@host/repo.git"},
		{gitAPI, "https://user:${TOKEN}file-secret@host/repo.git"},
		{gitAPI, "ssh://git:${TOKEN}@host/repo.git"},
		{gitAPI, "https://host/repo.git?token=file-secret"},
		{gitAPI, "https://host/repo.git#file-secret"},
		{gitAPI, "https://user:file-secret@host%ZZ/repo.git"},
		{gitAPI, "https://user:${BAD-NAME}@host/repo.git"},
		{s3API, "https://file-secret:${SECRET}@host"},
		{s3API, "https://${ACCESS}:file-secret@host"},
		{s3API, "https://host"},
		{s3API, "https://${ACCESS}:${SECRET}@host/bucket"},
	} {
		err := tc.api.validate(tc.value)
		if err == nil || strings.Contains(err.Error(), "file-secret") {
			t.Errorf("accepted or exposed literal credentials: %v", err)
		}
	}
}

func TestCredentialEscaping(t *testing.T) {
	password := "p@ss:/?#%&=+'\"${UNEXPANDED}"
	t.Setenv("MY_PASSWORD", password)
	t.Setenv("MY_USERNAME", "user@example")
	for _, api := range []connectionAPI{databaseAPI, gitAPI, s3API} {
		scheme, path := "https", ""
		if api.scheme == "postgres" {
			scheme, path = "postgres", "/db"
		}
		if api.name == "git" {
			path = "/repo.git"
		}
		resolved, err := api.resolve(scheme + "://${MY_USERNAME}:${MY_PASSWORD}@host:5432" + path)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(resolved)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := u.User.Password()
		if got != password || u.User.Username() != "user@example" || u.Host != "host:5432" || u.RawQuery != "" || u.Fragment != "" {
			t.Fatal("credential expansion changed URL boundaries or password bytes")
		}
	}
}

func TestReferencesAcrossComponents(t *testing.T) {
	t.Setenv("HOST", "localhost")
	t.Setenv("PORT", "5432")
	t.Setenv("DB_NAME", "my db")
	t.Setenv("PASSWORD", "p@ss&other=value")
	value, err := databaseAPI.resolve("postgres://nextask@${HOST}:${PORT}/${DB_NAME}?password=${PASSWORD}&sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "localhost:5432" || u.Path != "/my db" || u.Query().Get("password") != "p@ss&other=value" || u.Query().Get("other") != "" {
		t.Fatal("component expansion lost values or introduced query parameters")
	}
	t.Setenv("HOST", "evil/path@other")
	if _, err := databaseAPI.resolve("postgres://user@${HOST}/db"); err == nil {
		t.Fatal("host injection accepted")
	}
	t.Setenv("PORT", "bad-port")
	if _, err := databaseAPI.resolve("postgres://user@host:${PORT}/db"); err == nil {
		t.Fatal("invalid expanded port accepted")
	}
}

func TestWholeConnectionEnvironment(t *testing.T) {
	for _, tc := range []struct {
		api   connectionAPI
		value string
	}{
		{databaseAPI, "postgres://user:password@host/db?sslmode=require"},
		{databaseAPI, "host=localhost user=nextask password='some secret'"},
		{gitAPI, "https://user:token@host/repo.git"},
		{s3API, "https://access:secret@host:9000"},
	} {
		t.Setenv("CUSTOM_CONNECTION", tc.value)
		got, err := tc.api.resolve("${CUSTOM_CONNECTION}")
		if err != nil || got != tc.value {
			t.Fatalf("whole environment URL changed: %v", err)
		}
	}
	t.Setenv("CUSTOM_CONNECTION", "https://user:private-value@host%ZZ/repo")
	_, err := gitAPI.resolve("${CUSTOM_CONNECTION}")
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("invalid environment URL exposed: %v", err)
	}
}

func TestMissingVariablesAndControlCharacters(t *testing.T) {
	for _, value := range []string{"", " ", "secret\nvalue"} {
		t.Setenv("MY_TOKEN", value)
		_, err := gitAPI.resolve("https://user:${MY_TOKEN}@host/repo")
		if err == nil || !strings.Contains(err.Error(), "MY_TOKEN") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("missing/invalid variable diagnostic: %v", err)
		}
	}
	// Checking inactive integration configuration must not resolve references.
	t.Setenv("MY_TOKEN", "")
	if err := gitAPI.validate("https://user:${MY_TOKEN}@host/repo"); err != nil {
		t.Fatal(err)
	}
}

func TestMarkerCollisionCannotHideLiteralPassword(t *testing.T) {
	for _, literal := range []string{"nextaskenvref0end", "%6eextaskenvref0end"} {
		if err := gitAPI.validate("https://${USERNAME}:" + literal + "@host/repo"); err == nil {
			t.Fatal("literal password mistaken for an environment reference")
		}
	}
}

func TestConnectionBoundaryCases(t *testing.T) {
	t.Setenv("USER", "nextask")
	t.Setenv("PASSWORD", "test-password")
	t.Setenv("PORT", "5432")
	t.Setenv("ROOT", "/srv/git")
	for _, tc := range []struct {
		name        string
		api         connectionAPI
		input, want string
	}{
		{"empty DB", databaseAPI, "", ""},
		{"empty Git", gitAPI, "", ""},
		{"empty S3", s3API, "", ""},
		{"literal username", gitAPI, "https://nextask@host/repo", "https://nextask@host/repo"},
		{"username reference", gitAPI, "https://${USER}@host/repo", "https://nextask@host/repo"},
		{"local reference", gitAPI, "${ROOT}/repo.git", "/srv/git/repo.git"},
		{"local literal", gitAPI, "/srv/git/repo.git", "/srv/git/repo.git"},
		{"file URL", gitAPI, "file:///srv/git/repo.git", "file:///srv/git/repo.git"},
		{"IPv6", databaseAPI, "postgres://[::1]:${PORT}/db", "postgres://[::1]:5432/db"},
		{"IPv6 zone", databaseAPI, "postgres://[fe80::1%25en0]:${PORT}/db", "postgres://[fe80::1%25en0]:5432/db"},
		{"port without path", gitAPI, "https://host:${PORT}", "https://host:5432"},
		{"S3 trailing slash", s3API, "http://${USER}:${PASSWORD}@[::1]:9000/", "http://nextask:test-password@[::1]:9000/"},
		{"SSL password", databaseAPI, "postgres://host/db?sslpassword=${PASSWORD}", "postgres://host/db?sslpassword=test-password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.api.validate(tc.input); err != nil {
				t.Fatal(err)
			}
			got, err := tc.api.resolve(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("unexpected resolution: %v", err)
			}
		})
	}
}

func TestInvalidConnections(t *testing.T) {
	for _, tc := range []struct {
		name           string
		api            connectionAPI
		input, message string
	}{
		{"newline", gitAPI, "https://host/repo\n", "control character"},
		{"carriage return", databaseAPI, "postgres://host/db\r", "control character"},
		{"NUL", s3API, "https://host\x00", "control character"},
		{"unfinished reference", gitAPI, "https://user:${TOKEN@host/repo", "environment reference"},
		{"bad DB scheme", databaseAPI, "mysql://host/db", "postgres://"},
		{"absent Git host", gitAPI, "https:///repo", "requires a host"},
		{"empty query", gitAPI, "https://host/repo?", "query string"},
		{"encoded username control", gitAPI, "https://user%0A@host/repo", "control character"},
		{"encoded password control", databaseAPI, "postgres://user:bad%0D@host/db", "control character"},
		{"S3 bad scheme", s3API, "ftp://${USER}:${PASSWORD}@host", "http(s)"},
		{"S3 no password", s3API, "https://${USER}@host", "access key and secret key"},
		{"S3 empty access", s3API, "https://:${PASSWORD}@host", "access key and secret key"},
		{"S3 missing host", s3API, "https:///", "requires a host"},
		{"S3 query value", s3API, "https://${USER}:${PASSWORD}@host?token=value", "query string"},
		{"S3 query", s3API, "https://${USER}:${PASSWORD}@host?", "query string"},
		{"S3 bad structure", s3API, "https://${USER}:${PASSWORD}@host#fragment", "fragment"},
		{"bad query escape", databaseAPI, "postgres://host/db?port=%ZZ", "URL query"},
		{"bad query separator", databaseAPI, "postgres://host/db?port=5432;sslmode=require", "URL query"},
		{"bad masked host", databaseAPI, "postgres://host%ZZ:${PORT}/db", "invalid connection URL"},
		{"encoded credential key", databaseAPI, "postgres://host/db?%70assword=literal", "credential parameters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, check := range []func(string) error{
				tc.api.validate, func(v string) error { _, err := tc.api.resolve(v); return err },
			} {
				if err := check(tc.input); err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("expected %s, got %v", tc.message, err)
				}
			}
		})
	}
}

func TestWholeEnvironmentValidation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		api            connectionAPI
		value, message string
	}{
		{"missing", gitAPI, "", "is required"},
		{"blank", s3API, " \t", "is required"},
		{"control", databaseAPI, "postgres://host/db\r", "control character"},
		{"local Git", gitAPI, "/srv/git/repo.git", ""},
		{"SSH shorthand", gitAPI, "git@host:repo.git", ""},
		{"remote name", gitAPI, "origin", ""},
		{"opaque", gitAPI, "https:opaque://host", "opaque value"},
		{"DB scheme", databaseAPI, "https://host/db", "postgres://"},
		{"S3 missing secret", s3API, "https://access@host", "access key and secret key"},
		{"S3 empty secret", s3API, "https://access:@host", "access key and secret key"},
		{"S3 empty access", s3API, "https://:secret@host", "access key and secret key"},
		{"S3 no userinfo", s3API, "https://host", "requires access-key"},
		{"encoded control", gitAPI, "https://user:secret%00@host/repo", "control character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CONNECTION_TEST", tc.value)
			ref := Reference("CONNECTION_TEST")
			if !HasReferences(ref) || HasReferences(tc.value) {
				t.Fatal("incorrect reference detection")
			}
			got, err := tc.api.resolve(ref)
			if tc.message == "" {
				if err != nil || got != tc.value {
					t.Fatalf("whole value changed: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), "CONNECTION_TEST") {
				t.Fatalf("expected variable name and %s, got %v", tc.message, err)
			}
		})
	}
	t.Setenv("HOST_TEST", ":5432")
	if _, err := gitAPI.resolve("https://${HOST_TEST}/repo"); err == nil || !strings.Contains(err.Error(), "requires a host") {
		t.Fatalf("expanded host missing hostname: %v", err)
	}
}

func FuzzCredentialRoundTrip(f *testing.F) {
	for _, value := range []string{"plain", "p@ss:/?#%&=", "${OTHER}", "space password", "非ASCII", "nextaskenvref0end"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, password string) {
		if strings.TrimSpace(password) == "" || strings.ContainsAny(password, "\x00\r\n") {
			t.Skip()
		}
		t.Setenv("FUZZ_CREDENTIAL", password)
		for _, api := range []connectionAPI{databaseAPI, gitAPI, s3API} {
			scheme := "https"
			if api.scheme == "postgres" {
				scheme = "postgres"
			}
			t.Setenv("FUZZ_USER", "nextask")
			got, err := api.resolve(scheme + "://${FUZZ_USER}:${FUZZ_CREDENTIAL}@host:5432")
			if err != nil {
				t.Fatalf("valid credential rejected: %v", err)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal("resolved URL cannot be parsed")
			}
			restored, _ := u.User.Password()
			if restored != password || u.Host != "host:5432" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				t.Fatal("credential bytes or URL boundaries changed")
			}
		}
	})
}

func TestGitRemotePrefixExpansion(t *testing.T) {
	for _, value := range []string{"https://user:password@host", "https://user:bad%0A@host", "https://host%ZZ"} {
		t.Setenv("GIT_BASE", value)
		got, err := ResolveGitRemote("${GIT_BASE}/repo.git")
		if value == "https://user:password@host" {
			if err != nil || got != value+"/repo.git" {
				t.Fatalf("valid prefix expansion failed: %v", err)
			}
		} else if err == nil {
			t.Fatal("prefix expansion bypassed URL validation")
		}
	}
}

func FuzzConnectionTemplates(f *testing.F) {
	for _, value := range []string{
		"", "${FUZZ_VALUE}", "${FUZZ_VALUE}/repo", "postgres://user:${FUZZ_VALUE}@host/db",
		"https://${FUZZ_VALUE}:${FUZZ_VALUE}@host", "https://host:${FUZZ_VALUE}",
		"postgres://[fe80::1%25en0]:${FUZZ_VALUE}/db", "postgres://host/db?password=${FUZZ_VALUE}",
		"https://user:nextaskenvref0end@${FUZZ_VALUE}/repo", "https://host%ZZ:${FUZZ_VALUE}",
	} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		t.Setenv("FUZZ_VALUE", "5432")
		for _, api := range []connectionAPI{databaseAPI, gitAPI, s3API} {
			validation := api.validate(value)
			_, resolution := api.resolve(value)
			if validation != nil && resolution == nil {
				t.Fatal("resolution accepted an invalid template")
			}
		}
	})
}
