package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type StepWriteMode string

const (
	StepWriteAppend StepWriteMode = "append"
	StepWriteUpdate StepWriteMode = "update"
)

type RunWriteMode string

const (
	RunWriteCreate RunWriteMode = "create"
	RunWriteUpdate RunWriteMode = "update"
	RunWriteCheck  RunWriteMode = "check"
)

type RunWrite struct {
	Mode RunWriteMode
	Run  RunRecord
}

type StepWrite struct {
	Mode StepWriteMode
	Step StepRecord
}

type CheckpointWrite struct {
	Record  CheckpointRecord
	Payload []byte
}

type Commit struct {
	Run         *RunWrite
	Steps       []StepWrite
	Checkpoints []CheckpointWrite
	Events      []Event
}

type CommitResult struct {
	Run *RunRecord
}

type TransactionStore interface {
	Commit(ctx context.Context, commit Commit) (CommitResult, error)
}

type MemoryRuntimeStore struct {
	execution   *MemoryExecutionStore
	checkpoints *MemoryCheckpointStore
	events      *MemoryEventSink
}

func NewMemoryRuntimeStore() *MemoryRuntimeStore {
	return &MemoryRuntimeStore{
		execution:   NewMemoryExecutionStore(),
		checkpoints: NewMemoryCheckpointStore(),
		events:      NewMemoryEventSink(),
	}
}

func newMemoryRuntimeStore(execution *MemoryExecutionStore, checkpoints *MemoryCheckpointStore, events *MemoryEventSink) *MemoryRuntimeStore {
	if execution == nil || checkpoints == nil || events == nil {
		return nil
	}
	return &MemoryRuntimeStore{execution: execution, checkpoints: checkpoints, events: events}
}

func (store *MemoryRuntimeStore) CreateRun(ctx context.Context, run RunRecord) error {
	return store.execution.CreateRun(ctx, run)
}

func (store *MemoryRuntimeStore) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	return store.execution.CompareAndSwapRun(ctx, expectedRevision, run)
}

func (store *MemoryRuntimeStore) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	return store.execution.GetRun(ctx, runID)
}

func (store *MemoryRuntimeStore) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	return store.execution.ListRuns(ctx, filter)
}

func (store *MemoryRuntimeStore) AppendStep(ctx context.Context, step StepRecord) error {
	return store.execution.AppendStep(ctx, step)
}

func (store *MemoryRuntimeStore) UpdateStep(ctx context.Context, step StepRecord) error {
	return store.execution.UpdateStep(ctx, step)
}

func (store *MemoryRuntimeStore) GetStep(ctx context.Context, stepID string) (StepRecord, error) {
	return store.execution.GetStep(ctx, stepID)
}

func (store *MemoryRuntimeStore) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	return store.execution.ListSteps(ctx, runID)
}

func (store *MemoryRuntimeStore) Save(ctx context.Context, record CheckpointRecord, payload []byte) error {
	return store.checkpoints.Save(ctx, record, payload)
}

func (store *MemoryRuntimeStore) Load(ctx context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
	return store.checkpoints.Load(ctx, checkpointID)
}

func (store *MemoryRuntimeStore) List(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	return store.checkpoints.List(ctx, runID)
}

func (store *MemoryRuntimeStore) Publish(ctx context.Context, event Event) error {
	return store.events.Publish(ctx, event)
}

func (store *MemoryRuntimeStore) PublishBatch(ctx context.Context, events []Event) error {
	return store.events.PublishBatch(ctx, events)
}

func (store *MemoryRuntimeStore) ListEvents(runID string) ([]Event, error) {
	return store.events.ListEvents(runID)
}

func (store *MemoryRuntimeStore) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	return store.events.ListEventPage(runID, cursor, limit)
}

func (store *MemoryRuntimeStore) DeleteRun(ctx context.Context, runID string) error {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return errors.New("memory runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	store.execution.mu.Lock()
	defer store.execution.mu.Unlock()
	store.checkpoints.mu.Lock()
	defer store.checkpoints.mu.Unlock()
	store.events.mu.Lock()
	defer store.events.mu.Unlock()
	store.execution.ensureInitialized()
	if err := validateMemoryRunFenceDeletion(ctx, store.execution.deletions, runID); err != nil {
		return err
	}
	if err := validateMemoryRunFenceDeletion(ctx, store.checkpoints.deletions, runID); err != nil {
		return err
	}
	if err := validateMemoryRunFenceDeletion(ctx, store.events.deletions, runID); err != nil {
		return err
	}
	if run, exists := store.execution.runs[runID]; exists {
		if err := validateMemoryRunDeletion(ctx, runID, run.Deletion); err != nil {
			return err
		}
	}
	for checkpointID, checkpoint := range store.checkpoints.checkpoints {
		if checkpoint.record.RunID == runID {
			delete(store.checkpoints.checkpoints, checkpointID)
		}
	}
	delete(store.events.events, runID)
	for stepID, step := range store.execution.steps {
		if step.RunID == runID {
			delete(store.execution.steps, stepID)
		}
	}
	delete(store.execution.runs, runID)
	return nil
}

func (store *MemoryRuntimeStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return errors.New("memory runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	store.execution.mu.Lock()
	defer store.execution.mu.Unlock()
	store.checkpoints.mu.Lock()
	defer store.checkpoints.mu.Unlock()
	store.events.mu.Lock()
	defer store.events.mu.Unlock()
	store.execution.ensureInitialized()
	for _, fences := range []map[string]string{
		store.execution.deletions,
		store.checkpoints.deletions,
		store.events.deletions,
	} {
		if existingID := fences[runID]; existingID != "" && existingID != deletionID {
			return fmt.Errorf("%w: run %q is fenced by deletion %q, not %q", ErrRunControlNotAllowed, runID, existingID, deletionID)
		}
	}
	store.execution.deletions[runID] = deletionID
	if store.checkpoints.deletions == nil {
		store.checkpoints.deletions = map[string]string{}
	}
	store.checkpoints.deletions[runID] = deletionID
	if store.events.deletions == nil {
		store.events.deletions = map[string]string{}
	}
	store.events.deletions[runID] = deletionID
	return nil
}

func (store *MemoryRuntimeStore) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return CommitResult{}, errors.New("memory runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return CommitResult{}, err
	}
	commit = sanitizeCommit(ctx, commit)
	if err := validateRuntimeCommit(commit); err != nil {
		return CommitResult{}, err
	}
	store.execution.mu.Lock()
	defer store.execution.mu.Unlock()
	store.checkpoints.mu.Lock()
	defer store.checkpoints.mu.Unlock()
	store.events.mu.Lock()
	defer store.events.mu.Unlock()

	store.execution.ensureInitialized()
	runs := cloneRunRecords(store.execution.runs)
	steps := cloneStepRecords(store.execution.steps)
	checkpoints := cloneMemoryCheckpoints(store.checkpoints.checkpoints)
	events := cloneMemoryEvents(store.events.events)

	result, err := applyMemoryCommit(ctx, commit, runs, steps, checkpoints, events, store.execution.deletions)
	if err != nil {
		return CommitResult{}, err
	}
	store.execution.runs = runs
	store.execution.steps = steps
	store.checkpoints.checkpoints = checkpoints
	store.events.events = events
	return result, nil
}

func applyMemoryCommit(
	ctx context.Context,
	commit Commit,
	runs map[string]RunRecord,
	steps map[string]StepRecord,
	checkpoints map[string]memoryCheckpoint,
	events map[string][]Event,
	deletions map[string]string,
) (CommitResult, error) {
	if err := validateRuntimeCommit(commit); err != nil {
		return CommitResult{}, err
	}
	result := CommitResult{}
	if commit.Run != nil {
		run := cloneRunRecord(commit.Run.Run)
		existing, exists := runs[run.RunID]
		switch commit.Run.Mode {
		case RunWriteCreate:
			if err := validateNewRunDeletion(run); err != nil {
				return CommitResult{}, err
			}
			if err := ensureMemoryRunWritable(deletions, run.RunID); err != nil {
				return CommitResult{}, err
			}
			if exists {
				return CommitResult{}, fmt.Errorf("run %q already exists", run.RunID)
			}
			if run.ParentRunID != "" {
				if err := ensureMemoryRunWritable(deletions, run.ParentRunID); err != nil {
					return CommitResult{}, err
				}
				parent, parentExists := runs[run.ParentRunID]
				if !parentExists {
					return CommitResult{}, fmt.Errorf("parent run %q: %w", run.ParentRunID, ErrRunnerRecordNotFound)
				}
				if err := validateNewRunParent(run, parent); err != nil {
					return CommitResult{}, err
				}
			}
		case RunWriteUpdate, RunWriteCheck:
			if !exists {
				return CommitResult{}, ErrRunnerRecordNotFound
			}
			if existing.RunID != run.RunID {
				return CommitResult{}, fmt.Errorf("run %q metadata identity mismatch", run.RunID)
			}
			if existing.Revision != run.Revision {
				return CommitResult{}, &RunRevisionConflictError{RunID: run.RunID, Expected: run.Revision, Actual: existing.Revision}
			}
			if commit.Run.Mode == RunWriteCheck {
				if err := ensureMemoryRunWritable(deletions, run.RunID); err != nil {
					return CommitResult{}, err
				}
				if err := ensureRunNotDeleting(existing, "write runtime records"); err != nil {
					return CommitResult{}, err
				}
				break
			}
			if err := validateRunDeletionTransition(ctx, existing, run); err != nil {
				return CommitResult{}, err
			}
			run.Revision++
		}
		if commit.Run.Mode != RunWriteCheck {
			runs[run.RunID] = cloneRunRecord(run)
			result.Run = &run
		}
	}
	for _, write := range commit.Steps {
		step := cloneStepRecord(write.Step)
		if err := validateRunnerStorageID("run ID", step.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
			return CommitResult{}, err
		}
		run, exists := runs[step.RunID]
		if !exists {
			return CommitResult{}, ErrRunnerRecordNotFound
		}
		if err := ensureMemoryRunWritable(deletions, step.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := ensureRunNotDeleting(run, "write a step"); err != nil {
			return CommitResult{}, err
		}
		existing, exists := steps[step.StepID]
		switch write.Mode {
		case StepWriteAppend:
			if exists {
				return CommitResult{}, fmt.Errorf("step %q already exists", step.StepID)
			}
		case StepWriteUpdate:
			if !exists || existing.RunID != step.RunID {
				return CommitResult{}, ErrRunnerRecordNotFound
			}
		default:
			return CommitResult{}, fmt.Errorf("invalid step write mode %q", write.Mode)
		}
		steps[step.StepID] = step
	}
	for _, write := range commit.Checkpoints {
		record := write.Record
		if err := validateRunnerStorageID("run ID", record.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := validateRunnerStorageID("checkpoint ID", record.CheckpointID); err != nil {
			return CommitResult{}, err
		}
		run, exists := runs[record.RunID]
		if !exists {
			return CommitResult{}, ErrRunnerRecordNotFound
		}
		if err := ensureMemoryRunWritable(deletions, record.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := ensureRunNotDeleting(run, "write a checkpoint"); err != nil {
			return CommitResult{}, err
		}
		if _, exists := checkpoints[record.CheckpointID]; exists {
			return CommitResult{}, fmt.Errorf("checkpoint %q already exists", record.CheckpointID)
		}
		record.PayloadRef = ""
		checkpoints[record.CheckpointID] = memoryCheckpoint{record: record, payload: append([]byte(nil), write.Payload...)}
	}
	for _, event := range commit.Events {
		if IsStreamingEvent(event.Type) {
			continue
		}
		run, exists := runs[event.RunID]
		if !exists {
			return CommitResult{}, ErrRunnerRecordNotFound
		}
		if err := ensureMemoryRunWritable(deletions, event.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := ensureRunNotDeleting(run, "publish an event"); err != nil {
			return CommitResult{}, err
		}
		events[event.RunID] = append(events[event.RunID], cloneEvent(event))
	}
	return result, nil
}

func validateRuntimeCommit(commit Commit) error {
	if commit.Run != nil {
		if err := validateRunnerStorageID("run ID", commit.Run.Run.RunID); err != nil {
			return err
		}
		if err := validateRunChildState(commit.Run.Run); err != nil {
			return err
		}
		switch commit.Run.Mode {
		case RunWriteCreate, RunWriteUpdate, RunWriteCheck:
		default:
			return fmt.Errorf("invalid run write mode %q", commit.Run.Mode)
		}
	}
	for _, write := range commit.Steps {
		if err := validateRunnerStorageID("run ID", write.Step.RunID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("step ID", write.Step.StepID); err != nil {
			return err
		}
		switch write.Mode {
		case StepWriteAppend, StepWriteUpdate:
		default:
			return fmt.Errorf("invalid step write mode %q", write.Mode)
		}
	}
	for _, write := range commit.Checkpoints {
		if err := validateRunnerStorageID("run ID", write.Record.RunID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("checkpoint ID", write.Record.CheckpointID); err != nil {
			return err
		}
	}
	for _, event := range commit.Events {
		if IsStreamingEvent(event.Type) {
			continue
		}
		if err := validateRunnerStorageID("run ID", event.RunID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("event ID", event.ID); err != nil {
			return err
		}
	}
	return nil
}

func cloneRunRecords(source map[string]RunRecord) map[string]RunRecord {
	cloned := make(map[string]RunRecord, len(source))
	for key, value := range source {
		cloned[key] = cloneRunRecord(value)
	}
	return cloned
}

func cloneStepRecords(source map[string]StepRecord) map[string]StepRecord {
	cloned := make(map[string]StepRecord, len(source))
	for key, value := range source {
		cloned[key] = cloneStepRecord(value)
	}
	return cloned
}

func cloneMemoryCheckpoints(source map[string]memoryCheckpoint) map[string]memoryCheckpoint {
	cloned := make(map[string]memoryCheckpoint, len(source))
	for key, value := range source {
		cloned[key] = memoryCheckpoint{record: value.record, payload: append([]byte(nil), value.payload...)}
	}
	return cloned
}

func cloneMemoryEvents(source map[string][]Event) map[string][]Event {
	cloned := make(map[string][]Event, len(source))
	for runID, items := range source {
		cloned[runID] = make([]Event, len(items))
		for index, item := range items {
			cloned[runID][index] = cloneEvent(item)
		}
	}
	return cloned
}

type FileRuntimeStore struct {
	execution   *FileExecutionStore
	checkpoints *FileCheckpointStore
	events      *FileEventSink
	journalDir  string
}

func NewFileRuntimeStore(baseDir string) (*FileRuntimeStore, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("runtime store base directory is required")
	}
	absolute, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	return newFileRuntimeStore(
		NewFileExecutionStore(filepath.Join(absolute, "execution")),
		NewFileCheckpointStore(filepath.Join(absolute, "checkpoints")),
		NewFileEventSink(filepath.Join(absolute, "events")),
	)
}

func newFileRuntimeStore(execution *FileExecutionStore, checkpoints *FileCheckpointStore, events *FileEventSink) (*FileRuntimeStore, error) {
	if execution == nil || checkpoints == nil || events == nil {
		return nil, fmt.Errorf("file runtime store components are required")
	}
	store := &FileRuntimeStore{
		execution:   execution,
		checkpoints: checkpoints,
		events:      events,
		journalDir:  filepath.Join(execution.baseDir, ".transactions"),
	}
	unlock := store.lockComponents()
	defer unlock()
	if err := store.recoverLocked(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *FileRuntimeStore) CreateRun(ctx context.Context, run RunRecord) error {
	return store.execution.CreateRun(ctx, run)
}

func (store *FileRuntimeStore) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	return store.execution.CompareAndSwapRun(ctx, expectedRevision, run)
}

func (store *FileRuntimeStore) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	return store.execution.GetRun(ctx, runID)
}

func (store *FileRuntimeStore) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	return store.execution.ListRuns(ctx, filter)
}

func (store *FileRuntimeStore) AppendStep(ctx context.Context, step StepRecord) error {
	return store.execution.AppendStep(ctx, step)
}

func (store *FileRuntimeStore) UpdateStep(ctx context.Context, step StepRecord) error {
	return store.execution.UpdateStep(ctx, step)
}

func (store *FileRuntimeStore) GetStep(ctx context.Context, stepID string) (StepRecord, error) {
	return store.execution.GetStep(ctx, stepID)
}

func (store *FileRuntimeStore) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	return store.execution.ListSteps(ctx, runID)
}

func (store *FileRuntimeStore) Save(ctx context.Context, record CheckpointRecord, payload []byte) error {
	return store.checkpoints.Save(ctx, record, payload)
}

func (store *FileRuntimeStore) Load(ctx context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
	return store.checkpoints.Load(ctx, checkpointID)
}

func (store *FileRuntimeStore) List(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	return store.checkpoints.List(ctx, runID)
}

func (store *FileRuntimeStore) Publish(ctx context.Context, event Event) error {
	return store.events.Publish(ctx, event)
}

func (store *FileRuntimeStore) PublishBatch(ctx context.Context, events []Event) error {
	return store.events.PublishBatch(ctx, events)
}

func (store *FileRuntimeStore) ListEvents(runID string) ([]Event, error) {
	return store.events.ListEvents(runID)
}

func (store *FileRuntimeStore) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	return store.events.ListEventPage(runID, cursor, limit)
}

func (store *FileRuntimeStore) DeleteRun(ctx context.Context, runID string) error {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return errors.New("file runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateFileStoreRunID(runID); err != nil {
		return err
	}
	unlock := store.lockComponents()
	defer unlock()
	if err := store.recoverLocked(); err != nil {
		return err
	}
	for _, baseDir := range []string{store.execution.baseDir, store.checkpoints.baseDir, store.events.baseDir} {
		if err := requireFileRunDeletionLocked(ctx, baseDir, runID); err != nil {
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
	if err := os.RemoveAll(store.checkpoints.checkpointsDir(runID)); err != nil {
		return err
	}
	if err := os.Remove(store.events.eventsPath(runID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(store.execution.stepsDir(runID)); err != nil {
		return err
	}
	if err := os.Remove(store.execution.runPath(runID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (store *FileRuntimeStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return errors.New("file runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateFileStoreRunID(runID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	unlock := store.lockComponents()
	defer unlock()
	if err := store.recoverLocked(); err != nil {
		return err
	}
	for _, baseDir := range []string{store.execution.baseDir, store.checkpoints.baseDir, store.events.baseDir} {
		var fence fileRunDeletionFence
		err := readRunnerJSONFile(fileRunDeletionPath(baseDir, runID), &fence)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := validateFileRunDeletionFence(fence, runID); err != nil {
			return err
		}
		if fence.DeletionID != deletionID {
			return fmt.Errorf("%w: run %q is fenced by deletion %q, not %q", ErrRunControlNotAllowed, runID, fence.DeletionID, deletionID)
		}
	}
	for _, baseDir := range []string{store.execution.baseDir, store.checkpoints.baseDir, store.events.baseDir} {
		if err := fenceFileRunDeletionLocked(ctx, baseDir, runID, deletionID); err != nil {
			return err
		}
	}
	return nil
}

type fileTransactionPhase string

const (
	fileTransactionPrepared  fileTransactionPhase = "prepared"
	fileTransactionCommitted fileTransactionPhase = "committed"
)

type fileTransactionJournal struct {
	ID        string                `json:"id"`
	Phase     fileTransactionPhase  `json:"phase"`
	Mutations []fileTransactionFile `json:"mutations"`
}

type fileTransactionFile struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	Before       []byte `json:"before,omitempty"`
	AfterExists  bool   `json:"after_exists"`
	After        []byte `json:"after,omitempty"`
}

func (store *FileRuntimeStore) Commit(ctx context.Context, commit Commit) (CommitResult, error) {
	if store == nil {
		return CommitResult{}, errors.New("file runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return CommitResult{}, err
	}
	commit = sanitizeCommit(ctx, commit)
	if err := validateRuntimeCommit(commit); err != nil {
		return CommitResult{}, err
	}
	unlock := store.lockComponents()
	defer unlock()
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
	if err := os.MkdirAll(store.journalDir, 0o700); err != nil {
		return CommitResult{}, err
	}
	journalPath := filepath.Join(store.journalDir, journal.ID+".json")
	if err := writeRunnerJSONFile(journalPath, journal); err != nil {
		return CommitResult{}, err
	}
	if err := applyFileTransactionState(journal.Mutations, true); err != nil {
		rollbackErr := applyFileTransactionState(journal.Mutations, false)
		return CommitResult{}, errors.Join(err, rollbackErr)
	}
	journal.Phase = fileTransactionCommitted
	if err := writeRunnerJSONFile(journalPath, journal); err != nil {
		rollbackErr := applyFileTransactionState(journal.Mutations, false)
		return CommitResult{}, errors.Join(err, rollbackErr)
	}
	if err := os.Remove(journalPath); err != nil && !os.IsNotExist(err) {
		return CommitResult{}, err
	}
	return result, nil
}

func (store *FileRuntimeStore) prepareJournalLocked(ctx context.Context, commit Commit) (fileTransactionJournal, CommitResult, error) {
	if err := validateRuntimeCommit(commit); err != nil {
		return fileTransactionJournal{}, CommitResult{}, err
	}
	journal := fileTransactionJournal{ID: uuid.NewString(), Phase: fileTransactionPrepared}
	result := CommitResult{}
	mutations := map[string]fileTransactionFile{}
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
		mutations[absolute] = fileTransactionFile{Path: absolute, BeforeExists: beforeExists, Before: before, AfterExists: true, After: append([]byte(nil), after...)}
		return nil
	}

	if commit.Run != nil {
		run := cloneRunRecord(commit.Run.Run)
		path := store.execution.runPath(run.RunID)
		switch commit.Run.Mode {
		case RunWriteCreate:
			if err := validateNewRunDeletion(run); err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			if err := ensureFileRunNotDeletingLocked(store.execution.baseDir, run.RunID, "create a run"); err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			if err := ensureRunnerRecordDoesNotExist(path, "run", run.RunID); err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			if run.ParentRunID != "" {
				if err := validateFileStoreRunID(run.ParentRunID); err != nil {
					return fileTransactionJournal{}, CommitResult{}, err
				}
				if err := ensureFileRunNotDeletingLocked(store.execution.baseDir, run.ParentRunID, "create a child run"); err != nil {
					return fileTransactionJournal{}, CommitResult{}, err
				}
				parent, err := store.execution.readRunLocked(run.ParentRunID)
				if err != nil {
					return fileTransactionJournal{}, CommitResult{}, fmt.Errorf("load parent run %q: %w", run.ParentRunID, err)
				}
				if err := validateNewRunParent(run, parent); err != nil {
					return fileTransactionJournal{}, CommitResult{}, err
				}
			}
		case RunWriteUpdate, RunWriteCheck:
			var existing RunRecord
			if err := readRunnerJSONFile(path, &existing); err != nil {
				if os.IsNotExist(err) {
					return fileTransactionJournal{}, CommitResult{}, ErrRunnerRecordNotFound
				}
				return fileTransactionJournal{}, CommitResult{}, err
			}
			if existing.RunID != run.RunID {
				return fileTransactionJournal{}, CommitResult{}, fmt.Errorf("run %q metadata identity mismatch", run.RunID)
			}
			if existing.Revision != run.Revision {
				return fileTransactionJournal{}, CommitResult{}, &RunRevisionConflictError{RunID: run.RunID, Expected: run.Revision, Actual: existing.Revision}
			}
			if commit.Run.Mode == RunWriteCheck {
				if err := ensureFileRunNotDeletingLocked(store.execution.baseDir, run.RunID, "write runtime records"); err != nil {
					return fileTransactionJournal{}, CommitResult{}, err
				}
				if err := ensureRunNotDeleting(existing, "write runtime records"); err != nil {
					return fileTransactionJournal{}, CommitResult{}, err
				}
				break
			}
			if err := validateRunDeletionTransition(ctx, existing, run); err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			run.Revision++
		}
		if commit.Run.Mode != RunWriteCheck {
			encoded, err := marshalRunnerJSONFile(run)
			if err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			if err := addMutation(path, encoded); err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			result.Run = &run
		}
	}

	for _, write := range commit.Steps {
		step := cloneStepRecord(write.Step)
		if err := store.ensureFileCommitRunLocked(commit, step.RunID, "write a step"); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
		path := store.execution.stepPath(step.RunID, step.StepID)
		var existing StepRecord
		readErr := readRunnerJSONFile(path, &existing)
		exists := readErr == nil
		if readErr != nil && !os.IsNotExist(readErr) {
			return fileTransactionJournal{}, CommitResult{}, readErr
		}
		switch write.Mode {
		case StepWriteAppend:
			if exists {
				return fileTransactionJournal{}, CommitResult{}, fmt.Errorf("step %q already exists", step.StepID)
			}
		case StepWriteUpdate:
			if !exists {
				return fileTransactionJournal{}, CommitResult{}, ErrRunnerRecordNotFound
			}
			if existing.RunID != step.RunID || existing.StepID != step.StepID {
				return fileTransactionJournal{}, CommitResult{}, fmt.Errorf("step %q metadata identity mismatch", step.StepID)
			}
		default:
			return fileTransactionJournal{}, CommitResult{}, fmt.Errorf("invalid step write mode %q", write.Mode)
		}
		encoded, err := marshalRunnerJSONFile(step)
		if err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
		if err := addMutation(path, encoded); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
	}

	for _, write := range commit.Checkpoints {
		record := write.Record
		if err := store.ensureFileCommitRunLocked(commit, record.RunID, "write a checkpoint"); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
		metadataPath := store.checkpoints.metadataPath(record.RunID, record.CheckpointID)
		payloadPath := store.checkpoints.payloadPath(record.RunID, record.CheckpointID)
		if _, err := os.Stat(metadataPath); err == nil {
			return fileTransactionJournal{}, CommitResult{}, fmt.Errorf("checkpoint %q already exists", record.CheckpointID)
		} else if !os.IsNotExist(err) {
			return fileTransactionJournal{}, CommitResult{}, err
		}
		record.PayloadRef = payloadPath
		metadata, err := marshalRunnerJSONFile(record)
		if err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
		if err := addMutation(payloadPath, write.Payload); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
		if err := addMutation(metadataPath, metadata); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
		}
	}

	eventsByPath := map[string][]Event{}
	for _, event := range commit.Events {
		if IsStreamingEvent(event.Type) {
			continue
		}
		if err := store.ensureFileCommitRunLocked(commit, event.RunID, "publish an event"); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
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
			return fileTransactionJournal{}, CommitResult{}, err
		}
		combined := append([]byte(nil), existing...)
		for _, event := range eventsByPath[path] {
			line, err := marshalRunnerJSONLine(event)
			if err != nil {
				return fileTransactionJournal{}, CommitResult{}, err
			}
			combined = append(combined, line...)
		}
		if err := addMutation(path, combined); err != nil {
			return fileTransactionJournal{}, CommitResult{}, err
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

func (store *FileRuntimeStore) ensureFileCommitRunLocked(commit Commit, runID, action string) error {
	if commit.Run != nil && commit.Run.Run.RunID == runID {
		if commit.Run.Mode == RunWriteCheck {
			referencedRun, err := store.execution.readRunLocked(runID)
			if err != nil {
				return err
			}
			if err := ensureFileRunNotDeletingLocked(store.execution.baseDir, runID, action); err != nil {
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
	if err := ensureFileRunNotDeletingLocked(store.execution.baseDir, runID, action); err != nil {
		return err
	}
	return ensureRunNotDeleting(referencedRun, action)
}

func (store *FileRuntimeStore) recoverLocked() error {
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
		var journal fileTransactionJournal
		if err := readRunnerJSONFile(path, &journal); err != nil {
			return err
		}
		useAfter := journal.Phase == fileTransactionCommitted
		if err := applyFileTransactionState(journal.Mutations, useAfter); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func applyFileTransactionState(mutations []fileTransactionFile, after bool) error {
	items := mutations
	if !after {
		items = append([]fileTransactionFile(nil), mutations...)
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
		if err := os.Remove(mutation.Path); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, err)
		}
	}
	return result
}

type fileStoreLockRef struct {
	key   string
	mutex *sync.Mutex
}

func (store *FileRuntimeStore) lockComponents() func() {
	refs := []fileStoreLockRef{
		fileStoreLock(store.execution.baseDir, &store.execution.mu),
		fileStoreLock(store.checkpoints.baseDir, &store.checkpoints.mu),
		fileStoreLock(store.events.baseDir, &store.events.mu),
	}
	unique := make(map[*sync.Mutex]fileStoreLockRef, len(refs))
	for _, ref := range refs {
		unique[ref.mutex] = ref
	}
	refs = refs[:0]
	for _, ref := range unique {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(leftIndex, rightIndex int) bool { return refs[leftIndex].key < refs[rightIndex].key })
	for _, ref := range refs {
		ref.mutex.Lock()
	}
	return func() {
		for index := len(refs) - 1; index >= 0; index-- {
			refs[index].mutex.Unlock()
		}
	}
}

func fileStoreLock(baseDir string, storeMutex *fileStoreMutex) fileStoreLockRef {
	storeMutex.once.Do(func() { storeMutex.shared = sharedFileStoreMutex(storeMutex.baseDir) })
	key, err := filepath.Abs(baseDir)
	if err != nil {
		key = filepath.Clean(baseDir)
	}
	return fileStoreLockRef{key: strings.ToLower(key), mutex: storeMutex.shared}
}

func resolveRuntimeTransactionStore(execution ExecutionStore, checkpoints CheckpointStore, eventSink EventSink) (TransactionStore, error) {
	if store, ok := execution.(TransactionStore); ok {
		return store, nil
	}
	persistentEventSink := firstTransactionalEventSink(eventSink)
	if memoryExecution, ok := execution.(*MemoryExecutionStore); ok {
		memoryCheckpoints, checkpointOK := checkpoints.(*MemoryCheckpointStore)
		memoryEvents, eventOK := persistentEventSink.(*MemoryEventSink)
		if checkpointOK && eventOK {
			return newMemoryRuntimeStore(memoryExecution, memoryCheckpoints, memoryEvents), nil
		}
	}
	if fileExecution, ok := execution.(*FileExecutionStore); ok {
		fileCheckpoints, checkpointOK := checkpoints.(*FileCheckpointStore)
		fileEvents, eventOK := persistentEventSink.(*FileEventSink)
		if checkpointOK && eventOK {
			return newFileRuntimeStore(fileExecution, fileCheckpoints, fileEvents)
		}
	}
	return nil, fmt.Errorf("custom runtime stores require WithRuntimeTransactionStore")
}

func firstTransactionalEventSink(sink EventSink) EventSink {
	switch typed := sink.(type) {
	case *MemoryRuntimeStore, *FileRuntimeStore, *MemoryEventSink, *FileEventSink:
		return sink
	case *CombineEventSink:
		for _, nested := range typed.sinks {
			if candidate := firstTransactionalEventSink(nested); candidate != nil {
				return candidate
			}
		}
	}
	return nil
}

func publishCommittedEventObservers(ctx context.Context, sink EventSink, transactionStore TransactionStore, events []Event) error {
	if len(events) == 0 || sink == nil {
		return nil
	}
	if combine, ok := sink.(*CombineEventSink); ok {
		var publishErrors []error
		for _, nested := range combine.sinks {
			if eventSinkIsTransactionStore(nested, transactionStore) {
				continue
			}
			if err := nested.PublishBatch(ctx, events); err != nil {
				publishErrors = append(publishErrors, err)
			}
		}
		return errors.Join(publishErrors...)
	}
	if eventSinkIsTransactionStore(sink, transactionStore) {
		return nil
	}
	return sink.PublishBatch(ctx, events)
}

func observeCommittedEvents(ctx context.Context, sink EventSink, transactionStore TransactionStore, events []Event) {
	if len(events) == 0 {
		return
	}
	observerCtx := context.WithoutCancel(normalizeRunnerContext(ctx))
	observationErrors := make([]error, 0, len(events)+1)
	if err := publishCommittedEventObservers(observerCtx, sink, transactionStore, events); err != nil {
		observationErrors = append(observationErrors, err)
	}
	for _, event := range events {
		if err := observeRunnerContextEvent(observerCtx, event); err != nil {
			observationErrors = append(observationErrors, err)
		}
	}
	if err := errors.Join(observationErrors...); err != nil {
		logger.Warn("committed runtime event observation failed",
			zap.Int("event_count", len(events)),
			zap.String("error", redactSensitiveString(observerCtx, err.Error())),
		)
	}
}

func eventSinkIsTransactionStore(sink EventSink, transactionStore TransactionStore) bool {
	if sink == nil || transactionStore == nil {
		return false
	}
	switch store := transactionStore.(type) {
	case *MemoryRuntimeStore:
		return sink == store || sink == store.events
	case *FileRuntimeStore:
		return sink == store || sink == store.events
	}
	return false
}

var _ TransactionStore = (*MemoryRuntimeStore)(nil)
var _ TransactionStore = (*FileRuntimeStore)(nil)
var _ RunDeleter = (*MemoryRuntimeStore)(nil)
var _ RunDeletionFencer = (*MemoryRuntimeStore)(nil)
var _ RunDeleter = (*FileRuntimeStore)(nil)
var _ RunDeletionFencer = (*FileRuntimeStore)(nil)
var _ ExecutionStore = (*MemoryRuntimeStore)(nil)
var _ CheckpointStore = (*MemoryRuntimeStore)(nil)
var _ EventSink = (*MemoryRuntimeStore)(nil)
var _ ExecutionStore = (*FileRuntimeStore)(nil)
var _ CheckpointStore = (*FileRuntimeStore)(nil)
var _ EventSink = (*FileRuntimeStore)(nil)
