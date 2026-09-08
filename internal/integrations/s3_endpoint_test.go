package integrations

import (
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/storage"
)

func TestS3RequiresWorkerCredentials(t *testing.T) {
	for _, tc := range []struct {
		access, secret string
		missing        []string
	}{
		{"", "", []string{"CUSTOM_ACCESS"}},
		{"test-access", "", []string{"CUSTOM_SECRET"}},
		{"", "test-secret", []string{"CUSTOM_ACCESS"}},
		{" ", "test-secret", []string{"CUSTOM_ACCESS"}},
	} {
		t.Setenv("CUSTOM_ACCESS", tc.access)
		t.Setenv("CUSTOM_SECRET", tc.secret)
		cfg := storage.Config{}
		cfg.Endpoint = "https://${CUSTOM_ACCESS}:${CUSTOM_SECRET}@storage.invalid"
		_, err := newS3Store(cfg)
		if err == nil {
			t.Fatal("missing credentials accepted")
		}
		for _, name := range []string{"CUSTOM_ACCESS", "CUSTOM_SECRET"} {
			want := false
			for _, missing := range tc.missing {
				want = want || name == missing
			}
			if strings.Contains(err.Error(), name) != want {
				t.Fatalf("incorrect missing variable diagnostic: %v", err)
			}
		}
		if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "test-access") {
			t.Fatal("credential value exposed in error")
		}
	}
}

func TestS3StoreRequiresEndpoint(t *testing.T) {
	if _, err := newS3Store(storage.Config{}); err == nil || err.Error() != "s3 endpoint is required" {
		t.Fatalf("missing endpoint: %v", err)
	}
}
