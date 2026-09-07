package endpoint

import (
	"net/url"
	"strings"
	"testing"
)

func TestTemplates(t *testing.T) {
	for _, tc := range []struct {
		kind  Kind
		value string
	}{
		{Database, "postgresql://nextask:${PASSWORD}@db:5432/nextask?sslmode=require"},
		{Database, "postgres:///nextask?host=/var/run/postgresql"},
		{Database, "postgres://user@db/db?password=${PASSWORD}&port=5432"},
		{Database, "${MY_DATABASE}"},
		{Git, "https://user:${TOKEN}@git.example/repo.git"},
		{Git, "https://user@git.example/repo.git"},
		{Git, "ssh://git@host:2222/repo.git"},
		{Git, "git@host:repo.git"},
		{Git, "${MY_GIT}"},
		{Git, "${ROOT}/repo.git"},
		{S3, "https://${ACCESS}:${SECRET}@storage.example:9000"},
		{S3, "${MY_STORAGE}"},
	} {
		if err := Validate(tc.value, tc.kind); err != nil {
			t.Errorf("template %q: %v", tc.value, err)
		}
	}
}

func TestLiteralSecretsRejected(t *testing.T) {
	for _, tc := range []struct {
		kind  Kind
		value string
	}{
		{Database, "postgres://user:file-secret@db/db"},
		{Database, "postgres://db/db?password=file-secret"},
		{Database, "postgres://db/db?sslpassword=file-secret"},
		{Database, "postgres://db/db?password=${PASSWORD}&password=file-secret"},
		{Database, "postgres://db/db?${KEY}=file-secret"},
		{Database, "host=db password=file-secret"},
		{Git, "https://user:file-secret@host/repo.git"},
		{Git, "https://user:${TOKEN}file-secret@host/repo.git"},
		{Git, "ssh://git:${TOKEN}@host/repo.git"},
		{Git, "https://host/repo.git?token=file-secret"},
		{Git, "https://host/repo.git#file-secret"},
		{Git, "https://user:file-secret@host%ZZ/repo.git"},
		{Git, "https://user:${BAD-NAME}@host/repo.git"},
		{S3, "https://file-secret:${SECRET}@host"},
		{S3, "https://${ACCESS}:file-secret@host"},
		{S3, "https://host"},
		{S3, "https://${ACCESS}:${SECRET}@host/bucket"},
	} {
		err := Validate(tc.value, tc.kind)
		if err == nil || strings.Contains(err.Error(), "file-secret") {
			t.Errorf("accepted or exposed literal credentials: %v", err)
		}
	}
}

func TestCredentialEscaping(t *testing.T) {
	password := "p@ss:/?#%&=+'\"${UNEXPANDED}"
	t.Setenv("MY_PASSWORD", password)
	t.Setenv("MY_USERNAME", "user@example")
	for _, kind := range []Kind{Database, Git, S3} {
		scheme, path := "https", ""
		if kind == Database {
			scheme, path = "postgres", "/db"
		}
		if kind == Git {
			path = "/repo.git"
		}
		resolved, err := Resolve(scheme+"://${MY_USERNAME}:${MY_PASSWORD}@host:5432"+path, kind)
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
	value, err := Resolve("postgres://nextask@${HOST}:${PORT}/${DB_NAME}?password=${PASSWORD}&sslmode=require", Database)
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
	if _, err := Resolve("postgres://user@${HOST}/db", Database); err == nil {
		t.Fatal("host injection accepted")
	}
	t.Setenv("PORT", "bad-port")
	if _, err := Resolve("postgres://user@host:${PORT}/db", Database); err == nil {
		t.Fatal("invalid expanded port accepted")
	}
}

func TestWholeConnectionEnvironment(t *testing.T) {
	for _, tc := range []struct {
		kind  Kind
		value string
	}{
		{Database, "postgres://user:password@host/db?sslmode=require"},
		{Database, "host=localhost user=nextask password='some secret'"},
		{Git, "https://user:token@host/repo.git"},
		{S3, "https://access:secret@host:9000"},
	} {
		t.Setenv("CUSTOM_CONNECTION", tc.value)
		got, err := Resolve("${CUSTOM_CONNECTION}", tc.kind)
		if err != nil || got != tc.value {
			t.Fatalf("whole environment URL changed: %v", err)
		}
	}
	t.Setenv("CUSTOM_CONNECTION", "https://user:private-value@host%ZZ/repo")
	_, err := Resolve("${CUSTOM_CONNECTION}", Git)
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("invalid environment URL exposed: %v", err)
	}
}

func TestMissingVariablesAndControlCharacters(t *testing.T) {
	for _, value := range []string{"", " ", "secret\nvalue"} {
		t.Setenv("MY_TOKEN", value)
		_, err := Resolve("https://user:${MY_TOKEN}@host/repo", Git)
		if err == nil || !strings.Contains(err.Error(), "MY_TOKEN") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("missing/invalid variable diagnostic: %v", err)
		}
	}
	// Checking inactive integration configuration must not resolve references.
	t.Setenv("MY_TOKEN", "")
	if err := Validate("https://user:${MY_TOKEN}@host/repo", Git); err != nil {
		t.Fatal(err)
	}
}

func TestMarkerCollisionCannotHideLiteralPassword(t *testing.T) {
	for _, literal := range []string{"nextaskenvref0end", "%6eextaskenvref0end"} {
		if err := Validate("https://${USERNAME}:"+literal+"@host/repo", Git); err == nil {
			t.Fatal("literal password mistaken for an environment reference")
		}
	}
}
