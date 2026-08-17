package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

func (store *executionStore) LoadRunDeletionManifest(ctx context.Context, deletionID string) (RunDeletionManifest, error) {
	if err := storeContextErr(ctx); err != nil {
		return RunDeletionManifest{}, err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return RunDeletionManifest{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return loadRunDeletionManifest(store.manifestDir, deletionID)
}

func (store *executionStore) ListRunDeletionManifests(ctx context.Context) ([]RunDeletionManifest, error) {
	if err := storeContextErr(ctx); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return listRunDeletionManifests(store.manifestDir)
}

func (store *executionStore) SaveRunDeletionManifest(ctx context.Context, manifest RunDeletionManifest) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := fruntime.ValidateRunDeletionManifest(manifest); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := requireWritable(store.writer); err != nil {
		return err
	}
	path := filepath.Join(store.manifestDir, manifest.ID+".json")
	existing, err := loadRunDeletionManifest(store.manifestDir, manifest.ID)
	if err != nil && !errors.Is(err, ErrRunnerRecordNotFound) {
		return err
	}
	if err == nil {
		if err := validateManifestUpdate(existing, manifest); err != nil {
			return err
		}
	}
	return writeRunnerJSONFile(path, manifest)
}

func (store *executionStore) ValidateRunDeletionFences(ctx context.Context) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.validateRunDeletionFencesLocked()
}

func (store *executionStore) validateRunDeletionFencesLocked() error {
	manifests, err := listRunDeletionManifests(store.manifestDir)
	if err != nil {
		return err
	}
	manifestByID := make(map[string]RunDeletionManifest, len(manifests))
	for _, manifest := range manifests {
		manifestByID[manifest.ID] = manifest
		if manifest.Phase == fruntime.RunDeletionPlanned {
			root, rootErr := store.readRunLocked(manifest.RootRunID)
			if rootErr != nil {
				return fmt.Errorf("deletion manifest %q planned root: %w", manifest.ID, rootErr)
			}
			if err := validateManifestRoot(manifest, root); err != nil {
				return err
			}
		}
	}
	rootDir := filepath.Dir(store.baseDir)
	for _, baseDir := range []string{
		filepath.Join(rootDir, "execution"),
		filepath.Join(rootDir, "checkpoints"),
		filepath.Join(rootDir, "events"),
		filepath.Join(rootDir, "artifacts"),
	} {
		if err := validateRunDeletionFenceDirectory(baseDir, store, manifestByID); err != nil {
			return err
		}
	}
	return nil
}

func validateRunDeletionFenceDirectory(baseDir string, store *executionStore, manifests map[string]RunDeletionManifest) error {
	directory := filepath.Join(baseDir, runDeletionDirName)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return fmt.Errorf("deletion fence directory %q contains invalid entry %q", directory, entry.Name())
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateRunnerStorageID("fenced run ID", runID); err != nil {
			return err
		}
		var fence runDeletionFence
		path := filepath.Join(directory, entry.Name())
		if err := readRunnerJSONFile(path, &fence); err != nil {
			return fmt.Errorf("read deletion fence %q: %w", path, err)
		}
		if fence.RunID != runID {
			return fmt.Errorf("deletion fence %q identity mismatch", path)
		}
		if err := validateRunnerStorageID("deletion ID", fence.DeletionID); err != nil {
			return fmt.Errorf("deletion fence %q: %w", path, err)
		}
		run, runErr := store.readRunLocked(runID)
		if runErr == nil {
			if run.Deletion == nil || run.Deletion.ID != fence.DeletionID {
				return fmt.Errorf("deletion fence %q does not match run deletion state", path)
			}
			continue
		}
		if !errors.Is(runErr, ErrRunnerRecordNotFound) {
			return runErr
		}
		manifest, exists := manifests[fence.DeletionID]
		if !exists || !fruntimeManifestContainsRun(manifest, runID) {
			return fmt.Errorf("orphan deletion fence %q", path)
		}
	}
	return nil
}

func loadRunDeletionManifest(directory, deletionID string) (RunDeletionManifest, error) {
	path := filepath.Join(directory, deletionID+".json")
	var manifest RunDeletionManifest
	if err := readRunnerJSONFile(path, &manifest); err != nil {
		if os.IsNotExist(err) {
			return RunDeletionManifest{}, ErrRunnerRecordNotFound
		}
		return RunDeletionManifest{}, err
	}
	if manifest.ID != deletionID {
		return RunDeletionManifest{}, fmt.Errorf("deletion manifest %q identity mismatch", deletionID)
	}
	if err := fruntime.ValidateRunDeletionManifest(manifest); err != nil {
		return RunDeletionManifest{}, err
	}
	return manifest, nil
}

func listRunDeletionManifests(directory string) ([]RunDeletionManifest, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return []RunDeletionManifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	manifests := make([]RunDeletionManifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("deletion manifest directory contains invalid entry %q", entry.Name())
		}
		deletionID := strings.TrimSuffix(entry.Name(), ".json")
		manifest, err := loadRunDeletionManifest(directory, deletionID)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(left, right int) bool { return manifests[left].ID < manifests[right].ID })
	return manifests, nil
}

func validateManifestUpdate(existing, next RunDeletionManifest) error {
	if existing.ID != next.ID || existing.RootRunID != next.RootRunID || existing.ParentRunID != next.ParentRunID {
		return fmt.Errorf("deletion manifest %q identity changed", existing.ID)
	}
	if len(existing.RunIDs) != len(next.RunIDs) {
		return fmt.Errorf("deletion manifest %q run plan changed", existing.ID)
	}
	for index := range existing.RunIDs {
		if existing.RunIDs[index] != next.RunIDs[index] {
			return fmt.Errorf("deletion manifest %q run plan changed", existing.ID)
		}
	}
	if next.CreatedAt.Before(existing.CreatedAt) || next.UpdatedAt.Before(existing.UpdatedAt) {
		return fmt.Errorf("deletion manifest %q timestamps moved backwards", existing.ID)
	}
	switch existing.Phase {
	case fruntime.RunDeletionPlanned:
		if next.Phase != fruntime.RunDeletionPlanned && next.Phase != fruntime.RunDeletionUnlinked && next.Phase != fruntime.RunDeletionDeleted {
			return fmt.Errorf("deletion manifest %q phase moved backwards", existing.ID)
		}
	case fruntime.RunDeletionUnlinked:
		if next.Phase != fruntime.RunDeletionUnlinked && next.Phase != fruntime.RunDeletionDeleted {
			return fmt.Errorf("deletion manifest %q phase moved backwards", existing.ID)
		}
	case fruntime.RunDeletionDeleted:
		if next.Phase != fruntime.RunDeletionDeleted {
			return fmt.Errorf("deletion manifest %q is already deleted", existing.ID)
		}
	default:
		return fmt.Errorf("deletion manifest %q has unsupported existing phase %q", existing.ID, existing.Phase)
	}
	return nil
}

func validateManifestRoot(manifest RunDeletionManifest, run fruntime.RunRecord) error {
	if run.RunID != manifest.RootRunID || run.Deletion == nil || run.Deletion.ID != manifest.ID {
		return fmt.Errorf("deletion manifest %q root identity mismatch", manifest.ID)
	}
	if !fruntimeManifestContainsRun(manifest, run.RunID) {
		return fmt.Errorf("deletion manifest %q does not include root run", manifest.ID)
	}
	return nil
}

func fruntimeManifestContainsRun(manifest RunDeletionManifest, runID string) bool {
	for _, candidate := range manifest.RunIDs {
		if candidate == runID {
			return true
		}
	}
	return false
}

var _ RunDeletionManifestStore = (*executionStore)(nil)
var _ RunDeletionFenceScanner = (*executionStore)(nil)
