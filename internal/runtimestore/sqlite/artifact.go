package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

type artifactStore struct {
	store *Store
}

func (artifacts artifactStore) Stage(ctx context.Context, transactionID string, artifact fruntime.Artifact) (fruntime.ArtifactStage, error) {
	store := artifacts.store
	stage := fruntime.ArtifactStage{TransactionID: transactionID}
	if err := fruntime.ValidateStorageID("transaction ID", transactionID); err != nil {
		return stage, err
	}
	if err := fruntime.ValidateStorageID("run ID", artifact.RunID); err != nil {
		return stage, err
	}
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if err := fruntime.ValidateStorageID("artifact ID", artifact.ID); err != nil {
		return stage, err
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	artifact.StepID = strings.TrimSpace(artifact.StepID)
	artifact.NodeID = strings.TrimSpace(artifact.NodeID)
	artifact.Type = strings.TrimSpace(artifact.Type)
	artifact.MIMEType = strings.TrimSpace(artifact.MIMEType)
	if artifact.MIMEType == "" {
		artifact.MIMEType = "application/octet-stream"
	}
	artifact = fruntime.SanitizeArtifact(ctx, artifact)
	artifact.Location = "sqlite/" + artifact.RunID + "/" + artifact.ID
	stage.Ref = artifactRef(artifact)
	metadataArtifact := artifact
	metadataArtifact.Data = nil
	metadata, err := marshal(metadataArtifact)
	if err != nil {
		return stage, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return stage, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadRun(ctx, tx, artifact.RunID); err != nil {
		return stage, err
	}
	if err := ensureRunWritable(ctx, tx, artifact.RunID); err != nil {
		return stage, err
	}
	var existingMetadata, existingPayload []byte
	loadErr := tx.QueryRowContext(ctx, `SELECT metadata,payload FROM artifact_stages WHERE transaction_id=? AND artifact_id=?`, transactionID, artifact.ID).Scan(&existingMetadata, &existingPayload)
	if loadErr == nil {
		var existing fruntime.Artifact
		if err := unmarshal(existingMetadata, &existing); err != nil {
			return stage, err
		}
		existing.Data = existingPayload
		artifact.CreatedAt = existing.CreatedAt
		if !artifactsEqual(existing, artifact) {
			return stage, fmt.Errorf("artifact stage %q payload mismatch", artifact.ID)
		}
		return fruntime.ArtifactStage{TransactionID: transactionID, Ref: artifactRef(existing)}, nil
	}
	if !errors.Is(loadErr, sql.ErrNoRows) {
		return stage, loadErr
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_stages(transaction_id,run_id,artifact_id,created_at_ns,metadata,payload) VALUES(?,?,?,?,?,?)`, transactionID, artifact.RunID, artifact.ID, unixNano(artifact.CreatedAt), metadata, artifact.Data); err != nil {
		return stage, err
	}
	if err := tx.Commit(); err != nil {
		return stage, err
	}
	return stage, nil
}

func (artifacts artifactStore) Finalize(ctx context.Context, transactionID string, stages []fruntime.ArtifactStage) error {
	store := artifacts.store
	if err := fruntime.ValidateStorageID("transaction ID", transactionID); err != nil {
		return err
	}
	if err := fruntime.ValidateArtifactStages(transactionID, stages); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stage := range stages {
		if err := finalizeArtifactStage(ctx, tx, transactionID, stage); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func finalizeArtifactStage(ctx context.Context, tx *sql.Tx, transactionID string, stage fruntime.ArtifactStage) error {
	var runID string
	var createdAt int64
	var metadata, payload []byte
	err := tx.QueryRowContext(ctx, `SELECT run_id,created_at_ns,metadata,payload FROM artifact_stages WHERE transaction_id=? AND artifact_id=?`, transactionID, stage.Ref.ID).Scan(&runID, &createdAt, &metadata, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		var existingMetadata, existingPayload []byte
		if loadErr := tx.QueryRowContext(ctx, `SELECT metadata,payload FROM artifacts WHERE run_id=? AND artifact_id=?`, stage.Ref.RunID, stage.Ref.ID).Scan(&existingMetadata, &existingPayload); errors.Is(loadErr, sql.ErrNoRows) {
			return fmt.Errorf("artifact stage %q: %w", stage.Ref.ID, fruntime.ErrRunnerRecordNotFound)
		} else if loadErr != nil {
			return loadErr
		}
		return nil
	}
	if err != nil {
		return err
	}
	if runID != stage.Ref.RunID {
		return fmt.Errorf("artifact stage %q run mismatch", stage.Ref.ID)
	}
	if err := ensureRunWritable(ctx, tx, runID); err != nil {
		return err
	}
	var existingMetadata, existingPayload []byte
	loadErr := tx.QueryRowContext(ctx, `SELECT metadata,payload FROM artifacts WHERE run_id=? AND artifact_id=?`, runID, stage.Ref.ID).Scan(&existingMetadata, &existingPayload)
	if loadErr == nil && (!bytes.Equal(existingMetadata, metadata) || !bytes.Equal(existingPayload, payload)) {
		return fmt.Errorf("artifact %q already exists with different content", stage.Ref.ID)
	}
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		return loadErr
	}
	if loadErr != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts(run_id,artifact_id,transaction_id,created_at_ns,metadata,payload) VALUES(?,?,?,?,?,?)`, runID, stage.Ref.ID, transactionID, createdAt, metadata, payload); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM artifact_stages WHERE transaction_id=? AND artifact_id=?`, transactionID, stage.Ref.ID)
	return err
}

func (artifacts artifactStore) Discard(ctx context.Context, transactionID string) error {
	store := artifacts.store
	if err := fruntime.ValidateStorageID("transaction ID", transactionID); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `DELETE FROM artifact_stages WHERE transaction_id=?`, transactionID)
	return err
}

func (artifacts artifactStore) Load(ctx context.Context, ref state.ArtifactRef) (fruntime.Artifact, error) {
	store := artifacts.store
	if err := fruntime.ValidateStorageID("run ID", ref.RunID); err != nil {
		return fruntime.Artifact{}, err
	}
	if err := fruntime.ValidateStorageID("artifact ID", ref.ID); err != nil {
		return fruntime.Artifact{}, err
	}
	var metadata, payload []byte
	if err := store.db.QueryRowContext(ctx, `SELECT metadata,payload FROM artifacts WHERE run_id=? AND artifact_id=?`, ref.RunID, ref.ID).Scan(&metadata, &payload); errors.Is(err, sql.ErrNoRows) {
		return fruntime.Artifact{}, fruntime.ErrRunnerRecordNotFound
	} else if err != nil {
		return fruntime.Artifact{}, err
	}
	var artifact fruntime.Artifact
	if err := unmarshal(metadata, &artifact); err != nil {
		return fruntime.Artifact{}, err
	}
	artifact.Data = append([]byte(nil), payload...)
	return artifact, nil
}

func (artifacts artifactStore) List(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	store := artifacts.store
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT metadata FROM artifacts WHERE run_id=? ORDER BY created_at_ns,artifact_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []state.ArtifactRef
	for rows.Next() {
		var metadata []byte
		if err := rows.Scan(&metadata); err != nil {
			return nil, err
		}
		var artifact fruntime.Artifact
		if err := unmarshal(metadata, &artifact); err != nil {
			return nil, err
		}
		refs = append(refs, artifactRef(artifact))
	}
	sort.Slice(refs, func(leftIndex, rightIndex int) bool {
		if refs[leftIndex].CreatedAt.Equal(refs[rightIndex].CreatedAt) {
			return refs[leftIndex].ID < refs[rightIndex].ID
		}
		return refs[leftIndex].CreatedAt.Before(refs[rightIndex].CreatedAt)
	})
	return refs, rows.Err()
}

func (artifacts artifactStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	return artifacts.store.FenceRunDeletion(ctx, runID, deletionID)
}

func (artifacts artifactStore) DeleteRun(ctx context.Context, runID string) error {
	return artifacts.store.deleteRunComponent(ctx, runID, "artifacts")
}

func artifactRef(artifact fruntime.Artifact) state.ArtifactRef {
	return state.ArtifactRef{
		ID: artifact.ID, RunID: artifact.RunID, StepID: artifact.StepID, NodeID: artifact.NodeID,
		OperationKey: artifact.OperationKey, ParentRunID: artifact.ParentRunID, ParentStepID: artifact.ParentStepID,
		ParentTaskID: artifact.ParentTaskID, RootRunID: artifact.RootRunID, RunPath: append([]string(nil), artifact.RunPath...),
		Namespace: artifact.Namespace, Type: artifact.Type, MIMEType: artifact.MIMEType, Location: artifact.Location, CreatedAt: artifact.CreatedAt,
	}
}

func artifactsEqual(left, right fruntime.Artifact) bool {
	leftData, leftErr := marshal(left)
	rightData, rightErr := marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftData, rightData) && bytes.Equal(left.Data, right.Data)
}
