package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

// MemoryExecutionStore keeps run and step records in process memory.
type MemoryExecutionStore struct {
	mu        sync.RWMutex
	runs      map[string]RunRecord
	steps     map[string]StepRecord
	deletions map[string]string
}

func NewMemoryExecutionStore() *MemoryExecutionStore {
	return &MemoryExecutionStore{
		runs:      map[string]RunRecord{},
		steps:     map[string]StepRecord{},
		deletions: map[string]string{},
	}
}

func (store *MemoryExecutionStore) CreateRun(ctx context.Context, run RunRecord) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", run.RunID); err != nil {
		return err
	}
	run = sanitizeRunRecord(ctx, run)
	if err := validateNewRunDeletion(run); err != nil {
		return err
	}
	if err := validateRunChildState(run); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureInitialized()
	if err := ensureMemoryRunWritable(store.deletions, run.RunID); err != nil {
		return err
	}
	if _, exists := store.runs[run.RunID]; exists {
		return fmt.Errorf("run %q already exists", run.RunID)
	}
	if run.ParentRunID != "" {
		if err := ensureMemoryRunWritable(store.deletions, run.ParentRunID); err != nil {
			return err
		}
		parent, exists := store.runs[run.ParentRunID]
		if !exists {
			return fmt.Errorf("parent run %q: %w", run.ParentRunID, ErrRunnerRecordNotFound)
		}
		if err := validateNewRunParent(run, parent); err != nil {
			return err
		}
	}
	store.runs[run.RunID] = cloneRunRecord(run)
	return nil
}

func (store *MemoryExecutionStore) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return RunRecord{}, err
	}
	if err := validateRunnerStorageID("run ID", run.RunID); err != nil {
		return RunRecord{}, err
	}
	run = sanitizeRunRecord(ctx, run)
	if err := validateRunChildState(run); err != nil {
		return RunRecord{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureInitialized()
	existing, exists := store.runs[run.RunID]
	if !exists {
		return RunRecord{}, ErrRunnerRecordNotFound
	}
	if existing.Revision != expectedRevision || run.Revision != expectedRevision {
		return RunRecord{}, &RunRevisionConflictError{RunID: run.RunID, Expected: expectedRevision, Actual: existing.Revision}
	}
	if err := validateRunDeletionTransition(ctx, existing, run); err != nil {
		return RunRecord{}, err
	}
	if err := validateRunExecutionLeaseTransition(ctx, existing, run); err != nil {
		return RunRecord{}, err
	}
	run.Revision = expectedRevision + 1
	store.runs[run.RunID] = cloneRunRecord(run)
	return cloneRunRecord(run), nil
}

func (store *MemoryExecutionStore) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return RunRecord{}, err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return RunRecord{}, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	run, exists := store.runs[runID]
	if !exists {
		return RunRecord{}, ErrRunnerRecordNotFound
	}
	return cloneRunRecord(run), nil
}

func (store *MemoryExecutionStore) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return nil, err
	}
	statuses := make(map[RunStatus]struct{}, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses[status] = struct{}{}
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	runs := make([]RunRecord, 0, len(store.runs))
	for _, run := range store.runs {
		if len(statuses) > 0 {
			if _, included := statuses[run.Status]; !included {
				continue
			}
		}
		if filter.ParentRunID != "" && run.ParentRunID != filter.ParentRunID {
			continue
		}
		if filter.ParentTaskID != "" && run.ParentTaskID != filter.ParentTaskID {
			continue
		}
		if filter.RootRunID != "" && run.RootRunID != filter.RootRunID {
			continue
		}
		if filter.Namespace != "" && run.Namespace != filter.Namespace {
			continue
		}
		runs = append(runs, cloneRunRecord(run))
	}
	sort.Slice(runs, func(leftIndex, rightIndex int) bool {
		if runs[leftIndex].StartedAt.Equal(runs[rightIndex].StartedAt) {
			return runs[leftIndex].RunID < runs[rightIndex].RunID
		}
		return runs[leftIndex].StartedAt.Before(runs[rightIndex].StartedAt)
	})
	return runs, nil
}

func (store *MemoryExecutionStore) AppendStep(ctx context.Context, step StepRecord) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", step.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
		return err
	}
	step = sanitizeStepRecord(ctx, step)
	if err := validateStepEffect(step); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureInitialized()
	run, exists := store.runs[step.RunID]
	if !exists {
		return ErrRunnerRecordNotFound
	}
	if err := ensureRunNotDeleting(run, "append a step"); err != nil {
		return err
	}
	if err := ensureMemoryRunWritable(store.deletions, step.RunID); err != nil {
		return err
	}
	if _, exists := store.steps[step.StepID]; exists {
		return fmt.Errorf("step %q already exists", step.StepID)
	}
	store.steps[step.StepID] = cloneStepRecord(step)
	return nil
}

func (store *MemoryExecutionStore) UpdateStep(ctx context.Context, step StepRecord) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", step.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
		return err
	}
	step = sanitizeStepRecord(ctx, step)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureInitialized()
	run, exists := store.runs[step.RunID]
	if !exists {
		return ErrRunnerRecordNotFound
	}
	if err := ensureRunNotDeleting(run, "update a step"); err != nil {
		return err
	}
	if err := ensureMemoryRunWritable(store.deletions, step.RunID); err != nil {
		return err
	}
	existing, exists := store.steps[step.StepID]
	if !exists || existing.RunID != step.RunID {
		return ErrRunnerRecordNotFound
	}
	if err := validateStepEffectTransition(existing, step); err != nil {
		return err
	}
	store.steps[step.StepID] = cloneStepRecord(step)
	return nil
}

func (store *MemoryExecutionStore) GetStep(ctx context.Context, stepID string) (StepRecord, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return StepRecord{}, err
	}
	if err := validateRunnerStorageID("step ID", stepID); err != nil {
		return StepRecord{}, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	step, exists := store.steps[stepID]
	if !exists {
		return StepRecord{}, ErrRunnerRecordNotFound
	}
	return cloneStepRecord(step), nil
}

func (store *MemoryExecutionStore) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	steps := make([]StepRecord, 0)
	for _, step := range store.steps {
		if step.RunID == runID {
			steps = append(steps, cloneStepRecord(step))
		}
	}
	sort.Slice(steps, func(leftIndex, rightIndex int) bool {
		if steps[leftIndex].StartedAt.Equal(steps[rightIndex].StartedAt) {
			return steps[leftIndex].StepID < steps[rightIndex].StepID
		}
		return steps[leftIndex].StartedAt.Before(steps[rightIndex].StartedAt)
	})
	return steps, nil
}

func (store *MemoryExecutionStore) DeleteRun(ctx context.Context, runID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureInitialized()
	if err := validateMemoryRunFenceDeletion(ctx, store.deletions, runID); err != nil {
		return err
	}
	run, exists := store.runs[runID]
	if !exists {
		return nil
	}
	if err := validateMemoryRunDeletion(ctx, runID, run.Deletion); err != nil {
		return err
	}
	delete(store.runs, runID)
	for stepID, step := range store.steps {
		if step.RunID == runID {
			delete(store.steps, stepID)
		}
	}
	return nil
}

func (store *MemoryExecutionStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureInitialized()
	return fenceMemoryRun(&store.deletions, runID, deletionID)
}

func (store *MemoryExecutionStore) ensureInitialized() {
	if store.runs == nil {
		store.runs = map[string]RunRecord{}
	}
	if store.steps == nil {
		store.steps = map[string]StepRecord{}
	}
	if store.deletions == nil {
		store.deletions = map[string]string{}
	}
}

// MemoryCheckpointStore keeps checkpoint metadata and payloads in process memory.
type MemoryCheckpointStore struct {
	mu          sync.RWMutex
	checkpoints map[string]memoryCheckpoint
	deletions   map[string]string
}

type memoryCheckpoint struct {
	record  CheckpointRecord
	payload []byte
}

func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{
		checkpoints: map[string]memoryCheckpoint{},
		deletions:   map[string]string{},
	}
}

func (store *MemoryCheckpointStore) Save(ctx context.Context, record CheckpointRecord, payload []byte) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", record.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("checkpoint ID", record.CheckpointID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.checkpoints == nil {
		store.checkpoints = map[string]memoryCheckpoint{}
	}
	if err := ensureMemoryRunWritable(store.deletions, record.RunID); err != nil {
		return err
	}
	if _, exists := store.checkpoints[record.CheckpointID]; exists {
		return fmt.Errorf("checkpoint %q already exists", record.CheckpointID)
	}
	cloned := record
	cloned.PayloadRef = ""
	store.checkpoints[record.CheckpointID] = memoryCheckpoint{record: cloned, payload: append([]byte(nil), payload...)}
	return nil
}

func (store *MemoryCheckpointStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return fenceMemoryRun(&store.deletions, runID, deletionID)
}

func (store *MemoryCheckpointStore) Load(ctx context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return CheckpointRecord{}, nil, err
	}
	if err := validateRunnerStorageID("checkpoint ID", checkpointID); err != nil {
		return CheckpointRecord{}, nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	checkpoint, exists := store.checkpoints[checkpointID]
	if !exists {
		return CheckpointRecord{}, nil, ErrRunnerRecordNotFound
	}
	return checkpoint.record, append([]byte(nil), checkpoint.payload...), nil
}

func (store *MemoryCheckpointStore) List(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	checkpoints := make([]CheckpointRecord, 0)
	for _, checkpoint := range store.checkpoints {
		if checkpoint.record.RunID == runID {
			checkpoints = append(checkpoints, checkpoint.record)
		}
	}
	sort.Slice(checkpoints, func(leftIndex, rightIndex int) bool {
		if checkpoints[leftIndex].CreatedAt.Equal(checkpoints[rightIndex].CreatedAt) {
			return checkpoints[leftIndex].CheckpointID < checkpoints[rightIndex].CheckpointID
		}
		return checkpoints[leftIndex].CreatedAt.Before(checkpoints[rightIndex].CreatedAt)
	})
	return checkpoints, nil
}

func (store *MemoryCheckpointStore) DeleteRun(ctx context.Context, runID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := validateMemoryRunFenceDeletion(ctx, store.deletions, runID); err != nil {
		return err
	}
	for checkpointID, checkpoint := range store.checkpoints {
		if checkpoint.record.RunID == runID {
			delete(store.checkpoints, checkpointID)
		}
	}
	return nil
}

// MemoryEventSink keeps non-streaming runtime events in process memory.
type MemoryEventSink struct {
	mu        sync.RWMutex
	events    map[string][]Event
	deletions map[string]string
}

func NewMemoryEventSink() *MemoryEventSink {
	return &MemoryEventSink{
		events:    map[string][]Event{},
		deletions: map[string]string{},
	}
}

func (sink *MemoryEventSink) Publish(ctx context.Context, event Event) error {
	return sink.PublishBatch(ctx, []Event{event})
}

func (sink *MemoryEventSink) PublishBatch(ctx context.Context, events []Event) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	events = sanitizeEvents(ctx, events)
	for _, event := range events {
		if IsStreamingEvent(event.Type) {
			continue
		}
		if err := validateRunnerStorageID("run ID", event.RunID); err != nil {
			return err
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.events == nil {
		sink.events = map[string][]Event{}
	}
	for _, event := range events {
		if IsStreamingEvent(event.Type) {
			continue
		}
		if err := ensureMemoryRunWritable(sink.deletions, event.RunID); err != nil {
			return err
		}
	}
	for _, event := range events {
		if !IsStreamingEvent(event.Type) {
			sink.events[event.RunID] = append(sink.events[event.RunID], cloneEvent(event))
		}
	}
	return nil
}

func (sink *MemoryEventSink) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return fenceMemoryRun(&sink.deletions, runID, deletionID)
}

func (sink *MemoryEventSink) ListEvents(runID string) ([]Event, error) {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	sink.mu.RLock()
	defer sink.mu.RUnlock()
	events := sink.events[runID]
	cloned := make([]Event, len(events))
	for index, event := range events {
		cloned[index] = cloneEvent(event)
	}
	return cloned, nil
}

func (sink *MemoryEventSink) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	events, err := sink.ListEvents(runID)
	if err != nil {
		return EventPage{}, err
	}
	return PaginateEventsNewestFirst(events, cursor, limit)
}

func (sink *MemoryEventSink) DeleteRun(ctx context.Context, runID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if err := validateMemoryRunFenceDeletion(ctx, sink.deletions, runID); err != nil {
		return err
	}
	delete(sink.events, runID)
	return nil
}

// MemoryArtifactStore keeps artifact metadata and payloads in process memory.
type MemoryArtifactStore struct {
	mu        sync.RWMutex
	artifacts map[string]Artifact
	stages    map[string]map[string]Artifact
	deletions map[string]string
}

func NewMemoryArtifactStore() *MemoryArtifactStore {
	return &MemoryArtifactStore{
		artifacts: map[string]Artifact{},
		stages:    map[string]map[string]Artifact{},
		deletions: map[string]string{},
	}
}

func (store *MemoryArtifactStore) Stage(ctx context.Context, transactionID string, artifact Artifact) (ArtifactStage, error) {
	stage := ArtifactStage{TransactionID: transactionID}
	if err := fileStoreContextErr(ctx); err != nil {
		return stage, err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return stage, err
	}
	if err := validateRunnerStorageID("run ID", artifact.RunID); err != nil {
		return stage, err
	}
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if err := validateRunnerStorageID("artifact ID", artifact.ID); err != nil {
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
	artifact = sanitizeArtifact(ctx, artifact)
	artifact.Location = "memory/" + artifact.RunID + "/" + artifact.ID
	stage.Ref = artifactRef(artifact)
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ensureMemoryRunWritable(store.deletions, artifact.RunID); err != nil {
		return stage, err
	}
	if store.stages == nil {
		store.stages = make(map[string]map[string]Artifact)
	}
	transactionStages := store.stages[transactionID]
	if transactionStages == nil {
		transactionStages = make(map[string]Artifact)
		store.stages[transactionID] = transactionStages
	}
	key := memoryArtifactKey(artifact.RunID, artifact.ID)
	if existing, exists := transactionStages[key]; exists {
		comparable := artifact
		comparable.CreatedAt = existing.CreatedAt
		if !artifactsEqual(existing, comparable) {
			return stage, fmt.Errorf("artifact stage %q payload mismatch", artifact.ID)
		}
		stage.Ref = artifactRef(existing)
		return stage, nil
	}
	transactionStages[key] = cloneArtifact(artifact)
	return stage, nil
}

func (store *MemoryArtifactStore) Finalize(ctx context.Context, transactionID string, stages []ArtifactStage) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.artifacts == nil {
		store.artifacts = make(map[string]Artifact)
	}
	transactionStages := store.stages[transactionID]
	for _, stage := range stages {
		if stage.TransactionID != transactionID {
			return fmt.Errorf("artifact stage transaction %q does not match %q", stage.TransactionID, transactionID)
		}
		key := memoryArtifactKey(stage.Ref.RunID, stage.Ref.ID)
		artifact, exists := transactionStages[key]
		if !exists {
			if _, finalized := store.artifacts[key]; finalized {
				continue
			}
			return fmt.Errorf("artifact stage %q: %w", stage.Ref.ID, ErrRunnerRecordNotFound)
		}
		if err := ensureMemoryRunWritable(store.deletions, artifact.RunID); err != nil {
			return err
		}
		if existing, exists := store.artifacts[key]; exists && !artifactsEqual(existing, artifact) {
			return fmt.Errorf("artifact %q already exists with different content", artifact.ID)
		}
		store.artifacts[key] = cloneArtifact(artifact)
		delete(transactionStages, key)
	}
	delete(store.stages, transactionID)
	return nil
}

func (store *MemoryArtifactStore) Discard(ctx context.Context, transactionID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.stages, transactionID)
	return nil
}

func (store *MemoryArtifactStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return fenceMemoryRun(&store.deletions, runID, deletionID)
}

func (store *MemoryArtifactStore) Load(ctx context.Context, ref state.ArtifactRef) (Artifact, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return Artifact{}, err
	}
	if err := validateRunnerStorageID("run ID", ref.RunID); err != nil {
		return Artifact{}, err
	}
	if err := validateRunnerStorageID("artifact ID", ref.ID); err != nil {
		return Artifact{}, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	artifact, exists := store.artifacts[memoryArtifactKey(ref.RunID, ref.ID)]
	if !exists {
		return Artifact{}, ErrRunnerRecordNotFound
	}
	return cloneArtifact(artifact), nil
}

func (store *MemoryArtifactStore) List(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	refs := make([]state.ArtifactRef, 0)
	for _, artifact := range store.artifacts {
		if artifact.RunID == runID {
			refs = append(refs, artifactRef(artifact))
		}
	}
	sort.Slice(refs, func(leftIndex, rightIndex int) bool {
		if refs[leftIndex].CreatedAt.Equal(refs[rightIndex].CreatedAt) {
			return refs[leftIndex].ID < refs[rightIndex].ID
		}
		return refs[leftIndex].CreatedAt.Before(refs[rightIndex].CreatedAt)
	})
	return refs, nil
}

func (store *MemoryArtifactStore) DeleteRun(ctx context.Context, runID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := validateMemoryRunFenceDeletion(ctx, store.deletions, runID); err != nil {
		return err
	}
	for key, artifact := range store.artifacts {
		if artifact.RunID == runID {
			delete(store.artifacts, key)
		}
	}
	for transactionID, stages := range store.stages {
		for key, artifact := range stages {
			if artifact.RunID == runID {
				delete(stages, key)
			}
		}
		if len(stages) == 0 {
			delete(store.stages, transactionID)
		}
	}
	return nil
}

func memoryArtifactKey(runID, artifactID string) string {
	return runID + "\x00" + artifactID
}

func fenceMemoryRun(fences *map[string]string, runID, deletionID string) error {
	if *fences == nil {
		*fences = map[string]string{}
	}
	existingID := (*fences)[runID]
	if existingID != "" && existingID != deletionID {
		return fmt.Errorf("%w: run %q is fenced by deletion %q, not %q", ErrRunControlNotAllowed, runID, existingID, deletionID)
	}
	(*fences)[runID] = deletionID
	return nil
}

func ensureMemoryRunWritable(fences map[string]string, runID string) error {
	if deletionID := fences[runID]; deletionID != "" {
		return fmt.Errorf("%w: run %q is fenced by deletion %q", ErrRunControlNotAllowed, runID, deletionID)
	}
	return nil
}

func validateMemoryRunDeletion(ctx context.Context, runID string, deletion *RunDeletionState) error {
	if deletion == nil {
		return fmt.Errorf("%w: run %q is not reserved for deletion", ErrRunControlNotAllowed, runID)
	}
	if err := validateRunDeletionState(deletion); err != nil {
		return fmt.Errorf("run %q deletion state: %w", runID, err)
	}
	return requireRunDeletionMutation(ctx, runID, deletion.ID)
}

func validateMemoryRunFenceDeletion(ctx context.Context, fences map[string]string, runID string) error {
	fenceID := fences[runID]
	if fenceID == "" {
		return fmt.Errorf("%w: run %q has no durable deletion fence", ErrRunControlNotAllowed, runID)
	}
	return requireRunDeletionMutation(ctx, runID, fenceID)
}

func artifactRef(artifact Artifact) state.ArtifactRef {
	return state.ArtifactRef{
		ID: artifact.ID, RunID: artifact.RunID, StepID: artifact.StepID, NodeID: artifact.NodeID,
		OperationKey: artifact.OperationKey,
		ParentRunID:  artifact.ParentRunID, ParentStepID: artifact.ParentStepID, ParentTaskID: artifact.ParentTaskID,
		RootRunID: artifact.RootRunID, RunPath: append([]string(nil), artifact.RunPath...), Namespace: artifact.Namespace,
		Type: artifact.Type, MIMEType: artifact.MIMEType, Location: artifact.Location, CreatedAt: artifact.CreatedAt,
	}
}

func artifactsEqual(left, right Artifact) bool {
	if !bytes.Equal(left.Data, right.Data) {
		return false
	}
	left.Data = nil
	right.Data = nil
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func cloneRunRecord(run RunRecord) RunRecord {
	cloned := run
	if run.ExecutionLease != nil {
		lease := *run.ExecutionLease
		cloned.ExecutionLease = &lease
	}
	cloned.RunPath = append([]string(nil), run.RunPath...)
	cloned.ChildRunIDs = append([]string(nil), run.ChildRunIDs...)
	cloned.PendingChildRuns = append([]PendingChildRun(nil), run.PendingChildRuns...)
	if run.Deletion != nil {
		deletion := *run.Deletion
		deletion.RunIDs = append([]string(nil), run.Deletion.RunIDs...)
		cloned.Deletion = &deletion
	}
	cloned.ReturnValue = cloneRunValue(run.ReturnValue)
	cloned.CurrentNodeIDs = append([]string(nil), run.CurrentNodeIDs...)
	cloned.CurrentStepIDs = append([]string(nil), run.CurrentStepIDs...)
	cloned.NextNodeIDs = append([]string(nil), run.NextNodeIDs...)
	if run.Origin != nil {
		origin := *run.Origin
		cloned.Origin = &origin
	}
	if run.FinishedAt != nil {
		finishedAt := *run.FinishedAt
		cloned.FinishedAt = &finishedAt
	}
	return cloned
}

func cloneRunValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var cloned any
	if err := decoder.Decode(&cloned); err != nil {
		return value
	}
	return cloned
}

func cloneStepRecord(step StepRecord) StepRecord {
	cloned := step
	cloned.RunPath = append([]string(nil), step.RunPath...)
	if step.EffectResolution != nil {
		resolution := *step.EffectResolution
		if step.EffectResolution.ResolvedAt != nil {
			resolvedAt := *step.EffectResolution.ResolvedAt
			resolution.ResolvedAt = &resolvedAt
		}
		cloned.EffectResolution = &resolution
	}
	if step.FinishedAt != nil {
		finishedAt := *step.FinishedAt
		cloned.FinishedAt = &finishedAt
	}
	return cloned
}

func cloneArtifact(artifact Artifact) Artifact {
	cloned := artifact
	cloned.RunPath = append([]string(nil), artifact.RunPath...)
	cloned.Data = append([]byte(nil), artifact.Data...)
	return cloned
}
