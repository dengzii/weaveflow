package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/google/uuid"
)

func (store *Store) Enqueue(ctx context.Context, task fruntime.Task) (fruntime.Task, error) {
	queued, _, err := store.enqueueWithCommit(ctx, task, nil)
	return queued, err
}

func (store *Store) EnqueueWithCommit(ctx context.Context, task fruntime.Task, commit fruntime.Commit) (fruntime.Task, fruntime.CommitResult, error) {
	return store.enqueueWithCommit(ctx, task, &commit)
}

func (store *Store) enqueueWithCommit(ctx context.Context, task fruntime.Task, commit *fruntime.Commit) (fruntime.Task, fruntime.CommitResult, error) {
	if strings.TrimSpace(task.ID) == "" {
		task.ID = uuid.NewString()
	}
	if err := validateTask(task); err != nil {
		return fruntime.Task{}, fruntime.CommitResult{}, err
	}
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = task.CreatedAt
	}
	if task.Status == "" {
		task.Status = fruntime.TaskStatusQueued
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fruntime.Task{}, fruntime.CommitResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	commitResult := fruntime.CommitResult{}
	if commit != nil {
		if strings.TrimSpace(commit.TransactionID) == "" {
			commit.TransactionID = uuid.NewString()
		}
		commitResult = fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionNotStarted}
		*commit = fruntime.SanitizeCommit(ctx, *commit)
		if err := fruntime.ValidateCommit(*commit); err != nil {
			return fruntime.Task{}, commitResult, err
		}
		fingerprint, err := taskAtomicFingerprint("enqueue", *commit, task, fruntime.TaskResult{}, nil, nil)
		if err != nil {
			return fruntime.Task{}, commitResult, err
		}
		storedFingerprint, storedResult, found, err := loadTaskTransaction(ctx, tx, commit.TransactionID)
		if err != nil {
			return fruntime.Task{}, commitResult, err
		}
		if found {
			if storedFingerprint != fingerprint {
				return fruntime.Task{}, commitResult, fmt.Errorf("runtime transaction %q fingerprint mismatch", commit.TransactionID)
			}
			if len(storedResult.Tasks) != 1 || !tasksEqual(storedResult.Tasks[0], task) {
				return fruntime.Task{}, commitResult, fmt.Errorf("runtime transaction %q task mismatch: %w", commit.TransactionID, fruntime.ErrTaskConflict)
			}
			return cloneTask(storedResult.Tasks[0]), storedResult.CommitResult, nil
		} else {
			if err := applyCommit(ctx, tx, *commit, &commitResult); err != nil {
				return fruntime.Task{}, commitResult, err
			}
			commitResult.Artifacts = fruntime.CloneArtifactStages(commit.Artifacts)
			commitResult.Outcome = fruntime.TransactionCommitted
			if err := insertTaskTransaction(ctx, tx, fingerprint, taskTransactionResult{CommitResult: commitResult, Tasks: []fruntime.Task{cloneTask(task)}}, now); err != nil {
				return fruntime.Task{}, commitResult, err
			}
		}
	}
	if task.RunID != "" {
		if err := ensureRunWritable(ctx, tx, task.RunID); err != nil {
			return fruntime.Task{}, commitResult, err
		}
	}
	data, err := marshal(task)
	if err != nil {
		return fruntime.Task{}, commitResult, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tasks(task_id,kind,run_id,status,version,available_at_ns,lease_expires_at_ns,created_at_ns,updated_at_ns,data) VALUES(?,?,?,?,?,?,?,?,?,?)`, task.ID, task.Kind, task.RunID, task.Status, task.Version, unixNano(task.AvailableAt), taskLeaseExpiry(task), unixNano(task.CreatedAt), unixNano(task.UpdatedAt), data)
	if err == nil {
		if err := tx.Commit(); err != nil {
			if commit != nil {
				commitResult.Outcome = fruntime.TransactionOutcomeUnknown
			}
			return fruntime.Task{}, commitResult, err
		}
		return cloneTask(task), commitResult, nil
	}
	existing, loadErr := loadTask(ctx, tx, task.ID)
	if loadErr == nil && tasksEqual(existing, task) {
		return existing, commitResult, nil
	}
	return fruntime.Task{}, commitResult, fmt.Errorf("enqueue task %q: %w", task.ID, errors.Join(fruntime.ErrTaskConflict, err))
}

func (store *Store) Claim(ctx context.Context, worker fruntime.WorkerIdentity, options fruntime.TaskClaimOptions) (fruntime.Task, fruntime.Attempt, error) {
	if err := fruntime.ValidateStorageID("worker ID", worker.ID); err != nil {
		return fruntime.Task{}, fruntime.Attempt{}, err
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if options.TTL <= 0 {
		return fruntime.Task{}, fruntime.Attempt{}, errors.New("task lease TTL must be greater than zero")
	}
	for conflictCount := 0; conflictCount < 8; conflictCount++ {
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		task, err := selectClaimableTask(ctx, tx, options.TaskID, options.Kinds, now)
		if err != nil {
			_ = tx.Rollback()
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		previousEpoch := uint64(0)
		if task.Lease != nil {
			previousEpoch = task.Lease.Epoch
			if err := abandonRunningAttempt(ctx, tx, task, now); err != nil {
				_ = tx.Rollback()
				return fruntime.Task{}, fruntime.Attempt{}, err
			}
		}
		lease := fruntime.TaskLease{
			TaskID: task.ID, WorkerID: worker.ID, Token: uuid.NewString(), Epoch: previousEpoch + 1,
			AcquiredAt: now, HeartbeatAt: now, ExpiresAt: now.Add(options.TTL),
		}
		task.Status = fruntime.TaskStatusRunning
		task.Lease = &lease
		task.AttemptCount++
		task.Version++
		task.UpdatedAt = now
		changed, err := updateTaskVersion(ctx, tx, task.Version-1, task)
		if err != nil {
			_ = tx.Rollback()
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		if changed == 0 {
			_ = tx.Rollback()
			continue
		}
		attempt := fruntime.Attempt{
			ID: uuid.NewString(), TaskID: task.ID, Number: task.AttemptCount, WorkerID: worker.ID,
			LeaseToken: lease.Token, LeaseEpoch: lease.Epoch, Status: fruntime.AttemptStatusRunning,
			StartedAt: now, HeartbeatAt: now,
		}
		if err := insertAttempt(ctx, tx, attempt); err != nil {
			_ = tx.Rollback()
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		workerData, err := marshal(worker)
		if err != nil {
			_ = tx.Rollback()
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workers(worker_id,heartbeat_at_ns,data) VALUES(?,?,?) ON CONFLICT(worker_id) DO UPDATE SET heartbeat_at_ns=excluded.heartbeat_at_ns,data=excluded.data`, worker.ID, unixNano(now), workerData); err != nil {
			_ = tx.Rollback()
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		if err := tx.Commit(); err != nil {
			return fruntime.Task{}, fruntime.Attempt{}, err
		}
		return cloneTask(task), attempt, nil
	}
	return fruntime.Task{}, fruntime.Attempt{}, fruntime.ErrTaskConflict
}

func (store *Store) Heartbeat(ctx context.Context, lease fruntime.TaskLease, now time.Time, ttl time.Duration) (fruntime.TaskLease, error) {
	if ttl <= 0 {
		return fruntime.TaskLease{}, errors.New("task lease TTL must be greater than zero")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fruntime.TaskLease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := loadTask(ctx, tx, lease.TaskID)
	if err != nil {
		return fruntime.TaskLease{}, err
	}
	if task.RunID != "" {
		if err := ensureRunWritable(ctx, tx, task.RunID); err != nil {
			return fruntime.TaskLease{}, err
		}
	}
	if err := validateTaskLease(task, lease, now, false); err != nil {
		return fruntime.TaskLease{}, err
	}
	updatedLease := *task.Lease
	updatedLease.HeartbeatAt = now
	updatedLease.ExpiresAt = now.Add(ttl)
	task.Lease = &updatedLease
	task.Version++
	task.UpdatedAt = now
	changed, err := updateTaskVersion(ctx, tx, task.Version-1, task)
	if err != nil || changed == 0 {
		if err == nil {
			err = fruntime.ErrTaskLeaseLost
		}
		return fruntime.TaskLease{}, err
	}
	if err := updateAttempt(ctx, tx, task, fruntime.AttemptStatusRunning, "", nil); err != nil {
		return fruntime.TaskLease{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workers SET heartbeat_at_ns=? WHERE worker_id=?`, unixNano(now), lease.WorkerID); err != nil {
		return fruntime.TaskLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return fruntime.TaskLease{}, err
	}
	return updatedLease, nil
}

func (store *Store) Complete(ctx context.Context, lease fruntime.TaskLease, result fruntime.TaskResult) (fruntime.Task, error) {
	task, _, err := store.completeWithCommit(ctx, lease, result, nil)
	return task, err
}

func (store *Store) CompleteWithCommit(ctx context.Context, lease fruntime.TaskLease, result fruntime.TaskResult, commit fruntime.Commit) (fruntime.Task, fruntime.CommitResult, error) {
	return store.completeWithCommit(ctx, lease, result, &commit)
}

func (store *Store) completeWithCommit(ctx context.Context, lease fruntime.TaskLease, taskResult fruntime.TaskResult, commit *fruntime.Commit) (fruntime.Task, fruntime.CommitResult, error) {
	commitResult := fruntime.CommitResult{}
	fingerprint := ""
	if commit != nil {
		if strings.TrimSpace(commit.TransactionID) == "" {
			commit.TransactionID = uuid.NewString()
		}
		commitResult = fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionNotStarted}
		*commit = fruntime.SanitizeCommit(ctx, *commit)
		if err := fruntime.ValidateCommit(*commit); err != nil {
			return fruntime.Task{}, commitResult, err
		}
		var err error
		fingerprint, err = taskAtomicFingerprint("complete", *commit, fruntime.Task{}, taskResult, &lease, nil)
		if err != nil {
			return fruntime.Task{}, commitResult, err
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fruntime.Task{}, commitResult, err
	}
	defer func() { _ = tx.Rollback() }()
	if commit != nil {
		storedFingerprint, storedResult, found, err := loadTaskTransaction(ctx, tx, commit.TransactionID)
		if err != nil {
			return fruntime.Task{}, commitResult, err
		}
		if found {
			if storedFingerprint != fingerprint {
				return fruntime.Task{}, commitResult, fmt.Errorf("runtime transaction %q fingerprint mismatch", commit.TransactionID)
			}
			if len(storedResult.Tasks) != 1 || storedResult.Tasks[0].ID != lease.TaskID {
				return fruntime.Task{}, commitResult, fmt.Errorf("runtime transaction %q completed task mismatch: %w", commit.TransactionID, fruntime.ErrTaskConflict)
			}
			return cloneTask(storedResult.Tasks[0]), storedResult.CommitResult, nil
		}
	}
	task, err := loadTask(ctx, tx, lease.TaskID)
	if err != nil {
		return fruntime.Task{}, commitResult, err
	}
	now := time.Now().UTC()
	if task.RunID != "" {
		if err := ensureRunWritable(ctx, tx, task.RunID); err != nil {
			return fruntime.Task{}, commitResult, err
		}
	}
	if err := validateTaskLease(task, lease, now, false); err != nil {
		return fruntime.Task{}, commitResult, err
	}
	if task.Kind == fruntime.TaskKindGraphRun {
		run, err := loadRun(ctx, tx, task.RunID)
		if err != nil {
			return fruntime.Task{}, commitResult, err
		}
		if !isTerminalTaskRunStatus(run.Status) {
			return fruntime.Task{}, commitResult, fmt.Errorf("%w: graph run task %q cannot complete while run %q status is %q", fruntime.ErrTaskConflict, task.ID, task.RunID, run.Status)
		}
	}
	if commit != nil {
		if err := applyCommit(ctx, tx, *commit, &commitResult); err != nil {
			return fruntime.Task{}, commitResult, err
		}
		commitResult.Artifacts = fruntime.CloneArtifactStages(commit.Artifacts)
		commitResult.Outcome = fruntime.TransactionCommitted
	}
	task.Status = fruntime.TaskStatusCompleted
	task.Payload = append(task.Payload[:0], taskResult.Payload...)
	task.Lease = nil
	task.Version++
	task.UpdatedAt = now
	task.CompletedAt = &now
	changed, err := updateTaskVersion(ctx, tx, task.Version-1, task)
	if err != nil || changed == 0 {
		if err == nil {
			err = fruntime.ErrTaskLeaseLost
		}
		return fruntime.Task{}, commitResult, err
	}
	if err := updateAttempt(ctx, tx, task, fruntime.AttemptStatusCompleted, "", &now); err != nil {
		return fruntime.Task{}, commitResult, err
	}
	if commit != nil {
		if err := insertTaskTransaction(ctx, tx, fingerprint, taskTransactionResult{CommitResult: commitResult, Tasks: []fruntime.Task{cloneTask(task)}}, now); err != nil {
			return fruntime.Task{}, commitResult, err
		}
	}
	if err := tx.Commit(); err != nil {
		if commit != nil {
			commitResult.Outcome = fruntime.TransactionOutcomeUnknown
		}
		return fruntime.Task{}, commitResult, err
	}
	return cloneTask(task), commitResult, nil
}

func (store *Store) Fail(ctx context.Context, lease fruntime.TaskLease, failure fruntime.TaskFailure) (fruntime.Task, error) {
	tasks, _, err := store.failWithCommit(ctx, []fruntime.TaskFailureTransition{{Lease: lease, Failure: failure}}, nil)
	if err != nil {
		return fruntime.Task{}, err
	}
	return tasks[0], nil
}

func (store *Store) FailWithCommit(ctx context.Context, failures []fruntime.TaskFailureTransition, commit fruntime.Commit) ([]fruntime.Task, fruntime.CommitResult, error) {
	return store.failWithCommit(ctx, failures, &commit)
}

func (store *Store) failWithCommit(ctx context.Context, failures []fruntime.TaskFailureTransition, commit *fruntime.Commit) ([]fruntime.Task, fruntime.CommitResult, error) {
	if len(failures) == 0 {
		return nil, fruntime.CommitResult{}, errors.New("task failures are required")
	}
	commitResult := fruntime.CommitResult{}
	fingerprint := ""
	if commit != nil {
		if strings.TrimSpace(commit.TransactionID) == "" {
			commit.TransactionID = uuid.NewString()
		}
		commitResult = fruntime.CommitResult{TransactionID: commit.TransactionID, Outcome: fruntime.TransactionNotStarted}
		*commit = fruntime.SanitizeCommit(ctx, *commit)
		if err := fruntime.ValidateCommit(*commit); err != nil {
			return nil, commitResult, err
		}
		var err error
		fingerprint, err = taskAtomicFingerprint("fail", *commit, fruntime.Task{}, fruntime.TaskResult{}, nil, failures)
		if err != nil {
			return nil, commitResult, err
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, commitResult, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	if commit != nil {
		storedFingerprint, storedResult, found, err := loadTaskTransaction(ctx, tx, commit.TransactionID)
		if err != nil {
			return nil, commitResult, err
		}
		if found {
			if storedFingerprint != fingerprint {
				return nil, commitResult, fmt.Errorf("runtime transaction %q fingerprint mismatch", commit.TransactionID)
			}
			if len(storedResult.Tasks) != len(failures) {
				return nil, commitResult, fmt.Errorf("runtime transaction %q failed task count mismatch: %w", commit.TransactionID, fruntime.ErrTaskConflict)
			}
			return cloneTasks(storedResult.Tasks), storedResult.CommitResult, nil
		}
	}
	tasks := make([]fruntime.Task, 0, len(failures))
	seen := make(map[string]struct{}, len(failures))
	for _, transition := range failures {
		lease := transition.Lease
		failure := transition.Failure
		if _, exists := seen[lease.TaskID]; exists {
			return nil, fruntime.CommitResult{}, fmt.Errorf("duplicate task failure %q", lease.TaskID)
		}
		seen[lease.TaskID] = struct{}{}
		task, loadErr := loadTask(ctx, tx, lease.TaskID)
		if loadErr != nil {
			return nil, fruntime.CommitResult{}, loadErr
		}
		if task.RunID != "" {
			if fenceErr := ensureRunWritable(ctx, tx, task.RunID); fenceErr != nil {
				return nil, commitResult, fenceErr
			}
		}
		if leaseErr := validateTaskLease(task, lease, now, false); leaseErr != nil {
			return nil, fruntime.CommitResult{}, leaseErr
		}
		status := fruntime.TaskStatusFailed
		if failure.Retryable && (task.MaxAttempts <= 0 || task.AttemptCount < task.MaxAttempts) {
			status = fruntime.TaskStatusQueued
			if failure.RetryAt.IsZero() {
				failure.RetryAt = now
			}
			task.AvailableAt = failure.RetryAt.UTC()
		} else if task.MaxAttempts > 0 && task.AttemptCount >= task.MaxAttempts {
			status = fruntime.TaskStatusDead
		}
		task.Status = status
		task.LastError = strings.TrimSpace(failure.Message)
		task.Lease = nil
		task.Version++
		task.UpdatedAt = now
		if status != fruntime.TaskStatusQueued {
			task.CompletedAt = &now
		}
		changed, updateErr := updateTaskVersion(ctx, tx, task.Version-1, task)
		if updateErr != nil || changed == 0 {
			if updateErr == nil {
				updateErr = fruntime.ErrTaskLeaseLost
			}
			return nil, fruntime.CommitResult{}, updateErr
		}
		if attemptErr := updateAttempt(ctx, tx, task, fruntime.AttemptStatusFailed, task.LastError, &now); attemptErr != nil {
			return nil, fruntime.CommitResult{}, attemptErr
		}
		tasks = append(tasks, cloneTask(task))
	}
	if commit != nil {
		if applyErr := applyCommit(ctx, tx, *commit, &commitResult); applyErr != nil {
			return nil, commitResult, applyErr
		}
		commitResult.Artifacts = fruntime.CloneArtifactStages(commit.Artifacts)
		commitResult.Outcome = fruntime.TransactionCommitted
		if insertErr := insertTaskTransaction(ctx, tx, fingerprint, taskTransactionResult{CommitResult: commitResult, Tasks: cloneTasks(tasks)}, now); insertErr != nil {
			return nil, commitResult, insertErr
		}
	}
	if err := tx.Commit(); err != nil {
		if commit != nil {
			commitResult.Outcome = fruntime.TransactionOutcomeUnknown
		}
		return nil, commitResult, err
	}
	return tasks, commitResult, nil
}

func (store *Store) Cancel(ctx context.Context, taskID string, expectedVersion uint64, now time.Time) (fruntime.Task, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fruntime.Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return fruntime.Task{}, err
	}
	if task.RunID != "" {
		if err := ensureRunWritable(ctx, tx, task.RunID); err != nil {
			return fruntime.Task{}, err
		}
	}
	if task.Version != expectedVersion {
		return fruntime.Task{}, fruntime.ErrTaskConflict
	}
	if task.Status == fruntime.TaskStatusCompleted || task.Status == fruntime.TaskStatusCanceled {
		return cloneTask(task), nil
	}
	task.Status = fruntime.TaskStatusCanceled
	task.Lease = nil
	task.Version++
	task.UpdatedAt = now.UTC()
	task.CompletedAt = &task.UpdatedAt
	changed, err := updateTaskVersion(ctx, tx, expectedVersion, task)
	if err != nil || changed == 0 {
		if err == nil {
			err = fruntime.ErrTaskConflict
		}
		return fruntime.Task{}, err
	}
	if task.AttemptCount > 0 {
		if err := updateAttempt(ctx, tx, task, fruntime.AttemptStatusCanceled, "task canceled", task.CompletedAt); err != nil {
			return fruntime.Task{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return fruntime.Task{}, err
	}
	return cloneTask(task), nil
}

func (store *Store) GetTask(ctx context.Context, taskID string) (fruntime.Task, error) {
	if err := fruntime.ValidateStorageID("task ID", taskID); err != nil {
		return fruntime.Task{}, err
	}
	return loadTask(ctx, store.db, taskID)
}

func (store *Store) ListTasks(ctx context.Context, filter fruntime.TaskFilter) ([]fruntime.Task, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT data FROM tasks ORDER BY created_at_ns,task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	statuses := make(map[fruntime.TaskStatus]struct{}, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses[status] = struct{}{}
	}
	kinds := make(map[string]struct{}, len(filter.Kinds))
	for _, kind := range filter.Kinds {
		kinds[kind] = struct{}{}
	}
	var tasks []fruntime.Task
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var task fruntime.Task
		if err := unmarshal(data, &task); err != nil {
			return nil, err
		}
		if len(statuses) > 0 {
			if _, ok := statuses[task.Status]; !ok {
				continue
			}
		}
		if len(kinds) > 0 {
			if _, ok := kinds[task.Kind]; !ok {
				continue
			}
		}
		if filter.RunID != "" && task.RunID != filter.RunID {
			continue
		}
		tasks = append(tasks, cloneTask(task))
		if filter.Limit > 0 && len(tasks) >= filter.Limit {
			break
		}
	}
	return tasks, rows.Err()
}

func (store *Store) ListAttempts(ctx context.Context, taskID string) ([]fruntime.Attempt, error) {
	if err := fruntime.ValidateStorageID("task ID", taskID); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT data FROM attempts WHERE task_id=? ORDER BY number`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []fruntime.Attempt
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var attempt fruntime.Attempt
		if err := unmarshal(data, &attempt); err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

func selectClaimableTask(ctx context.Context, tx *sql.Tx, taskID string, kinds []string, now time.Time) (fruntime.Task, error) {
	query := `SELECT data FROM tasks WHERE ((status=? AND available_at_ns<=?) OR (status=? AND lease_expires_at_ns>0 AND lease_expires_at_ns<=?)) AND NOT EXISTS (SELECT 1 FROM run_deletion_fences WHERE run_deletion_fences.run_id=tasks.run_id)`
	arguments := []any{fruntime.TaskStatusQueued, unixNano(now), fruntime.TaskStatusRunning, unixNano(now)}
	if strings.TrimSpace(taskID) != "" {
		query += ` AND task_id=?`
		arguments = append(arguments, strings.TrimSpace(taskID))
	}
	if len(kinds) > 0 {
		query += ` AND kind IN (` + strings.TrimRight(strings.Repeat("?,", len(kinds)), ",") + `)`
		for _, kind := range kinds {
			arguments = append(arguments, strings.TrimSpace(kind))
		}
	}
	query += ` AND (json_extract(data,'$.max_attempts') IS NULL OR json_extract(data,'$.max_attempts')<=0 OR json_extract(data,'$.attempt_count')<json_extract(data,'$.max_attempts')) ORDER BY available_at_ns,created_at_ns,task_id LIMIT 1`
	var data []byte
	if err := tx.QueryRowContext(ctx, query, arguments...).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return fruntime.Task{}, fruntime.ErrTaskNotFound
	} else if err != nil {
		return fruntime.Task{}, err
	}
	var task fruntime.Task
	if err := unmarshal(data, &task); err != nil {
		return fruntime.Task{}, err
	}
	return task, nil
}

func loadTask(ctx context.Context, query rowQuerier, taskID string) (fruntime.Task, error) {
	var data []byte
	if err := query.QueryRowContext(ctx, `SELECT data FROM tasks WHERE task_id=?`, taskID).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return fruntime.Task{}, fruntime.ErrTaskNotFound
	} else if err != nil {
		return fruntime.Task{}, err
	}
	var task fruntime.Task
	if err := unmarshal(data, &task); err != nil {
		return fruntime.Task{}, err
	}
	return task, nil
}

func updateTaskVersion(ctx context.Context, tx *sql.Tx, expectedVersion uint64, task fruntime.Task) (int64, error) {
	data, err := marshal(task)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status=?,version=?,available_at_ns=?,lease_expires_at_ns=?,updated_at_ns=?,data=? WHERE task_id=? AND version=?`, task.Status, task.Version, unixNano(task.AvailableAt), taskLeaseExpiry(task), unixNano(task.UpdatedAt), data, task.ID, expectedVersion)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func insertAttempt(ctx context.Context, tx *sql.Tx, attempt fruntime.Attempt) error {
	data, err := marshal(attempt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attempts(attempt_id,task_id,number,status,started_at_ns,data) VALUES(?,?,?,?,?,?)`, attempt.ID, attempt.TaskID, attempt.Number, attempt.Status, unixNano(attempt.StartedAt), data)
	return err
}

func updateAttempt(ctx context.Context, tx *sql.Tx, task fruntime.Task, status fruntime.AttemptStatus, message string, finishedAt *time.Time) error {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM attempts WHERE task_id=? AND number=?`, task.ID, task.AttemptCount).Scan(&data); err != nil {
		return err
	}
	var attempt fruntime.Attempt
	if err := unmarshal(data, &attempt); err != nil {
		return err
	}
	attempt.Status = status
	attempt.Error = message
	if task.Lease != nil {
		attempt.HeartbeatAt = task.Lease.HeartbeatAt
	}
	attempt.FinishedAt = finishedAt
	data, err := marshal(attempt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE attempts SET status=?,data=? WHERE attempt_id=?`, attempt.Status, data, attempt.ID)
	return err
}

func abandonRunningAttempt(ctx context.Context, tx *sql.Tx, task fruntime.Task, now time.Time) error {
	if task.AttemptCount <= 0 {
		return nil
	}
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM attempts WHERE task_id=? AND number=?`, task.ID, task.AttemptCount).Scan(&data); err != nil {
		return err
	}
	var attempt fruntime.Attempt
	if err := unmarshal(data, &attempt); err != nil {
		return err
	}
	attempt.Status = fruntime.AttemptStatusAbandoned
	attempt.FinishedAt = &now
	attempt.Error = "task lease expired"
	data, err := marshal(attempt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE attempts SET status=?,data=? WHERE attempt_id=?`, attempt.Status, data, attempt.ID)
	return err
}

func validateTask(task fruntime.Task) error {
	if err := fruntime.ValidateStorageID("task ID", task.ID); err != nil {
		return err
	}
	if strings.TrimSpace(task.Kind) == "" {
		return errors.New("task kind is required")
	}
	if task.RunID != "" {
		if err := fruntime.ValidateStorageID("run ID", task.RunID); err != nil {
			return err
		}
	}
	return nil
}

func validateTaskLease(task fruntime.Task, lease fruntime.TaskLease, now time.Time, allowExpired bool) error {
	if task.Status != fruntime.TaskStatusRunning || task.Lease == nil {
		return fruntime.ErrTaskLeaseLost
	}
	current := task.Lease
	if current.TaskID != lease.TaskID || current.WorkerID != lease.WorkerID || current.Token != lease.Token || current.Epoch != lease.Epoch {
		return fruntime.ErrTaskLeaseLost
	}
	if !allowExpired && !current.ExpiresAt.After(now) {
		return fruntime.ErrTaskLeaseLost
	}
	return nil
}

func taskLeaseExpiry(task fruntime.Task) int64 {
	if task.Lease == nil {
		return 0
	}
	return unixNano(task.Lease.ExpiresAt)
}

func cloneTask(task fruntime.Task) fruntime.Task {
	data, _ := marshal(task)
	var cloned fruntime.Task
	_ = unmarshal(data, &cloned)
	return cloned
}

func tasksEqual(left, right fruntime.Task) bool {
	leftData, leftErr := marshal(left)
	rightData, rightErr := marshal(right)
	return leftErr == nil && rightErr == nil && string(leftData) == string(rightData)
}

func isTerminalTaskRunStatus(status fruntime.RunStatus) bool {
	switch status {
	case fruntime.RunStatusCompleted, fruntime.RunStatusFailed, fruntime.RunStatusCanceled:
		return true
	default:
		return false
	}
}

type taskAtomicFingerprintInput struct {
	Operation string                           `json:"operation"`
	Commit    fruntime.Commit                  `json:"commit"`
	Task      fruntime.Task                    `json:"task,omitempty"`
	Lease     *fruntime.TaskLease              `json:"lease,omitempty"`
	Result    fruntime.TaskResult              `json:"result,omitempty"`
	Failures  []fruntime.TaskFailureTransition `json:"failures,omitempty"`
}

type taskTransactionResult struct {
	fruntime.CommitResult
	Tasks []fruntime.Task `json:"tasks"`
}

func insertTaskTransaction(ctx context.Context, tx *sql.Tx, fingerprint string, result taskTransactionResult, committedAt time.Time) error {
	data, err := marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_transactions(transaction_id,fingerprint,result,committed_at_ns) VALUES(?,?,?,?)`, result.TransactionID, fingerprint, data, unixNano(committedAt))
	return err
}

func loadTaskTransaction(ctx context.Context, query rowQuerier, transactionID string) (string, taskTransactionResult, bool, error) {
	fingerprint, data, found, err := loadRawTransaction(ctx, query, transactionID)
	if err != nil || !found {
		return fingerprint, taskTransactionResult{}, found, err
	}
	var result taskTransactionResult
	if err := unmarshal(data, &result); err != nil {
		return "", taskTransactionResult{}, false, err
	}
	return fingerprint, result, true, nil
}

func loadRawTransaction(ctx context.Context, query rowQuerier, transactionID string) (string, []byte, bool, error) {
	var fingerprint string
	var data []byte
	err := query.QueryRowContext(ctx, `SELECT fingerprint,result FROM runtime_transactions WHERE transaction_id=?`, transactionID).Scan(&fingerprint, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	return fingerprint, data, true, nil
}

func cloneTasks(tasks []fruntime.Task) []fruntime.Task {
	if len(tasks) == 0 {
		return nil
	}
	cloned := make([]fruntime.Task, len(tasks))
	for index, task := range tasks {
		cloned[index] = cloneTask(task)
	}
	return cloned
}

func taskAtomicFingerprint(operation string, commit fruntime.Commit, task fruntime.Task, result fruntime.TaskResult, lease *fruntime.TaskLease, failures []fruntime.TaskFailureTransition) (string, error) {
	commit.TransactionID = ""
	if len(failures) > 1 {
		failures = append([]fruntime.TaskFailureTransition(nil), failures...)
		sort.Slice(failures, func(left, right int) bool {
			if failures[left].Lease.TaskID != failures[right].Lease.TaskID {
				return failures[left].Lease.TaskID < failures[right].Lease.TaskID
			}
			return failures[left].Lease.Epoch < failures[right].Lease.Epoch
		})
	}
	data, err := json.Marshal(taskAtomicFingerprintInput{
		Operation: operation,
		Commit:    commit,
		Task:      task,
		Lease:     lease,
		Result:    result,
		Failures:  failures,
	})
	if err != nil {
		return "", fmt.Errorf("encode task transaction fingerprint: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
