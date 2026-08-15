package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/google/uuid"
)

const managedSecretSource = "managed"

type managedSecretStore struct {
	dir     string
	mu      sync.Mutex
	pending map[string]struct{}
}

func newManagedSecretStore(dir string) (*managedSecretStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("managed secret directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve managed secret directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create managed secret directory: %w", err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("protect managed secret directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve managed secret directory symlinks: %w", err)
	}
	return &managedSecretStore{
		dir:     filepath.Clean(resolved),
		pending: make(map[string]struct{}),
	}, nil
}

func (store *managedSecretStore) Put(ctx context.Context, value string) (dsl.SecretRef, func(bool), error) {
	if store == nil || store.dir == "" {
		return dsl.SecretRef{}, nil, fmt.Errorf("managed secret store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return dsl.SecretRef{}, nil, err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return dsl.SecretRef{}, nil, fmt.Errorf("managed secret value is empty")
	}
	if int64(len(value)) > maxSecretFileBytes {
		return dsl.SecretRef{}, nil, fmt.Errorf("managed secret exceeds %d bytes", maxSecretFileBytes)
	}

	store.mu.Lock()
	secretID, err := store.putLocked(value)
	store.mu.Unlock()
	if err != nil {
		return dsl.SecretRef{}, nil, err
	}
	path := filepath.Join(store.dir, secretID)

	var once sync.Once
	release := func(commit bool) {
		once.Do(func() {
			store.mu.Lock()
			defer store.mu.Unlock()
			if !commit {
				_ = os.Remove(path)
			}
			delete(store.pending, secretID)
		})
	}
	return dsl.SecretRef{Source: managedSecretSource, Ref: secretID}, release, nil
}

func (store *managedSecretStore) putLocked(value string) (string, error) {
	secretID := uuid.NewString()
	path := filepath.Join(store.dir, secretID)
	store.pending[secretID] = struct{}{}
	complete := false
	defer func() {
		if complete {
			return
		}
		delete(store.pending, secretID)
		_ = os.Remove(path)
	}()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create managed secret: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(value); err != nil {
		return "", fmt.Errorf("write managed secret: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync managed secret: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close managed secret: %w", err)
	}
	complete = true
	return secretID, nil
}

func (store *managedSecretStore) sweep(ctx context.Context, referenced map[string]struct{}) error {
	if store == nil || store.dir == "" {
		return fmt.Errorf("managed secret store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		return fmt.Errorf("read managed secret directory: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !isManagedSecretID(entry.Name()) {
			continue
		}
		if _, ok := store.pending[entry.Name()]; ok {
			continue
		}
		if _, ok := referenced[entry.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(store.dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove orphaned managed secret %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func isManagedSecretID(value string) bool {
	secretID, err := uuid.Parse(value)
	return err == nil && secretID.String() == value
}

func (store *managedSecretStore) Resolve(_ context.Context, ref dsl.SecretRef) (string, error) {
	ref.Source = strings.ToLower(strings.TrimSpace(ref.Source))
	ref.Ref = strings.TrimSpace(ref.Ref)
	if ref.Source != managedSecretSource {
		return "", fmt.Errorf("unsupported managed secret source %q", ref.Source)
	}
	if !isManagedSecretID(ref.Ref) {
		return "", fmt.Errorf("managed secret ref is invalid")
	}
	return (&localSecretResolver{secretDir: store.dir}).resolveFile(ref.Ref)
}

func (store *managedSecretStore) SetModel(ctx context.Context, modelID string, value string) (func(bool), error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("model credential value is empty")
	}
	if int64(len(value)) > maxSecretFileBytes {
		return nil, fmt.Errorf("model credential exceeds %d bytes", maxSecretFileBytes)
	}
	return store.updateModel(ctx, modelID, &value)
}

func (store *managedSecretStore) ClearModel(ctx context.Context, modelID string) (func(bool), error) {
	return store.updateModel(ctx, modelID, nil)
}

func (store *managedSecretStore) ResolveModel(ctx context.Context, modelID string) (string, error) {
	if store == nil || store.dir == "" {
		return "", fmt.Errorf("managed secret store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := modelSecretFileName(modelID)
	if err != nil {
		return "", err
	}
	return (&localSecretResolver{secretDir: store.dir}).resolveFile(name)
}

func (store *managedSecretStore) updateModel(ctx context.Context, modelID string, value *string) (func(bool), error) {
	if store == nil || store.dir == "" {
		return nil, fmt.Errorf("managed secret store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := modelSecretFileName(modelID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(store.dir, name)

	store.mu.Lock()
	previous, readErr := os.ReadFile(path)
	previousExists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		store.mu.Unlock()
		return nil, fmt.Errorf("read model credential: %w", readErr)
	}
	if value == nil {
		err = os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			store.mu.Unlock()
			return nil, fmt.Errorf("clear model credential: %w", err)
		}
	} else if err = writeManagedSecretFile(path, *value); err != nil {
		store.mu.Unlock()
		return nil, err
	}
	store.mu.Unlock()

	var once sync.Once
	release := func(commit bool) {
		once.Do(func() {
			if commit {
				return
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if previousExists {
				_ = writeManagedSecretFile(path, string(previous))
				return
			}
			_ = os.Remove(path)
		})
	}
	return release, nil
}

func modelSecretFileName(modelID string) (string, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", fmt.Errorf("model id is required")
	}
	hash := sha256.Sum256([]byte(modelID))
	return fmt.Sprintf("model-%x", hash[:]), nil
}

func writeManagedSecretFile(path string, value string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".model-secret-*.tmp")
	if err != nil {
		return fmt.Errorf("create model credential: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect model credential: %w", err)
	}
	if _, err := temporary.WriteString(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write model credential: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync model credential: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close model credential: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace model credential: %w", err)
	}
	return nil
}

func (s *Server) applyGraphModelCredentialChanges(ctx context.Context, settings *graphRuntimeSettingsRequest) (func(bool), error) {
	noSecrets := func(bool) {}
	if settings == nil || len(settings.Models) == 0 {
		return noSecrets, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	seenModelIDs := make(map[string]struct{}, len(settings.Models))
	for _, model := range settings.Models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			return nil, invalidRequestf("model id is required")
		}
		if _, exists := seenModelIDs[modelID]; exists {
			return nil, invalidRequestf("duplicate model id %q", modelID)
		}
		seenModelIDs[modelID] = struct{}{}
	}

	releases := make([]func(bool), 0)
	rollback := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index](false)
		}
	}
	for index := range settings.Models {
		model := &settings.Models[index]
		value := strings.TrimSpace(model.CredentialValue)
		model.CredentialValue = ""
		if model.CredentialClear && value != "" {
			rollback()
			return nil, invalidRequestf("model %q credential clear conflicts with a new credential", strings.TrimSpace(model.ID))
		}
		if value == "" && !model.CredentialClear {
			continue
		}
		if s == nil || s.managedSecrets == nil {
			rollback()
			return nil, fmt.Errorf("managed model credential storage is unavailable")
		}
		var release func(bool)
		var err error
		if model.CredentialClear {
			release, err = s.managedSecrets.ClearModel(ctx, model.ID)
		} else {
			release, err = s.managedSecrets.SetModel(ctx, model.ID, value)
		}
		if err != nil {
			rollback()
			return nil, fmt.Errorf("store model %q credential: %w", strings.TrimSpace(model.ID), err)
		}
		releases = append(releases, release)
	}

	var once sync.Once
	releaseAll := func(commit bool) {
		once.Do(func() {
			if !commit {
				rollback()
				return
			}
			for _, release := range releases {
				release(true)
			}
		})
	}
	return releaseAll, nil
}

type serverSecretResolver struct {
	managed  *managedSecretStore
	external SecretResolver
}

func (resolver *serverSecretResolver) Resolve(ctx context.Context, ref dsl.SecretRef) (string, error) {
	if resolver == nil {
		return "", fmt.Errorf("secret resolver is unavailable")
	}
	if strings.EqualFold(strings.TrimSpace(ref.Source), managedSecretSource) {
		if resolver.managed == nil {
			return "", fmt.Errorf("managed secret resolver is unavailable")
		}
		return resolver.managed.Resolve(ctx, ref)
	}
	if resolver.external == nil {
		return "", fmt.Errorf("secret resolver is unavailable")
	}
	return resolver.external.Resolve(ctx, ref)
}
