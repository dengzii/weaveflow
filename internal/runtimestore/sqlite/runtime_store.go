package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/google/uuid"
)

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (store *Store) CreateRun(ctx context.Context, run fruntime.RunRecord) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := fruntime.ValidateStorageID("run ID", run.RunID); err != nil {
		return err
	}
	run = fruntime.SanitizeRunRecord(ctx, run)
	if err := fruntime.ValidateNewRunDeletion(run); err != nil {
		return err
	}
	if err := fruntime.ValidateRunChildState(run); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureRunWritable(ctx, tx, run.RunID); err != nil {
		return err
	}
	if _, err := loadRun(ctx, tx, run.RunID); err == nil {
		return fmt.Errorf("run %q already exists", run.RunID)
	} else if !errors.Is(err, fruntime.ErrRunnerRecordNotFound) {
		return err
	}
	if run.ParentRunID != "" {
		if err := ensureRunWritable(ctx, tx, run.ParentRunID); err != nil {
			return err
		}
		parent, err := loadRun(ctx, tx, run.ParentRunID)
		if err != nil {
			return fmt.Errorf("parent run %q: %w", run.ParentRunID, err)
		}
		if err := fruntime.ValidateNewRunParent(run, parent); err != nil {
			return err
		}
	}
	if err := insertRun(ctx, tx, run); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run fruntime.RunRecord) (fruntime.RunRecord, error) {
	if err := contextErr(ctx); err != nil {
		return fruntime.RunRecord{}, err
	}
	if err := fruntime.ValidateStorageID("run ID", run.RunID); err != nil {
		return fruntime.RunRecord{}, err
	}
	run = fruntime.SanitizeRunRecord(ctx, run)
	if err := fruntime.ValidateRunChildState(run); err != nil {
		return fruntime.RunRecord{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fruntime.RunRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := loadRun(ctx, tx, run.RunID)
	if err != nil {
		return fruntime.RunRecord{}, err
	}
	if existing.Revision != expectedRevision || run.Revision != expectedRevision {
		return fruntime.RunRecord{}, &fruntime.RunRevisionConflictError{RunID: run.RunID, Expected: expectedRevision, Actual: existing.Revision}
	}
	if err := fruntime.ValidateRunDeletionTransition(ctx, existing, run); err != nil {
		return fruntime.RunRecord{}, err
	}
	if err := fruntime.ValidateRunExecutionLeaseTransition(ctx, existing, run); err != nil {
		return fruntime.RunRecord{}, err
	}
	run.Revision++
	result, err := updateRun(ctx, tx, expectedRevision, run)
	if err != nil {
		return fruntime.RunRecord{}, err
	}
	if result == 0 {
		actual, loadErr := loadRun(ctx, tx, run.RunID)
		if loadErr != nil {
			return fruntime.RunRecord{}, loadErr
		}
		return fruntime.RunRecord{}, &fruntime.RunRevisionConflictError{RunID: run.RunID, Expected: expectedRevision, Actual: actual.Revision}
	}
	if err := tx.Commit(); err != nil {
		return fruntime.RunRecord{}, err
	}
	return fruntime.CloneRunRecord(run), nil
}

func (store *Store) GetRun(ctx context.Context, runID string) (fruntime.RunRecord, error) {
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return fruntime.RunRecord{}, err
	}
	return loadRun(ctx, store.db, runID)
}

func (store *Store) ListRuns(ctx context.Context, filter fruntime.RunFilter) ([]fruntime.RunRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT data FROM runs ORDER BY started_at_ns, run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	statuses := make(map[fruntime.RunStatus]struct{}, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses[status] = struct{}{}
	}
	var runs []fruntime.RunRecord
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var run fruntime.RunRecord
		if err := unmarshal(data, &run); err != nil {
			return nil, err
		}
		if len(statuses) > 0 {
			if _, ok := statuses[run.Status]; !ok {
				continue
			}
		}
		if filter.ParentRunID != "" && run.ParentRunID != filter.ParentRunID ||
			filter.ParentTaskID != "" && run.ParentTaskID != filter.ParentTaskID ||
			filter.RootRunID != "" && run.RootRunID != filter.RootRunID ||
			filter.Namespace != "" && run.Namespace != filter.Namespace {
			continue
		}
		runs = append(runs, fruntime.CloneRunRecord(run))
	}
	return runs, rows.Err()
}

func (store *Store) AppendStep(ctx context.Context, step fruntime.StepRecord) error {
	return store.writeStep(ctx, fruntime.StepWriteAppend, step)
}

func (store *Store) UpdateStep(ctx context.Context, step fruntime.StepRecord) error {
	return store.writeStep(ctx, fruntime.StepWriteUpdate, step)
}

func (store *Store) writeStep(ctx context.Context, mode fruntime.StepWriteMode, step fruntime.StepRecord) error {
	step = fruntime.SanitizeStepRecord(ctx, step)
	if err := fruntime.ValidateStorageID("run ID", step.RunID); err != nil {
		return err
	}
	if err := fruntime.ValidateStorageID("step ID", step.StepID); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	run, err := loadRun(ctx, tx, step.RunID)
	if err != nil {
		return err
	}
	if err := ensureRunWritable(ctx, tx, step.RunID); err != nil {
		return err
	}
	if err := fruntime.EnsureRunNotDeleting(run, "write a step"); err != nil {
		return err
	}
	existing, loadErr := loadStep(ctx, tx, step.StepID)
	switch mode {
	case fruntime.StepWriteAppend:
		if err := fruntime.ValidateStepEffect(step); err != nil {
			return err
		}
		if loadErr == nil {
			return fmt.Errorf("step %q already exists", step.StepID)
		}
		if !errors.Is(loadErr, fruntime.ErrRunnerRecordNotFound) {
			return loadErr
		}
	case fruntime.StepWriteUpdate:
		if loadErr != nil || existing.RunID != step.RunID {
			return fruntime.ErrRunnerRecordNotFound
		}
		if err := fruntime.ValidateStepEffectTransition(existing, step); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid step write mode %q", mode)
	}
	if err := putStep(ctx, tx, step); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) GetStep(ctx context.Context, stepID string) (fruntime.StepRecord, error) {
	if err := fruntime.ValidateStorageID("step ID", stepID); err != nil {
		return fruntime.StepRecord{}, err
	}
	return loadStep(ctx, store.db, stepID)
}

func (store *Store) ListSteps(ctx context.Context, runID string) ([]fruntime.StepRecord, error) {
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT data FROM steps WHERE run_id = ? ORDER BY started_at_ns, step_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []fruntime.StepRecord
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var step fruntime.StepRecord
		if err := unmarshal(data, &step); err != nil {
			return nil, err
		}
		steps = append(steps, fruntime.CloneStepRecord(step))
	}
	return steps, rows.Err()
}

func (store *Store) Save(ctx context.Context, record fruntime.CheckpointRecord, payload []byte) error {
	if err := fruntime.ValidateStorageID("run ID", record.RunID); err != nil {
		return err
	}
	if err := fruntime.ValidateStorageID("checkpoint ID", record.CheckpointID); err != nil {
		return err
	}
	record.PayloadRef = ""
	data, err := marshal(record)
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadRun(ctx, tx, record.RunID); err != nil {
		return err
	}
	if err := ensureRunWritable(ctx, tx, record.RunID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoints(checkpoint_id, run_id, created_at_ns, metadata, payload) VALUES(?,?,?,?,?)`, record.CheckpointID, record.RunID, unixNano(record.CreatedAt), data, append([]byte(nil), payload...)); err != nil {
		return fmt.Errorf("save checkpoint %q: %w", record.CheckpointID, err)
	}
	return tx.Commit()
}

func (store *Store) Load(ctx context.Context, checkpointID string) (fruntime.CheckpointRecord, []byte, error) {
	if err := fruntime.ValidateStorageID("checkpoint ID", checkpointID); err != nil {
		return fruntime.CheckpointRecord{}, nil, err
	}
	var metadata, payload []byte
	if err := store.db.QueryRowContext(ctx, `SELECT metadata, payload FROM checkpoints WHERE checkpoint_id = ?`, checkpointID).Scan(&metadata, &payload); errors.Is(err, sql.ErrNoRows) {
		return fruntime.CheckpointRecord{}, nil, fruntime.ErrRunnerRecordNotFound
	} else if err != nil {
		return fruntime.CheckpointRecord{}, nil, err
	}
	var record fruntime.CheckpointRecord
	if err := unmarshal(metadata, &record); err != nil {
		return fruntime.CheckpointRecord{}, nil, err
	}
	return record, append([]byte(nil), payload...), nil
}

func (store *Store) List(ctx context.Context, runID string) ([]fruntime.CheckpointRecord, error) {
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT metadata FROM checkpoints WHERE run_id = ? ORDER BY created_at_ns, checkpoint_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []fruntime.CheckpointRecord
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var record fruntime.CheckpointRecord
		if err := unmarshal(data, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) Publish(ctx context.Context, event fruntime.Event) error {
	return store.PublishBatch(ctx, []fruntime.Event{event})
}

func (store *Store) PublishBatch(ctx context.Context, events []fruntime.Event) error {
	events = fruntime.SanitizeEvents(ctx, events)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, event := range events {
		if fruntime.IsStreamingEvent(event.Type) {
			continue
		}
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *Store) ListEvents(runID string) ([]fruntime.Event, error) {
	if err := fruntime.ValidateStorageID("run ID", runID); err != nil {
		return nil, err
	}
	rows, err := store.db.Query(`SELECT data FROM events WHERE run_id = ? ORDER BY sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []fruntime.Event
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var event fruntime.Event
		if err := unmarshal(data, &event); err != nil {
			return nil, err
		}
		events = append(events, fruntime.CloneEvent(event))
	}
	return events, rows.Err()
}

func (store *Store) ListEventPage(runID, cursor string, limit int) (fruntime.EventPage, error) {
	events, err := store.ListEvents(runID)
	if err != nil {
		return fruntime.EventPage{}, err
	}
	return fruntime.PaginateEventsNewestFirst(events, cursor, limit)
}

func (store *Store) Commit(ctx context.Context, commit fruntime.Commit) (fruntime.CommitResult, error) {
	if strings.TrimSpace(commit.TransactionID) == "" {
		commit.TransactionID = uuid.NewString()
	}
	result := fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionNotStarted}
	commit = fruntime.SanitizeCommit(ctx, commit)
	if err := fruntime.ValidateCommit(commit); err != nil {
		return result, err
	}
	fingerprint, err := fruntime.CommitFingerprint(commit)
	if err != nil {
		return result, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if storedFingerprint, storedResult, found, err := loadTransaction(ctx, tx, commit.TransactionID); err != nil {
		return result, err
	} else if found {
		if storedFingerprint != fingerprint {
			return result, fmt.Errorf("runtime transaction %q fingerprint mismatch", commit.TransactionID)
		}
		return storedResult, nil
	}
	if err := applyCommit(ctx, tx, commit, &result); err != nil {
		return result, err
	}
	result.Artifacts = fruntime.CloneArtifactStages(commit.Artifacts)
	result.Outcome = fruntime.TransactionCommitted
	data, err := marshal(result)
	if err != nil {
		return fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionOutcomeUnknown}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_transactions(transaction_id, fingerprint, result, committed_at_ns) VALUES(?,?,?,?)`, commit.TransactionID, fingerprint, data, time.Now().UTC().UnixNano()); err != nil {
		return fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionOutcomeUnknown}, err
	}
	if err := tx.Commit(); err != nil {
		return fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionOutcomeUnknown}, err
	}
	return result, nil
}

func (store *Store) ResolveCommit(ctx context.Context, transactionID string) (fruntime.CommitResult, error) {
	result := fruntime.CommitResult{TransactionID: transactionID, Outcome: fruntime.TransactionNotStarted}
	if err := fruntime.ValidateStorageID("transaction ID", transactionID); err != nil {
		return result, err
	}
	_, stored, found, err := loadTransaction(ctx, store.db, transactionID)
	if err != nil || !found {
		return result, err
	}
	return stored, nil
}

func applyCommit(ctx context.Context, tx *sql.Tx, commit fruntime.Commit, result *fruntime.CommitResult) error {
	if commit.Lease != nil {
		run, err := loadRun(ctx, tx, commit.Lease.RunID)
		if err != nil {
			return err
		}
		if err := fruntime.ValidateExecutionLeaseGuard(run, *commit.Lease); err != nil {
			return err
		}
	}
	if commit.Run != nil {
		run := fruntime.CloneRunRecord(commit.Run.Run)
		existing, loadErr := loadRun(ctx, tx, run.RunID)
		switch commit.Run.Mode {
		case fruntime.RunWriteCreate:
			if loadErr == nil {
				return fmt.Errorf("run %q already exists", run.RunID)
			}
			if !errors.Is(loadErr, fruntime.ErrRunnerRecordNotFound) {
				return loadErr
			}
			if err := fruntime.ValidateNewRunDeletion(run); err != nil {
				return err
			}
			if err := ensureRunWritable(ctx, tx, run.RunID); err != nil {
				return err
			}
			if run.ParentRunID != "" {
				parent, err := loadRun(ctx, tx, run.ParentRunID)
				if err != nil {
					return fmt.Errorf("parent run %q: %w", run.ParentRunID, err)
				}
				if err := fruntime.ValidateNewRunParent(run, parent); err != nil {
					return err
				}
			}
			if err := insertRun(ctx, tx, run); err != nil {
				return err
			}
			result.Run = &run
		case fruntime.RunWriteUpdate, fruntime.RunWriteCheck:
			if loadErr != nil {
				return loadErr
			}
			if existing.Revision != run.Revision {
				return &fruntime.RunRevisionConflictError{RunID: run.RunID, Expected: run.Revision, Actual: existing.Revision}
			}
			if err := fruntime.ValidateCommitExecutionLease(existing, commit); err != nil {
				return err
			}
			if !fruntime.ExecutionLeasesEqual(existing.ExecutionLease, run.ExecutionLease) {
				return fmt.Errorf("%w: runtime commit cannot change run %q execution lease", fruntime.ErrRunControlNotAllowed, run.RunID)
			}
			if err := ensureRunWritable(ctx, tx, run.RunID); err != nil {
				return err
			}
			if err := fruntime.EnsureRunNotDeleting(existing, "write runtime records"); err != nil {
				return err
			}
			if commit.Run.Mode == fruntime.RunWriteUpdate {
				if err := fruntime.ValidateRunDeletionTransition(ctx, existing, run); err != nil {
					return err
				}
				run.Revision++
				changed, err := updateRun(ctx, tx, existing.Revision, run)
				if err != nil {
					return err
				}
				if changed == 0 {
					return &fruntime.RunRevisionConflictError{RunID: run.RunID, Expected: existing.Revision, Actual: existing.Revision + 1}
				}
				result.Run = &run
			}
		default:
			return fmt.Errorf("invalid run write mode %q", commit.Run.Mode)
		}
	}
	for _, write := range commit.Steps {
		step := fruntime.CloneStepRecord(write.Step)
		run, err := loadRun(ctx, tx, step.RunID)
		if err != nil {
			return err
		}
		if err := validateCommitRunWrite(ctx, tx, run, commit, "write a step"); err != nil {
			return err
		}
		existing, loadErr := loadStep(ctx, tx, step.StepID)
		switch write.Mode {
		case fruntime.StepWriteAppend:
			if loadErr == nil {
				return fmt.Errorf("step %q already exists", step.StepID)
			}
			if !errors.Is(loadErr, fruntime.ErrRunnerRecordNotFound) {
				return loadErr
			}
		case fruntime.StepWriteUpdate:
			if loadErr != nil || existing.RunID != step.RunID {
				return fruntime.ErrRunnerRecordNotFound
			}
			if err := fruntime.ValidateStepEffectTransition(existing, step); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid step write mode %q", write.Mode)
		}
		if err := putStep(ctx, tx, step); err != nil {
			return err
		}
	}
	for _, write := range commit.Checkpoints {
		run, err := loadRun(ctx, tx, write.Record.RunID)
		if err != nil {
			return err
		}
		if err := validateCommitRunWrite(ctx, tx, run, commit, "write a checkpoint"); err != nil {
			return err
		}
		record := write.Record
		record.PayloadRef = ""
		data, err := marshal(record)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoints(checkpoint_id, run_id, created_at_ns, metadata, payload) VALUES(?,?,?,?,?)`, record.CheckpointID, record.RunID, unixNano(record.CreatedAt), data, write.Payload); err != nil {
			return fmt.Errorf("save checkpoint %q: %w", record.CheckpointID, err)
		}
	}
	for _, event := range commit.Events {
		if fruntime.IsStreamingEvent(event.Type) {
			continue
		}
		run, err := loadRun(ctx, tx, event.RunID)
		if err != nil {
			return err
		}
		if err := validateCommitRunWrite(ctx, tx, run, commit, "publish an event"); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	for _, stage := range commit.Artifacts {
		run, err := loadRun(ctx, tx, stage.Ref.RunID)
		if err != nil {
			return err
		}
		if err := validateCommitRunWrite(ctx, tx, run, commit, "finalize an artifact"); err != nil {
			return err
		}
		if err := finalizeArtifactStage(ctx, tx, commit.TransactionID, stage); err != nil {
			return err
		}
	}
	return nil
}

func validateCommitRunWrite(ctx context.Context, tx *sql.Tx, run fruntime.RunRecord, commit fruntime.Commit, action string) error {
	if err := fruntime.ValidateCommitExecutionLease(run, commit); err != nil {
		return err
	}
	if err := ensureRunWritable(ctx, tx, run.RunID); err != nil {
		return err
	}
	return fruntime.EnsureRunNotDeleting(run, action)
}

func loadRun(ctx context.Context, query rowQuerier, runID string) (fruntime.RunRecord, error) {
	var data []byte
	if err := query.QueryRowContext(ctx, `SELECT data FROM runs WHERE run_id = ?`, runID).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return fruntime.RunRecord{}, fruntime.ErrRunnerRecordNotFound
	} else if err != nil {
		return fruntime.RunRecord{}, err
	}
	var run fruntime.RunRecord
	if err := unmarshal(data, &run); err != nil {
		return fruntime.RunRecord{}, err
	}
	return fruntime.CloneRunRecord(run), nil
}

func insertRun(ctx context.Context, tx *sql.Tx, run fruntime.RunRecord) error {
	data, err := marshal(run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(run_id,revision,status,parent_run_id,parent_task_id,root_run_id,namespace,started_at_ns,updated_at_ns,data) VALUES(?,?,?,?,?,?,?,?,?,?)`, run.RunID, run.Revision, run.Status, run.ParentRunID, run.ParentTaskID, run.RootRunID, run.Namespace, unixNano(run.StartedAt), unixNano(run.UpdatedAt), data)
	return err
}

func updateRun(ctx context.Context, tx *sql.Tx, expectedRevision uint64, run fruntime.RunRecord) (int64, error) {
	data, err := marshal(run)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runs SET revision=?,status=?,parent_run_id=?,parent_task_id=?,root_run_id=?,namespace=?,started_at_ns=?,updated_at_ns=?,data=? WHERE run_id=? AND revision=?`, run.Revision, run.Status, run.ParentRunID, run.ParentTaskID, run.RootRunID, run.Namespace, unixNano(run.StartedAt), unixNano(run.UpdatedAt), data, run.RunID, expectedRevision)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func loadStep(ctx context.Context, query rowQuerier, stepID string) (fruntime.StepRecord, error) {
	var data []byte
	if err := query.QueryRowContext(ctx, `SELECT data FROM steps WHERE step_id=?`, stepID).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return fruntime.StepRecord{}, fruntime.ErrRunnerRecordNotFound
	} else if err != nil {
		return fruntime.StepRecord{}, err
	}
	var step fruntime.StepRecord
	if err := unmarshal(data, &step); err != nil {
		return fruntime.StepRecord{}, err
	}
	return fruntime.CloneStepRecord(step), nil
}

func putStep(ctx context.Context, tx *sql.Tx, step fruntime.StepRecord) error {
	data, err := marshal(step)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO steps(step_id,run_id,task_id,started_at_ns,updated_at_ns,data) VALUES(?,?,?,?,?,?) ON CONFLICT(step_id) DO UPDATE SET run_id=excluded.run_id,task_id=excluded.task_id,started_at_ns=excluded.started_at_ns,updated_at_ns=excluded.updated_at_ns,data=excluded.data`, step.StepID, step.RunID, step.TaskID, unixNano(step.StartedAt), unixNano(step.UpdatedAt), data)
	return err
}

func insertEvent(ctx context.Context, tx *sql.Tx, event fruntime.Event) error {
	if err := fruntime.ValidateStorageID("run ID", event.RunID); err != nil {
		return err
	}
	if _, err := loadRun(ctx, tx, event.RunID); err != nil {
		return err
	}
	if err := ensureRunWritable(ctx, tx, event.RunID); err != nil {
		return err
	}
	data, err := marshal(fruntime.CloneEvent(event))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(event_id,run_id,timestamp_ns,data) VALUES(?,?,?,?)`, event.ID, event.RunID, unixNano(event.Timestamp), data)
	return err
}

func ensureRunWritable(ctx context.Context, query rowQuerier, runID string) error {
	var deletionID string
	err := query.QueryRowContext(ctx, `SELECT deletion_id FROM run_deletion_fences WHERE run_id=?`, runID).Scan(&deletionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: run %q is fenced by deletion %q", fruntime.ErrRunControlNotAllowed, runID, deletionID)
}

func loadTransaction(ctx context.Context, query rowQuerier, transactionID string) (string, fruntime.CommitResult, bool, error) {
	var fingerprint string
	var data []byte
	err := query.QueryRowContext(ctx, `SELECT fingerprint,result FROM runtime_transactions WHERE transaction_id=?`, transactionID).Scan(&fingerprint, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fruntime.CommitResult{}, false, nil
	}
	if err != nil {
		return "", fruntime.CommitResult{}, false, err
	}
	var result fruntime.CommitResult
	if err := unmarshal(data, &result); err != nil {
		return "", fruntime.CommitResult{}, false, err
	}
	return fingerprint, result, true, nil
}
