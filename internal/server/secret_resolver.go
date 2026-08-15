package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
)

const maxSecretFileBytes int64 = 64 << 10

type SecretResolver interface {
	Resolve(context.Context, dsl.SecretRef) (string, error)
}

type localSecretResolver struct {
	secretDir string
}

func newLocalSecretResolver(secretDir string) (*localSecretResolver, error) {
	secretDir = strings.TrimSpace(secretDir)
	if secretDir == "" {
		return &localSecretResolver{}, nil
	}
	absolute, err := filepath.Abs(secretDir)
	if err != nil {
		return nil, fmt.Errorf("resolve secret directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve secret directory symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect secret directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("secret directory must be a directory")
	}
	return &localSecretResolver{secretDir: filepath.Clean(resolved)}, nil
}

func (resolver *localSecretResolver) Resolve(_ context.Context, ref dsl.SecretRef) (string, error) {
	ref, err := normalizeSecretRef(ref)
	if err != nil {
		return "", err
	}
	switch ref.Source {
	case "env":
		value, ok := os.LookupEnv(ref.Ref)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("environment secret %q is not configured", ref.Ref)
		}
		return strings.TrimSpace(value), nil
	case "file":
		return resolver.resolveFile(ref.Ref)
	default:
		return "", fmt.Errorf("unsupported secret source %q", ref.Source)
	}
}

func (resolver *localSecretResolver) resolveFile(ref string) (string, error) {
	if resolver == nil || resolver.secretDir == "" {
		return "", fmt.Errorf("file secret source requires a configured secret directory")
	}
	if filepath.IsAbs(ref) {
		return "", fmt.Errorf("file secret ref must be relative")
	}
	clean := filepath.Clean(ref)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file secret ref must stay inside the secret directory")
	}
	file, err := os.OpenInRoot(resolver.secretDir, clean)
	if err != nil {
		return "", fmt.Errorf("open secret file %q: %w", ref, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect secret file %q: %w", ref, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %q must be a regular file", ref)
	}
	if info.Size() > maxSecretFileBytes {
		return "", fmt.Errorf("secret file %q exceeds %d bytes", ref, maxSecretFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSecretFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", ref, err)
	}
	if int64(len(data)) > maxSecretFileBytes {
		return "", fmt.Errorf("secret file %q exceeds %d bytes", ref, maxSecretFileBytes)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("secret file %q is empty", ref)
	}
	return value, nil
}

func normalizeSecretRef(ref dsl.SecretRef) (dsl.SecretRef, error) {
	ref.Source = strings.ToLower(strings.TrimSpace(ref.Source))
	ref.Ref = strings.TrimSpace(ref.Ref)
	if err := ref.Validate(); err != nil {
		return dsl.SecretRef{}, err
	}
	switch ref.Source {
	case "env":
		if err := validateEnvironmentName(ref.Ref); err != nil {
			return dsl.SecretRef{}, fmt.Errorf("environment secret ref: %w", err)
		}
	case "file":
		if filepath.IsAbs(ref.Ref) {
			return dsl.SecretRef{}, fmt.Errorf("file secret ref must be relative")
		}
	case managedSecretSource:
		if !isManagedSecretID(ref.Ref) {
			return dsl.SecretRef{}, fmt.Errorf("managed secret ref is invalid")
		}
	default:
		return dsl.SecretRef{}, fmt.Errorf("unsupported secret source %q", ref.Source)
	}
	return ref, nil
}
