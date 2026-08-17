package file

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type Store struct {
	baseDir     string
	execution   *executionStore
	checkpoints *checkpointStore
	events      *eventSink
	artifacts   *artifactStore
	journalDir  string
	writer      *writerLock
	writerState *writerState
	failure     transactionFailurePoint
	failureAt   int
}

type transactionFailurePoint string

const (
	failureAfterPreparedJournal  transactionFailurePoint = "after_prepared_journal"
	failureBeforeMutation        transactionFailurePoint = "before_mutation"
	failureAfterMutation         transactionFailurePoint = "after_mutation"
	failureAfterCommittedJournal transactionFailurePoint = "after_committed_journal"
	failureBeforeJournalRemoval  transactionFailurePoint = "before_journal_removal"
)

var errInjectedTransactionFailure = errors.New("injected file transaction failure")

func Open(baseDir string) (*Store, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("runtime store base directory is required")
	}
	absolute, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	writer, err := acquireWriterLock(absolute)
	if err != nil {
		return nil, err
	}
	shared := &sync.Mutex{}
	state := &writerState{}
	store := &Store{
		baseDir:     absolute,
		execution:   newExecutionStore(filepath.Join(absolute, "execution"), shared),
		checkpoints: newCheckpointStore(filepath.Join(absolute, "checkpoints"), shared),
		events:      newEventSink(filepath.Join(absolute, "events"), shared),
		artifacts:   newArtifactStore(filepath.Join(absolute, "artifacts"), shared),
		journalDir:  filepath.Join(absolute, ".transactions"),
		writer:      writer,
		writerState: state,
	}
	store.execution.writer = state
	store.checkpoints.writer = state
	store.events.writer = state
	store.artifacts.writer = state
	unlock := store.lockComponents()
	defer unlock()
	if err := store.recoverLocked(); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := store.execution.validateRunDeletionFencesLocked(); err != nil {
		_ = writer.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	unlock := store.lockComponents()
	defer unlock()
	if store.writerState.closed {
		return store.writer.Close()
	}
	store.writerState.closed = true
	return store.writer.Close()
}

func (store *Store) ExecutionStore() ExecutionStore {
	return store.execution
}

func (store *Store) CheckpointStore() CheckpointStore {
	return store.checkpoints
}

func (store *Store) EventSink() EventSink {
	return store.events
}

func (store *Store) ArtifactStore() ArtifactStore {
	return store.artifacts
}

func (store *Store) TransactionStore() TransactionStore {
	return store
}

func (store *Store) LoadRunDeletionManifest(ctx context.Context, deletionID string) (RunDeletionManifest, error) {
	return store.execution.LoadRunDeletionManifest(ctx, deletionID)
}

func (store *Store) ListRunDeletionManifests(ctx context.Context) ([]RunDeletionManifest, error) {
	return store.execution.ListRunDeletionManifests(ctx)
}

func (store *Store) SaveRunDeletionManifest(ctx context.Context, manifest RunDeletionManifest) error {
	return store.execution.SaveRunDeletionManifest(ctx, manifest)
}

func (store *Store) ValidateRunDeletionFences(ctx context.Context) error {
	return store.execution.ValidateRunDeletionFences(ctx)
}

func (store *Store) ExecutionDeletionStore() RunDeletionExecutionStore {
	return store.execution
}

func (store *Store) CheckpointDeleter() RunDeleter {
	return store.checkpoints
}

func (store *Store) EventDeleter() RunDeleter {
	return store.events
}

func (store *Store) ArtifactDeleter() RunDeleter {
	return store.artifacts
}

func (store *Store) CreateRun(ctx context.Context, run RunRecord) error {
	return store.execution.CreateRun(ctx, run)
}

func (store *Store) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	return store.execution.CompareAndSwapRun(ctx, expectedRevision, run)
}

func (store *Store) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	return store.execution.GetRun(ctx, runID)
}

func (store *Store) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	return store.execution.ListRuns(ctx, filter)
}

func (store *Store) AppendStep(ctx context.Context, step StepRecord) error {
	return store.execution.AppendStep(ctx, step)
}

func (store *Store) UpdateStep(ctx context.Context, step StepRecord) error {
	return store.execution.UpdateStep(ctx, step)
}

func (store *Store) GetStep(ctx context.Context, stepID string) (StepRecord, error) {
	return store.execution.GetStep(ctx, stepID)
}

func (store *Store) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	return store.execution.ListSteps(ctx, runID)
}

func (store *Store) Save(ctx context.Context, record CheckpointRecord, payload []byte) error {
	return store.checkpoints.Save(ctx, record, payload)
}

func (store *Store) Load(ctx context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
	return store.checkpoints.Load(ctx, checkpointID)
}

func (store *Store) List(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	return store.checkpoints.List(ctx, runID)
}

func (store *Store) Publish(ctx context.Context, event Event) error {
	return store.events.Publish(ctx, event)
}

func (store *Store) PublishBatch(ctx context.Context, events []Event) error {
	return store.events.PublishBatch(ctx, events)
}

func (store *Store) ListEvents(runID string) ([]Event, error) {
	return store.events.ListEvents(runID)
}

func (store *Store) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	return store.events.ListEventPage(runID, cursor, limit)
}

func (store *Store) DeleteRun(ctx context.Context, runID string) error {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return errors.New("file runtime store is nil")
	}
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	unlock := store.lockComponents()
	defer unlock()
	if err := requireWritable(store.writerState); err != nil {
		return err
	}
	if err := store.recoverLocked(); err != nil {
		return err
	}
	for _, baseDir := range []string{store.execution.baseDir, store.checkpoints.baseDir, store.events.baseDir} {
		if err := requireRunDeletionLocked(ctx, baseDir, runID); err != nil {
			return err
		}
	}
	run, err := store.execution.readRunLocked(runID)
	if err != nil && !errors.Is(err, ErrRunnerRecordNotFound) {
		return err
	}
	if err == nil {
		if run.Deletion == nil {
			return fmt.Errorf("%w: run %q is not reserved for deletion", ErrRunControlNotAllowed, runID)
		}
		if err := validateRunDeletionState(run.Deletion); err != nil {
			return fmt.Errorf("run %q deletion state: %w", runID, err)
		}
		if err := requireRunDeletionMutation(ctx, runID, run.Deletion.ID); err != nil {
			return err
		}
	}
	if err := removeRunnerDirectory(store.checkpoints.checkpointsDir(runID)); err != nil {
		return err
	}
	if err := removeRunnerFile(store.events.eventsPath(runID)); err != nil {
		return err
	}
	if err := removeRunnerDirectory(store.execution.stepsDir(runID)); err != nil {
		return err
	}
	if err := removeRunnerFile(store.execution.runPath(runID)); err != nil {
		return err
	}
	return nil
}

func (store *Store) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return errors.New("file runtime store is nil")
	}
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	unlock := store.lockComponents()
	defer unlock()
	if err := requireWritable(store.writerState); err != nil {
		return err
	}
	if err := store.recoverLocked(); err != nil {
		return err
	}
	for _, baseDir := range []string{store.execution.baseDir, store.checkpoints.baseDir, store.events.baseDir} {
		var fence runDeletionFence
		err := readRunnerJSONFile(runDeletionPath(baseDir, runID), &fence)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := validateRunDeletionFence(fence, runID); err != nil {
			return err
		}
		if fence.DeletionID != deletionID {
			return fmt.Errorf("%w: run %q is fenced by deletion %q, not %q", ErrRunControlNotAllowed, runID, fence.DeletionID, deletionID)
		}
	}
	for _, baseDir := range []string{store.execution.baseDir, store.checkpoints.baseDir, store.events.baseDir} {
		if err := fenceRunDeletionLocked(ctx, baseDir, runID, deletionID); err != nil {
			return err
		}
	}
	return nil
}

type transactionPhase string

const (
	transactionPrepared  transactionPhase = "prepared"
	transactionCommitted transactionPhase = "committed"
)

type transactionJournal struct {
	ID        string                `json:"id"`
	Phase     transactionPhase      `json:"phase"`
	Mutations []transactionMutation `json:"mutations"`
}

type transactionMutation struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	Before       []byte `json:"before,omitempty"`
	AfterExists  bool   `json:"after_exists"`
	After        []byte `json:"after,omitempty"`
}

func (store *Store) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	if store == nil {
		return CommitResult{}, errors.New("file runtime store is nil")
	}
	if err := storeContextErr(ctx); err != nil {
		return CommitResult{}, err
	}
	commit = sanitizeCommit(ctx, commit)
	if err := validateRuntimeCommit(commit); err != nil {
		return CommitResult{}, err
	}
	unlock := store.lockComponents()
	defer unlock()
	if err := requireWritable(store.writerState); err != nil {
		return CommitResult{}, err
	}
	if err := store.recoverLocked(); err != nil {
		return CommitResult{}, err
	}
	journal, result, err := store.prepareJournalLocked(ctx, commit)
	if err != nil {
		return CommitResult{}, err
	}
	if len(journal.Mutations) == 0 {
		return result, nil
	}
	if err := ensureRunnerDirectory(store.journalDir); err != nil {
		return CommitResult{}, err
	}
	journalPath := filepath.Join(store.journalDir, journal.ID+".json")
	if err := store.validateJournal(journalPath, journal); err != nil {
		return CommitResult{}, err
	}
	if err := writeRunnerJSONFile(journalPath, journal); err != nil {
		return CommitResult{}, err
	}
	if err := store.injectFailure(failureAfterPreparedJournal, 0); err != nil {
		return CommitResult{}, err
	}
	if err := store.applyCommitMutations(journal.Mutations); err != nil {
		if errors.Is(err, errInjectedTransactionFailure) {
			return CommitResult{}, err
		}
		rollbackErr := applyTransactionMutations(journal.Mutations, false)
		return CommitResult{}, errors.Join(err, rollbackErr)
	}
	journal.Phase = transactionCommitted
	if err := writeRunnerJSONFile(journalPath, journal); err != nil {
		rollbackErr := applyTransactionMutations(journal.Mutations, false)
		return CommitResult{}, errors.Join(err, rollbackErr)
	}
	if err := store.injectFailure(failureAfterCommittedJournal, 0); err != nil {
		return CommitResult{}, err
	}
	if err := store.injectFailure(failureBeforeJournalRemoval, 0); err != nil {
		return CommitResult{}, err
	}
	if err := removeRunnerFile(journalPath); err != nil {
		return CommitResult{}, err
	}
	return result, nil
}

func (store *Store) prepareJournalLocked(ctx context.Context, commit Commit) (transactionJournal, CommitResult, error) {
	if err := validateRuntimeCommit(commit); err != nil {
		return transactionJournal{}, CommitResult{}, err
	}
	journal := transactionJournal{ID: uuid.NewString(), Phase: transactionPrepared}
	result := CommitResult{}
	mutations := map[string]transactionMutation{}
	addMutation := func(path string, after []byte) error {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if _, duplicate := mutations[absolute]; duplicate {
			return fmt.Errorf("runtime transaction writes path %q more than once", absolute)
		}
		before, err := os.ReadFile(absolute)
		beforeExists := err == nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		mutations[absolute] = transactionMutation{Path: absolute, BeforeExists: beforeExists, Before: before, AfterExists: true, After: append([]byte(nil), after...)}
		return nil
	}

	if commit.Run != nil {
		run := cloneRunRecord(commit.Run.Run)
		path := store.execution.runPath(run.RunID)
		switch commit.Run.Mode {
		case RunWriteCreate:
			if err := validateNewRunDeletion(run); err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			if err := ensureRunNotDeletingLocked(store.execution.baseDir, run.RunID, "create a run"); err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			if err := ensureRunnerRecordDoesNotExist(path, "run", run.RunID); err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			if run.ParentRunID != "" {
				if err := validateRunID(run.ParentRunID); err != nil {
					return transactionJournal{}, CommitResult{}, err
				}
				if err := ensureRunNotDeletingLocked(store.execution.baseDir, run.ParentRunID, "create a child run"); err != nil {
					return transactionJournal{}, CommitResult{}, err
				}
				parent, err := store.execution.readRunLocked(run.ParentRunID)
				if err != nil {
					return transactionJournal{}, CommitResult{}, fmt.Errorf("load parent run %q: %w", run.ParentRunID, err)
				}
				if err := validateNewRunParent(run, parent); err != nil {
					return transactionJournal{}, CommitResult{}, err
				}
			}
		case RunWriteUpdate, RunWriteCheck:
			var existing RunRecord
			if err := readRunnerJSONFile(path, &existing); err != nil {
				if os.IsNotExist(err) {
					return transactionJournal{}, CommitResult{}, ErrRunnerRecordNotFound
				}
				return transactionJournal{}, CommitResult{}, err
			}
			if existing.RunID != run.RunID {
				return transactionJournal{}, CommitResult{}, fmt.Errorf("run %q metadata identity mismatch", run.RunID)
			}
			if existing.Revision != run.Revision {
				return transactionJournal{}, CommitResult{}, &RunRevisionConflictError{RunID: run.RunID, Expected: run.Revision, Actual: existing.Revision}
			}
			if commit.Run.Mode == RunWriteCheck {
				if err := ensureRunNotDeletingLocked(store.execution.baseDir, run.RunID, "write runtime records"); err != nil {
					return transactionJournal{}, CommitResult{}, err
				}
				if err := ensureRunNotDeleting(existing, "write runtime records"); err != nil {
					return transactionJournal{}, CommitResult{}, err
				}
				break
			}
			if err := validateRunDeletionTransition(ctx, existing, run); err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			run.Revision++
		}
		if commit.Run.Mode != RunWriteCheck {
			encoded, err := marshalRunnerJSONFile(run)
			if err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			if err := addMutation(path, encoded); err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			result.Run = &run
		}
	}

	for _, write := range commit.Steps {
		step := cloneStepRecord(write.Step)
		if err := store.ensureCommitRunLocked(commit, step.RunID, "write a step"); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
		path := store.execution.stepPath(step.RunID, step.StepID)
		var existing StepRecord
		readErr := readRunnerJSONFile(path, &existing)
		exists := readErr == nil
		if readErr != nil && !os.IsNotExist(readErr) {
			return transactionJournal{}, CommitResult{}, readErr
		}
		switch write.Mode {
		case StepWriteAppend:
			if exists {
				return transactionJournal{}, CommitResult{}, fmt.Errorf("step %q already exists", step.StepID)
			}
		case StepWriteUpdate:
			if !exists {
				return transactionJournal{}, CommitResult{}, ErrRunnerRecordNotFound
			}
			if existing.RunID != step.RunID || existing.StepID != step.StepID {
				return transactionJournal{}, CommitResult{}, fmt.Errorf("step %q metadata identity mismatch", step.StepID)
			}
		default:
			return transactionJournal{}, CommitResult{}, fmt.Errorf("invalid step write mode %q", write.Mode)
		}
		encoded, err := marshalRunnerJSONFile(step)
		if err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
		if err := addMutation(path, encoded); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
	}

	for _, write := range commit.Checkpoints {
		record := write.Record
		if err := store.ensureCommitRunLocked(commit, record.RunID, "write a checkpoint"); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
		metadataPath := store.checkpoints.metadataPath(record.RunID, record.CheckpointID)
		payloadPath := store.checkpoints.payloadPath(record.RunID, record.CheckpointID)
		if _, err := os.Stat(metadataPath); err == nil {
			return transactionJournal{}, CommitResult{}, fmt.Errorf("checkpoint %q already exists", record.CheckpointID)
		} else if !os.IsNotExist(err) {
			return transactionJournal{}, CommitResult{}, err
		}
		record.PayloadRef = payloadPath
		metadata, err := marshalRunnerJSONFile(record)
		if err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
		if err := addMutation(payloadPath, write.Payload); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
		if err := addMutation(metadataPath, metadata); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
	}

	eventsByPath := map[string][]Event{}
	for _, event := range commit.Events {
		if IsStreamingEvent(event.Type) {
			continue
		}
		if err := store.ensureCommitRunLocked(commit, event.RunID, "publish an event"); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
		path := store.events.eventsPath(event.RunID)
		eventsByPath[path] = append(eventsByPath[path], cloneEvent(event))
	}
	paths := make([]string, 0, len(eventsByPath))
	for path := range eventsByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		existing, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return transactionJournal{}, CommitResult{}, err
		}
		combined := append([]byte(nil), existing...)
		for _, event := range eventsByPath[path] {
			line, err := marshalRunnerJSONLine(event)
			if err != nil {
				return transactionJournal{}, CommitResult{}, err
			}
			combined = append(combined, line...)
		}
		if err := addMutation(path, combined); err != nil {
			return transactionJournal{}, CommitResult{}, err
		}
	}

	mutationPaths := make([]string, 0, len(mutations))
	for path := range mutations {
		mutationPaths = append(mutationPaths, path)
	}
	sort.Strings(mutationPaths)
	for _, path := range mutationPaths {
		journal.Mutations = append(journal.Mutations, mutations[path])
	}
	return journal, result, nil
}

func (store *Store) ensureCommitRunLocked(commit Commit, runID, action string) error {
	if commit.Run != nil && commit.Run.Run.RunID == runID {
		if commit.Run.Mode == RunWriteCheck {
			referencedRun, err := store.execution.readRunLocked(runID)
			if err != nil {
				return err
			}
			if err := ensureRunNotDeletingLocked(store.execution.baseDir, runID, action); err != nil {
				return err
			}
			return ensureRunNotDeleting(referencedRun, action)
		}
		return ensureRunNotDeleting(commit.Run.Run, action)
	}

	var referencedRun RunRecord
	if err := readRunnerJSONFile(store.execution.runPath(runID), &referencedRun); err != nil {
		if os.IsNotExist(err) {
			return ErrRunnerRecordNotFound
		}
		return err
	}
	if referencedRun.RunID != runID {
		return fmt.Errorf("run %q metadata identity mismatch", runID)
	}
	if err := ensureRunNotDeletingLocked(store.execution.baseDir, runID, action); err != nil {
		return err
	}
	return ensureRunNotDeleting(referencedRun, action)
}

func (store *Store) recoverLocked() error {
	files, err := os.ReadDir(store.journalDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		path := filepath.Join(store.journalDir, file.Name())
		var journal transactionJournal
		if err := readRunnerJSONFile(path, &journal); err != nil {
			return err
		}
		if err := store.validateJournal(path, journal); err != nil {
			return err
		}
		useAfter := journal.Phase == transactionCommitted
		if err := applyTransactionMutations(journal.Mutations, useAfter); err != nil {
			return err
		}
		if err := removeRunnerFile(path); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) validateJournal(path string, journal transactionJournal) error {
	if err := validateRunnerStorageID("transaction ID", journal.ID); err != nil {
		return err
	}
	if filepath.Base(path) != journal.ID+".json" {
		return fmt.Errorf("transaction journal %q identity mismatch", path)
	}
	switch journal.Phase {
	case transactionPrepared, transactionCommitted:
	default:
		return fmt.Errorf("transaction journal %q has unknown phase %q", journal.ID, journal.Phase)
	}
	seen := make(map[string]struct{}, len(journal.Mutations))
	for _, mutation := range journal.Mutations {
		absolute, err := filepath.Abs(mutation.Path)
		if err != nil {
			return err
		}
		if filepath.Clean(mutation.Path) != absolute {
			return fmt.Errorf("transaction journal %q has non-canonical mutation path %q", journal.ID, mutation.Path)
		}
		relative, err := filepath.Rel(store.baseDir, absolute)
		if err != nil {
			return err
		}
		if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("transaction journal %q mutation escapes store root: %q", journal.ID, mutation.Path)
		}
		if _, duplicate := seen[absolute]; duplicate {
			return fmt.Errorf("transaction journal %q repeats mutation path %q", journal.ID, mutation.Path)
		}
		seen[absolute] = struct{}{}
		if err := store.validateJournalMutation(relative, mutation); err != nil {
			return fmt.Errorf("transaction journal %q: %w", journal.ID, err)
		}
	}
	return nil
}

func (store *Store) validateJournalMutation(relative string, mutation transactionMutation) error {
	if !mutation.AfterExists {
		return fmt.Errorf("mutation %q has no after-state", mutation.Path)
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	validateStates := func(validate func([]byte) error) error {
		if mutation.BeforeExists {
			if err := validate(mutation.Before); err != nil {
				return fmt.Errorf("mutation %q before-state: %w", mutation.Path, err)
			}
		}
		if err := validate(mutation.After); err != nil {
			return fmt.Errorf("mutation %q after-state: %w", mutation.Path, err)
		}
		return nil
	}
	switch {
	case len(parts) == 3 && parts[0] == "execution" && parts[1] == "runs" && strings.EqualFold(filepath.Ext(parts[2]), ".json"):
		runID := strings.TrimSuffix(parts[2], filepath.Ext(parts[2]))
		if err := validateRunID(runID); err != nil {
			return err
		}
		return validateStates(func(data []byte) error {
			var run RunRecord
			if err := json.Unmarshal(data, &run); err != nil {
				return err
			}
			if run.RunID != runID {
				return fmt.Errorf("run %q metadata identity mismatch", runID)
			}
			return validateRunChildState(run)
		})
	case len(parts) == 4 && parts[0] == "execution" && parts[1] == "steps" && strings.EqualFold(filepath.Ext(parts[3]), ".json"):
		runID := parts[2]
		stepID := strings.TrimSuffix(parts[3], filepath.Ext(parts[3]))
		if err := validateRunID(runID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("step ID", stepID); err != nil {
			return err
		}
		return validateStates(func(data []byte) error {
			var step StepRecord
			if err := json.Unmarshal(data, &step); err != nil {
				return err
			}
			if step.RunID != runID || step.StepID != stepID {
				return fmt.Errorf("step %q metadata identity mismatch", stepID)
			}
			return nil
		})
	case len(parts) == 3 && parts[0] == "checkpoints" && strings.EqualFold(filepath.Ext(parts[2]), ".json"):
		runID := parts[1]
		checkpointID := strings.TrimSuffix(parts[2], filepath.Ext(parts[2]))
		if err := validateRunID(runID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("checkpoint ID", checkpointID); err != nil {
			return err
		}
		return validateStates(func(data []byte) error {
			var checkpoint CheckpointRecord
			if err := json.Unmarshal(data, &checkpoint); err != nil {
				return err
			}
			if checkpoint.RunID != runID || checkpoint.CheckpointID != checkpointID {
				return fmt.Errorf("checkpoint %q metadata identity mismatch", checkpointID)
			}
			expectedPayload := store.checkpoints.payloadPath(runID, checkpointID)
			if filepath.Clean(checkpoint.PayloadRef) != expectedPayload {
				return fmt.Errorf("checkpoint %q payload identity mismatch", checkpointID)
			}
			return nil
		})
	case len(parts) == 4 && parts[0] == "checkpoints" && parts[2] == "payloads" && strings.EqualFold(filepath.Ext(parts[3]), ".bin"):
		if err := validateRunID(parts[1]); err != nil {
			return err
		}
		return validateRunnerStorageID("checkpoint ID", strings.TrimSuffix(parts[3], filepath.Ext(parts[3])))
	case len(parts) == 2 && parts[0] == "events" && strings.EqualFold(filepath.Ext(parts[1]), ".jsonl"):
		runID := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
		if err := validateRunID(runID); err != nil {
			return err
		}
		return validateStates(func(data []byte) error {
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				var event Event
				if err := json.Unmarshal(line, &event); err != nil {
					return err
				}
				if event.RunID != runID {
					return fmt.Errorf("event %q metadata identity mismatch", event.ID)
				}
			}
			return nil
		})
	default:
		return fmt.Errorf("mutation path %q is outside runtime record namespaces", mutation.Path)
	}
}

func (store *Store) applyCommitMutations(mutations []transactionMutation) error {
	for index, mutation := range mutations {
		if err := store.injectFailure(failureBeforeMutation, index); err != nil {
			return err
		}
		if err := applyTransactionMutations([]transactionMutation{mutation}, true); err != nil {
			return err
		}
		if err := store.injectFailure(failureAfterMutation, index); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) injectFailure(point transactionFailurePoint, mutationIndex int) error {
	if store == nil || store.failure != point {
		return nil
	}
	if point == failureBeforeMutation || point == failureAfterMutation {
		if store.failureAt != mutationIndex {
			return nil
		}
	}
	return fmt.Errorf("%w at %s", errInjectedTransactionFailure, point)
}

func applyTransactionMutations(mutations []transactionMutation, after bool) error {
	items := mutations
	if !after {
		items = append([]transactionMutation(nil), mutations...)
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	var result error
	for _, mutation := range items {
		exists := mutation.AfterExists
		data := mutation.After
		if !after {
			exists = mutation.BeforeExists
			data = mutation.Before
		}
		if exists {
			result = errors.Join(result, writeRunnerBinaryFile(mutation.Path, data))
			continue
		}
		if err := removeRunnerFile(mutation.Path); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (store *Store) lockComponents() func() {
	store.execution.mu.Lock()
	return func() {
		store.execution.mu.Unlock()
	}
}
