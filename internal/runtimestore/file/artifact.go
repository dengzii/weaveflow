package file

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

type artifactStore struct {
	baseDir string
	mu      storeMutex
	writer  *writerState
}

type artifactReconciliationRecord struct {
	TransactionID string    `json:"transaction_id"`
	RunID         string    `json:"run_id"`
	Action        string    `json:"action"`
	ArtifactCount int       `json:"artifact_count"`
	ReconciledAt  time.Time `json:"reconciled_at"`
}

func newArtifactStore(baseDir string, shared *sync.Mutex) *artifactStore {
	baseDir = strings.TrimSpace(baseDir)
	baseDir = namespacedFileStoreBase(baseDir, "artifacts")
	return &artifactStore{baseDir: baseDir, mu: storeMutex{shared: shared}}
}

func (s *artifactStore) Stage(ctx context.Context, transactionID string, artifact Artifact) (ArtifactStage, error) {
	stage := ArtifactStage{TransactionID: transactionID}
	if err := storeContextErr(ctx); err != nil {
		return stage, err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return stage, err
	}
	runID := artifact.RunID
	if err := validateRunID(runID); err != nil {
		return stage, err
	}

	id := artifact.ID
	if id == "" {
		id = uuid.NewString()
	}
	if err := validateRunnerStorageID("artifact ID", id); err != nil {
		return stage, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return stage, err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, runID, "save an artifact"); err != nil {
		return stage, err
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

	stage.Ref = state.ArtifactRef{
		ID:           id,
		RunID:        runID,
		StepID:       strings.TrimSpace(artifact.StepID),
		NodeID:       strings.TrimSpace(artifact.NodeID),
		OperationKey: strings.TrimSpace(artifact.OperationKey),
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
	metadata, err := marshalRunnerJSONFile(stage)
	if err != nil {
		return stage, err
	}
	if err := s.validateFinalizedArtifact(stage.Ref); err == nil {
		return stage, fmt.Errorf("artifact %q already exists", id)
	} else if !os.IsNotExist(err) {
		return stage, err
	}
	stageMetadataPath := s.stageMetadataPath(runID, transactionID, id)
	stagePayloadPath := s.stagePayloadPath(runID, transactionID, id)
	if _, readErr := os.Stat(stageMetadataPath); readErr == nil {
		var existingStage ArtifactStage
		if err := readRunnerJSONFile(stageMetadataPath, &existingStage); err != nil {
			return stage, err
		}
		existingPayload, payloadErr := os.ReadFile(stagePayloadPath)
		if payloadErr != nil {
			return stage, payloadErr
		}
		comparable := stage
		comparable.Ref.CreatedAt = existingStage.Ref.CreatedAt
		if !artifactStagesEqual(existingStage, comparable) || !bytes.Equal(existingPayload, artifact.Data) {
			return stage, fmt.Errorf("artifact stage %q payload mismatch", id)
		}
		return existingStage, nil
	} else if !os.IsNotExist(readErr) {
		return stage, readErr
	}
	if err := writeRunnerBinaryFile(stagePayloadPath, artifact.Data); err != nil {
		return stage, err
	}
	if err := writeRunnerBinaryFile(stageMetadataPath, metadata); err != nil {
		return stage, err
	}
	return stage, nil
}

func (s *artifactStore) Finalize(ctx context.Context, transactionID string, stages []ArtifactStage) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	return s.finalizeLocked(transactionID, stages)
}

func (s *artifactStore) finalizeLocked(transactionID string, stages []ArtifactStage) error {
	runIDs := make(map[string]struct{})
	for _, expected := range stages {
		if expected.TransactionID != transactionID {
			return fmt.Errorf("artifact stage transaction %q does not match %q", expected.TransactionID, transactionID)
		}
		if err := validateRunID(expected.Ref.RunID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("artifact ID", expected.Ref.ID); err != nil {
			return err
		}
		if err := ensureRunNotDeletingLocked(s.baseDir, expected.Ref.RunID, "finalize an artifact"); err != nil {
			return err
		}
		runIDs[expected.Ref.RunID] = struct{}{}
		var stored ArtifactStage
		if err := readRunnerJSONFile(s.stageMetadataPath(expected.Ref.RunID, transactionID, expected.Ref.ID), &stored); err != nil {
			if os.IsNotExist(err) {
				if finalErr := s.validateFinalizedArtifact(expected.Ref); finalErr == nil {
					continue
				}
			}
			return err
		}
		if !artifactStagesEqual(stored, expected) {
			return fmt.Errorf("artifact stage %q identity mismatch", expected.Ref.ID)
		}
		payload, err := os.ReadFile(s.stagePayloadPath(expected.Ref.RunID, transactionID, expected.Ref.ID))
		if err != nil {
			return err
		}
		if finalErr := s.validateFinalizedArtifact(expected.Ref); finalErr != nil {
			if !os.IsNotExist(finalErr) {
				return finalErr
			}
			metadata, err := marshalRunnerJSONFile(expected.Ref)
			if err != nil {
				return err
			}
			if err := writeRunnerBinaryFile(s.payloadPath(expected.Ref.RunID, expected.Ref.ID), payload); err != nil {
				return err
			}
			if err := writeRunnerBinaryFile(s.metadataPath(expected.Ref.RunID, expected.Ref.ID), metadata); err != nil {
				return err
			}
		} else {
			finalPayload, err := os.ReadFile(s.payloadPath(expected.Ref.RunID, expected.Ref.ID))
			if err != nil {
				return err
			}
			if !bytes.Equal(finalPayload, payload) {
				return fmt.Errorf("artifact %q payload mismatch", expected.Ref.ID)
			}
		}
	}
	for runID := range runIDs {
		if err := removeRunnerDirectory(s.stageTransactionDir(runID, transactionID)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *artifactStore) validateFinalizedArtifact(ref state.ArtifactRef) error {
	var stored state.ArtifactRef
	if err := readRunnerJSONFile(s.metadataPath(ref.RunID, ref.ID), &stored); err != nil {
		return err
	}
	if !artifactRefsEqual(stored, ref) {
		return fmt.Errorf("artifact %q metadata identity mismatch", ref.ID)
	}
	if _, err := os.Stat(s.payloadPath(ref.RunID, ref.ID)); err != nil {
		return err
	}
	return nil
}

func (s *artifactStore) Discard(ctx context.Context, transactionID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	return s.discardLocked(transactionID)
}

func (s *artifactStore) discardLocked(transactionID string) error {
	runs, err := os.ReadDir(s.baseDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !run.IsDir() || strings.HasPrefix(run.Name(), ".") {
			continue
		}
		if err := removeRunnerDirectory(s.stageTransactionDir(run.Name(), transactionID)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *artifactStore) reconcileLocked(resolve func(string) (transactionResultRecord, bool, error)) error {
	runs, err := os.ReadDir(s.baseDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !run.IsDir() || strings.HasPrefix(run.Name(), ".") {
			continue
		}
		stages, err := os.ReadDir(s.stagesDir(run.Name()))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, stageDir := range stages {
			if !stageDir.IsDir() {
				continue
			}
			transactionID := stageDir.Name()
			if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
				return err
			}
			stored, found, err := resolve(transactionID)
			if err != nil {
				return err
			}
			if !found || stored.Result.Outcome != fruntime.TransactionCommitted {
				if err := s.recordReconciliationLocked(artifactReconciliationRecord{
					TransactionID: transactionID, RunID: run.Name(), Action: "discarded", ReconciledAt: time.Now().UTC(),
				}); err != nil {
					return err
				}
				if err := removeRunnerDirectory(s.stageTransactionDir(run.Name(), transactionID)); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			artifactCount := 0
			for _, stage := range stored.Result.Artifacts {
				if stage.Ref.RunID == run.Name() {
					artifactCount++
				}
			}
			if err := s.finalizeLocked(transactionID, stored.Result.Artifacts); err != nil {
				return err
			}
			if err := s.recordReconciliationLocked(artifactReconciliationRecord{
				TransactionID: transactionID, RunID: run.Name(), Action: "finalized", ArtifactCount: artifactCount, ReconciledAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *artifactStore) recordReconciliationLocked(record artifactReconciliationRecord) error {
	path := safeRunnerPath(filepath.Join(filepath.Dir(s.baseDir), ".transactions", "artifact-reconciliation"), record.TransactionID+"-"+record.RunID+".json")
	return writeRunnerJSONFile(path, record)
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
	data, err := readRunnerBinaryFile(payloadPath)
	if err != nil {
		return Artifact{}, err
	}
	stored.Location = payloadPath

	return Artifact{
		ID:           stored.ID,
		RunID:        stored.RunID,
		StepID:       stored.StepID,
		NodeID:       stored.NodeID,
		OperationKey: stored.OperationKey,
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
	files, err := runnerRootedReadDir(dir)
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
		if err := readRunnerJSONFile(safeRunnerPath(dir, file.Name()), &ref); err != nil {
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
	return safeRunnerPath(s.baseDir, runID)
}

func (s *artifactStore) stagesDir(runID string) string {
	return safeRunnerPath(s.artifactsDir(runID), ".stages")
}

func (s *artifactStore) stageTransactionDir(runID, transactionID string) string {
	return safeRunnerPath(s.stagesDir(runID), transactionID)
}

func (s *artifactStore) stageMetadataPath(runID, transactionID, artifactID string) string {
	return safeRunnerPath(s.stageTransactionDir(runID, transactionID), artifactID+".json")
}

func (s *artifactStore) stagePayloadPath(runID, transactionID, artifactID string) string {
	return safeRunnerPath(s.stageTransactionDir(runID, transactionID), artifactID+".bin")
}

func (s *artifactStore) payloadDir(runID string) string {
	return safeRunnerPath(s.baseDir, runID, "payloads")
}

func (s *artifactStore) metadataPath(runID, artifactID string) string {
	return safeRunnerPath(s.artifactsDir(runID), artifactID+".json")
}

func (s *artifactStore) payloadPath(runID, artifactID string) string {
	return safeRunnerPath(s.payloadDir(runID), artifactID+".bin")
}

func artifactStagesEqual(left, right ArtifactStage) bool {
	leftJSON, leftErr := marshalRunnerJSONFile(left)
	rightJSON, rightErr := marshalRunnerJSONFile(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func artifactRefsEqual(left, right state.ArtifactRef) bool {
	leftJSON, leftErr := marshalRunnerJSONFile(left)
	rightJSON, rightErr := marshalRunnerJSONFile(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
