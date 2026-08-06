package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

type FileArtifactStore struct {
	baseDir string
	mu      fileStoreMutex
}

func NewFileArtifactStore(baseDir string) *FileArtifactStore {
	baseDir = strings.TrimSpace(baseDir)
	return &FileArtifactStore{baseDir: baseDir, mu: fileStoreMutex{baseDir: baseDir}}
}

func (s *FileArtifactStore) Save(_ context.Context, artifact Artifact) (state.ArtifactRef, error) {
	runID := artifact.RunID
	if err := validateRunnerStorageID("run ID", runID); err != nil {
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

	ref := state.ArtifactRef{
		ID:        id,
		RunID:     runID,
		StepID:    strings.TrimSpace(artifact.StepID),
		NodeID:    strings.TrimSpace(artifact.NodeID),
		Type:      strings.TrimSpace(artifact.Type),
		MIMEType:  mimeType,
		Location:  s.payloadPath(runID, id),
		CreatedAt: createdAt,
	}

	metadata, err := marshalRunnerJSONFile(ref)
	if err != nil {
		return state.ArtifactRef{}, err
	}
	if err := writeRunnerBinaryFile(ref.Location, artifact.Data); err != nil {
		return state.ArtifactRef{}, err
	}
	if err := writeRunnerBinaryFile(metadataPath, metadata); err != nil {
		return state.ArtifactRef{}, err
	}
	return ref, nil
}

func (s *FileArtifactStore) Load(_ context.Context, ref state.ArtifactRef) (Artifact, error) {
	if err := validateRunnerStorageID("run ID", ref.RunID); err != nil {
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
		ID:        stored.ID,
		RunID:     stored.RunID,
		StepID:    stored.StepID,
		NodeID:    stored.NodeID,
		Type:      stored.Type,
		MIMEType:  stored.MIMEType,
		Location:  stored.Location,
		CreatedAt: stored.CreatedAt,
		Data:      data,
	}, nil
}

func (s *FileArtifactStore) List(_ context.Context, runID string) ([]state.ArtifactRef, error) {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
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

func (s *FileArtifactStore) DeleteRun(_ context.Context, runID string) error {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.artifactsDir(runID))
}

func (s *FileArtifactStore) artifactsDir(runID string) string {
	return filepath.Join(s.baseDir, runID)
}

func (s *FileArtifactStore) payloadDir(runID string) string {
	return filepath.Join(s.baseDir, runID, "payloads")
}

func (s *FileArtifactStore) metadataPath(runID, artifactID string) string {
	return filepath.Join(s.artifactsDir(runID), artifactID+".json")
}

func (s *FileArtifactStore) payloadPath(runID, artifactID string) string {
	return filepath.Join(s.payloadDir(runID), artifactID+".bin")
}
