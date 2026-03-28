package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type FileArtifactStore struct {
	baseDir string
	mu      sync.Mutex
}

func NewFileArtifactStore(baseDir string) *FileArtifactStore {
	return &FileArtifactStore{baseDir: strings.TrimSpace(baseDir)}
}

func (s *FileArtifactStore) Save(_ context.Context, artifact Artifact) (ArtifactRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runID := strings.TrimSpace(artifact.RunID)
	if runID == "" {
		return ArtifactRef{}, fmt.Errorf("artifact run id is required")
	}

	id := strings.TrimSpace(artifact.ID)
	if id == "" {
		id = uuid.NewString()
	}

	createdAt := artifact.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	mimeType := strings.TrimSpace(artifact.MIMEType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	ref := ArtifactRef{
		ID:        id,
		RunID:     runID,
		StepID:    strings.TrimSpace(artifact.StepID),
		NodeID:    strings.TrimSpace(artifact.NodeID),
		Type:      strings.TrimSpace(artifact.Type),
		MIMEType:  mimeType,
		Location:  s.payloadPath(runID, id),
		CreatedAt: createdAt,
	}

	if err := writeRunnerJSONFile(s.metadataPath(runID, id), ref); err != nil {
		return ArtifactRef{}, err
	}
	if err := writeRunnerBinaryFile(ref.Location, artifact.Data); err != nil {
		return ArtifactRef{}, err
	}
	return ref, nil
}

func (s *FileArtifactStore) Load(_ context.Context, ref ArtifactRef) (Artifact, error) {
	var stored ArtifactRef
	if err := readRunnerJSONFile(s.metadataPath(ref.RunID, ref.ID), &stored); err != nil {
		if os.IsNotExist(err) {
			return Artifact{}, ErrRunnerRecordNotFound
		}
		return Artifact{}, err
	}

	data, err := os.ReadFile(stored.Location)
	if err != nil {
		return Artifact{}, err
	}

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

func (s *FileArtifactStore) List(_ context.Context, runID string) ([]ArtifactRef, error) {
	dir := s.artifactsDir(runID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []ArtifactRef{}, nil
	}
	if err != nil {
		return nil, err
	}

	items := make([]ArtifactRef, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		var ref ArtifactRef
		if err := readRunnerJSONFile(filepath.Join(dir, file.Name()), &ref); err != nil {
			return nil, err
		}
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
