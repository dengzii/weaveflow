package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/dsl"
)

func TestLocalSecretResolverResolvesEnvironmentAndBoundedFile(t *testing.T) {
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "model.key"), []byte(" file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEAVEFLOW_TEST_MODEL_KEY", "environment-secret")
	resolver, err := newLocalSecretResolver(secretDir)
	if err != nil {
		t.Fatal(err)
	}

	environmentValue, err := resolver.Resolve(context.Background(), dsl.SecretRef{Source: "env", Ref: "WEAVEFLOW_TEST_MODEL_KEY"})
	if err != nil || environmentValue != "environment-secret" {
		t.Fatalf("environment secret = %q, err = %v", environmentValue, err)
	}
	fileValue, err := resolver.Resolve(context.Background(), dsl.SecretRef{Source: "file", Ref: "model.key"})
	if err != nil || fileValue != "file-secret" {
		t.Fatalf("file secret = %q, err = %v", fileValue, err)
	}
}

func TestLocalSecretResolverRejectsFileOutsideSecretDirectory(t *testing.T) {
	baseDir := t.TempDir()
	secretDir := filepath.Join(baseDir, "secrets")
	if err := os.Mkdir(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(baseDir, "outside.key")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := newLocalSecretResolver(secretDir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolver.Resolve(context.Background(), dsl.SecretRef{Source: "file", Ref: "../outside.key"})
	if err == nil || !strings.Contains(err.Error(), "inside the secret directory") {
		t.Fatalf("traversal error = %v", err)
	}

	symlinkPath := filepath.Join(secretDir, "outside-link.key")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	_, err = resolver.Resolve(context.Background(), dsl.SecretRef{Source: "file", Ref: "outside-link.key"})
	if err == nil {
		t.Fatal("outside symlink unexpectedly resolved")
	}
}

func TestLocalSecretResolverRejectsInvalidSecretSourcesAndFiles(t *testing.T) {
	secretDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(secretDir, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "empty.key"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "large.key"), make([]byte, maxSecretFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := newLocalSecretResolver(secretDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		ref  dsl.SecretRef
		want string
	}{
		{name: "unsupported source", ref: dsl.SecretRef{Source: "vault", Ref: "secret"}, want: "unsupported secret source"},
		{name: "absolute file", ref: dsl.SecretRef{Source: "file", Ref: filepath.Join(secretDir, "empty.key")}, want: "must be relative"},
		{name: "directory", ref: dsl.SecretRef{Source: "file", Ref: "directory"}, want: "regular file"},
		{name: "empty file", ref: dsl.SecretRef{Source: "file", Ref: "empty.key"}, want: "is empty"},
		{name: "oversized file", ref: dsl.SecretRef{Source: "file", Ref: "large.key"}, want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolver.Resolve(context.Background(), test.ref)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v, want %q", err, test.want)
			}
		})
	}

	withoutFileRoot, err := newLocalSecretResolver("")
	if err != nil {
		t.Fatal(err)
	}
	_, err = withoutFileRoot.Resolve(context.Background(), dsl.SecretRef{Source: "file", Ref: "secret.key"})
	if err == nil || !strings.Contains(err.Error(), "requires a configured secret directory") {
		t.Fatalf("file source without root error = %v", err)
	}
}

func TestLoadGraphRuntimeSettingsRejectsLegacyPlaintextVersion(t *testing.T) {
	baseDir := t.TempDir()
	data := []byte(`{"version":3,"environment":{},"models":[{"id":"default","api_key":"plaintext"}]}`)
	if err := os.WriteFile(graphRuntimeSettingsPath(baseDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadGraphRuntimeSettings(baseDir)
	if err == nil || !strings.Contains(err.Error(), "unsupported graph runtime settings version 3") {
		t.Fatalf("legacy settings error = %v", err)
	}
}
