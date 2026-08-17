package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

type artifactStore struct {
	baseDir string
	mu      storeMutex
	writer  *writerState
}

func newArtifactStore(baseDir string, shared *sync.Mutex) *artifactStore {
	baseDir = strings.TrimSpace(baseDir)
	baseDir = namespacedFileStoreBase(baseDir, "artifacts")
	return &artifactStore{baseDir: baseDir, mu: storeMutex{shared: shared}}
}

func (s *artifactStore) Save(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
	if err := storeContextErr(ctx); err != nil {
		return state.ArtifactRef{}, err
	}
	runID := artifact.RunID
	if err := validateRunID(runID); err != nil {
		return state.ArtifactRef{}, err
	}

	id := artifact.ID
	if id == "" {
		id = uuid.NewString()
	}
	if err := validateRunnerStorageID("artifact ID", id); err != nil {
		return state.ArtifactRef{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return state.ArtifactRef{}, err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, runID, "save an artifact"); err != nil {
		return state.ArtifactRef{}, err
	}

	metadataPath := s.metadataPath(runID, id)
	if err := ensureRunnerRecordDoesNotExist(metadataPath, "artifact", id); err != nil {
		return state.ArtifactRef{}, err
	}

	createdAt := artifact.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	mimeType := strings.TrimSpace(artifact.MIMEType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	artifact.MIMEType = mimeType
	artifact = sanitizeArtifact(ctx, artifact)

	ref := state.ArtifactRef{
		ID:           id,
		RunID:        runID,
		StepID:       strings.TrimSpace(artifact.StepID),
		NodeID:       strings.TrimSpace(artifact.NodeID),
		ParentRunID:  strings.TrimSpace(artifact.ParentRunID),
		ParentStepID: strings.TrimSpace(artifact.ParentStepID),
		ParentTaskID: strings.TrimSpace(artifact.ParentTaskID),
		RootRunID:    strings.TrimSpace(artifact.RootRunID),
		RunPath:      append([]string(nil), artifact.RunPath...),
		Namespace:    strings.Trim(strings.TrimSpace(artifact.Namespace), "/"),
		Type:         strings.TrimSpace(artifact.Type),
		MIMEType:     artifact.MIMEType,
		Location:     s.payloadPath(runID, id),
		CreatedAt:    createdAt,
	}

	metadata, err := marshalRunnerJSONFile(ref)
	if err != nil {
		return state.ArtifactRef{}, err
	}
	if err := writeRunnerBinaryFile(ref.Location, artifact.Data); err != nil {
		return state.ArtifactRef{}, err
	}
	if err := writeRunnerBinaryFile(metadataPath, metadata); err != nil {
		if cleanupErr := removeRunnerFile(ref.Location); cleanupErr != nil {
			return state.ArtifactRef{}, errors.Join(err, fmt.Errorf("cleanup artifact payload: %w", cleanupErr))
		}
		return state.ArtifactRef{}, err
	}
	return ref, nil
}

func (s *artifactStore) Load(ctx context.Context, ref state.ArtifactRef) (Artifact, error) {
	if err := storeContextErr(ctx); err != nil {
		return Artifact{}, err
	}
	if err := validateRunID(ref.RunID); err != nil {
		return Artifact{}, err
	}
	if err := validateRunnerStorageID("artifact ID", ref.ID); err != nil {
		return Artifact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var stored state.ArtifactRef
	if err := readRunnerJSONFile(s.metadataPath(ref.RunID, ref.ID), &stored); err != nil {
		if os.IsNotExist(err) {
			return Artifact{}, ErrRunnerRecordNotFound
		}
		return Artifact{}, err
	}

	if stored.RunID != ref.RunID || stored.ID != ref.ID {
		return Artifact{}, fmt.Errorf("artifact %q metadata identity mismatch", ref.ID)
	}
	payloadPath := s.payloadPath(ref.RunID, ref.ID)
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		return Artifact{}, err
	}
	stored.Location = payloadPath

	return Artifact{
		ID:           stored.ID,
		RunID:        stored.RunID,
		StepID:       stored.StepID,
		NodeID:       stored.NodeID,
		ParentRunID:  stored.ParentRunID,
		ParentStepID: stored.ParentStepID,
		ParentTaskID: stored.ParentTaskID,
		RootRunID:    stored.RootRunID,
		RunPath:      append([]string(nil), stored.RunPath...),
		Namespace:    stored.Namespace,
		Type:         stored.Type,
		MIMEType:     stored.MIMEType,
		Location:     stored.Location,
		CreatedAt:    stored.CreatedAt,
		Data:         data,
	}, nil
}

func (s *artifactStore) List(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	if err := storeContextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.artifactsDir(runID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []state.ArtifactRef{}, nil
	}
	if err != nil {
		return nil, err
	}

	items := make([]state.ArtifactRef, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		var ref state.ArtifactRef
		if err := readRunnerJSONFile(filepath.Join(dir, file.Name()), &ref); err != nil {
			return nil, err
		}
		if err := validateRunnerStorageID("artifact ID", ref.ID); err != nil {
			return nil, err
		}
		if ref.RunID != runID || file.Name() != ref.ID+".json" {
			return nil, fmt.Errorf("artifact %q metadata identity mismatch", ref.ID)
		}
		ref.Location = s.payloadPath(runID, ref.ID)
		items = append(items, ref)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *artifactStore) DeleteRun(ctx context.Context, runID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	if err := requireRunDeletionLocked(ctx, s.baseDir, runID); err != nil {
		return err
	}
	return removeRunnerDirectory(s.artifactsDir(runID))
}

func (s *artifactStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	return fenceRunDeletionLocked(ctx, s.baseDir, runID, deletionID)
}

func (s *artifactStore) artifactsDir(runID string) string {
	return filepath.Join(s.baseDir, runID)
}

func (s *artifactStore) payloadDir(runID string) string {
	return filepath.Join(s.baseDir, runID, "payloads")
}

func (s *artifactStore) metadataPath(runID, artifactID string) string {
	return filepath.Join(s.artifactsDir(runID), artifactID+".json")
}

func (s *artifactStore) payloadPath(runID, artifactID string) string {
	return filepath.Join(s.payloadDir(runID), artifactID+".bin")
}

func (s *artifactStore) deletionPath(runID string) string {
	return runDeletionPath(s.baseDir, runID)
}
