package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
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

type TransactionOutcome string

const (
	TransactionNotStarted     TransactionOutcome = "not_started"
	TransactionCommitted      TransactionOutcome = "committed"
	TransactionOutcomeUnknown TransactionOutcome = "outcome_unknown"
)

type ArtifactStage struct {
	TransactionID string            `json:"transaction_id"`
	Ref           state.ArtifactRef `json:"ref"`
}

type Commit struct {
	TransactionID string
	Lease         *ExecutionLeaseGuard
	Run           *RunWrite
	Steps         []StepWrite
	Checkpoints   []CheckpointWrite
	Events        []Event
	Artifacts     []ArtifactStage
}

type CommitResult struct {
	TransactionID string             `json:"transaction_id"`
	Outcome       TransactionOutcome `json:"outcome"`
	Run           *RunRecord         `json:"run,omitempty"`
	Artifacts     []ArtifactStage    `json:"artifacts,omitempty"`
}

type TransactionStore interface {
	Commit(ctx context.Context, commit Commit) (CommitResult, error)
	ResolveCommit(ctx context.Context, transactionID string) (CommitResult, error)
}

var ErrTransactionOutcomeUnknown = errors.New("runtime transaction outcome is unknown")

func ensureCommitTransactionID(commit Commit) Commit {
	if strings.TrimSpace(commit.TransactionID) == "" {
		commit.TransactionID = newRunnerID()
	}
	return commit
}

func commitFingerprint(commit Commit) (string, error) {
	commit.TransactionID = ""
	data, err := json.Marshal(commit)
	if err != nil {
		return "", fmt.Errorf("encode runtime transaction fingerprint: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func cloneCommitResult(result CommitResult) CommitResult {
	if result.Run != nil {
		run := cloneRunRecord(*result.Run)
		result.Run = &run
	}
	result.Artifacts = cloneArtifactStages(result.Artifacts)
	return result
}

func cloneArtifactStages(stages []ArtifactStage) []ArtifactStage {
	if len(stages) == 0 {
		return nil
	}
	cloned := make([]ArtifactStage, len(stages))
	for index, stage := range stages {
		cloned[index] = stage
		cloned[index].Ref = state.CloneArtifactRefs([]state.ArtifactRef{stage.Ref})[0]
	}
	return cloned
}

func commitAndResolve(ctx context.Context, store TransactionStore, commit Commit) (CommitResult, Commit, error) {
	commit = ensureCommitTransactionID(commit)
	result, commitErr := store.Commit(ctx, commit)
	if result.TransactionID == "" {
		result.TransactionID = commit.TransactionID
	}
	if result.Outcome == TransactionCommitted {
		return result, commit, nil
	}
	if result.Outcome == TransactionOutcomeUnknown {
		resolved, resolveErr := store.ResolveCommit(context.WithoutCancel(normalizeRunnerContext(ctx)), commit.TransactionID)
		if resolveErr == nil && resolved.Outcome == TransactionCommitted {
			return resolved, commit, nil
		}
		if resolveErr != nil {
			return result, commit, errors.Join(commitErr, fmt.Errorf("resolve runtime transaction %q: %w", commit.TransactionID, resolveErr), ErrTransactionOutcomeUnknown)
		}
		if resolved.Outcome == TransactionOutcomeUnknown {
			return resolved, commit, errors.Join(commitErr, ErrTransactionOutcomeUnknown)
		}
		if commitErr == nil {
			commitErr = fmt.Errorf("runtime transaction %q resolved to %q", commit.TransactionID, resolved.Outcome)
		}
		return resolved, commit, commitErr
	}
	if commitErr != nil {
		return result, commit, commitErr
	}
	return result, commit, fmt.Errorf("runtime transaction %q returned outcome %q without an error", commit.TransactionID, result.Outcome)
}

type MemoryRuntimeStore struct {
	execution     *MemoryExecutionStore
	checkpoints   *MemoryCheckpointStore
	events        *MemoryEventSink
	manifestMu    sync.Mutex
	manifests     map[string]RunDeletionManifest
	transactionMu sync.Mutex
	transactions  map[string]memoryTransactionResult
}

type memoryTransactionResult struct {
	Fingerprint string
	Result      CommitResult
}

func NewMemoryRuntimeStore() *MemoryRuntimeStore {
	return &MemoryRuntimeStore{
		execution:    NewMemoryExecutionStore(),
		checkpoints:  NewMemoryCheckpointStore(),
		events:       NewMemoryEventSink(),
		manifests:    make(map[string]RunDeletionManifest),
		transactions: make(map[string]memoryTransactionResult),
	}
}

func newMemoryRuntimeStore(execution *MemoryExecutionStore, checkpoints *MemoryCheckpointStore, events *MemoryEventSink) *MemoryRuntimeStore {
	if execution == nil || checkpoints == nil || events == nil {
		return nil
	}
	return &MemoryRuntimeStore{
		execution: execution, checkpoints: checkpoints, events: events,
		manifests: make(map[string]RunDeletionManifest), transactions: make(map[string]memoryTransactionResult),
	}
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

func (store *MemoryRuntimeStore) LoadRunDeletionManifest(ctx context.Context, deletionID string) (RunDeletionManifest, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return RunDeletionManifest{}, err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return RunDeletionManifest{}, err
	}
	store.manifestMu.Lock()
	defer store.manifestMu.Unlock()
	manifest, ok := store.manifests[deletionID]
	if !ok {
		return RunDeletionManifest{}, ErrRunnerRecordNotFound
	}
	manifest.RunIDs = append([]string(nil), manifest.RunIDs...)
	return manifest, nil
}

func (store *MemoryRuntimeStore) ListRunDeletionManifests(ctx context.Context) ([]RunDeletionManifest, error) {
	if err := fileStoreContextErr(ctx); err != nil {
		return nil, err
	}
	store.manifestMu.Lock()
	defer store.manifestMu.Unlock()
	manifests := make([]RunDeletionManifest, 0, len(store.manifests))
	for _, manifest := range store.manifests {
		manifest.RunIDs = append([]string(nil), manifest.RunIDs...)
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(left, right int) bool { return manifests[left].ID < manifests[right].ID })
	return manifests, nil
}

func (store *MemoryRuntimeStore) SaveRunDeletionManifest(ctx context.Context, manifest RunDeletionManifest) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := ValidateRunDeletionManifest(manifest); err != nil {
		return err
	}
	store.manifestMu.Lock()
	defer store.manifestMu.Unlock()
	if store.manifests == nil {
		store.manifests = make(map[string]RunDeletionManifest)
	}
	if existing, ok := store.manifests[manifest.ID]; ok {
		if err := validateRunDeletionManifestIdentity(existing, manifest); err != nil {
			return err
		}
	}
	manifest.RunIDs = append([]string(nil), manifest.RunIDs...)
	store.manifests[manifest.ID] = manifest
	return nil
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
	commit = ensureCommitTransactionID(commit)
	result := CommitResult{TransactionID: commit.TransactionID, Outcome: TransactionNotStarted}
	if store == nil || store.execution == nil || store.checkpoints == nil || store.events == nil {
		return result, errors.New("memory runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return result, err
	}
	commit = sanitizeCommit(ctx, commit)
	if err := validateRuntimeCommit(commit); err != nil {
		return result, err
	}
	fingerprint, err := commitFingerprint(commit)
	if err != nil {
		return result, err
	}
	store.transactionMu.Lock()
	defer store.transactionMu.Unlock()
	if existing, ok := store.transactions[commit.TransactionID]; ok {
		if existing.Fingerprint != fingerprint {
			return result, fmt.Errorf("runtime transaction %q fingerprint mismatch", commit.TransactionID)
		}
		return cloneCommitResult(existing.Result), nil
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

	applied, err := applyMemoryCommit(ctx, commit, runs, steps, checkpoints, events, store.execution.deletions)
	if err != nil {
		return result, err
	}
	store.execution.runs = runs
	store.execution.steps = steps
	store.checkpoints.checkpoints = checkpoints
	store.events.events = events
	result.Run = applied.Run
	result.Artifacts = cloneArtifactStages(commit.Artifacts)
	result.Outcome = TransactionCommitted
	if store.transactions == nil {
		store.transactions = make(map[string]memoryTransactionResult)
	}
	store.transactions[commit.TransactionID] = memoryTransactionResult{Fingerprint: fingerprint, Result: cloneCommitResult(result)}
	return result, nil
}

func (store *MemoryRuntimeStore) ResolveCommit(ctx context.Context, transactionID string) (CommitResult, error) {
	result := CommitResult{TransactionID: transactionID, Outcome: TransactionNotStarted}
	if store == nil {
		return result, errors.New("memory runtime store is nil")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return result, err
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return result, err
	}
	store.transactionMu.Lock()
	defer store.transactionMu.Unlock()
	stored, ok := store.transactions[transactionID]
	if !ok {
		return result, nil
	}
	return cloneCommitResult(stored.Result), nil
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
	if commit.Lease != nil {
		run, exists := runs[commit.Lease.RunID]
		if !exists {
			return CommitResult{}, ErrRunnerRecordNotFound
		}
		if err := validateExecutionLeaseGuard(run, *commit.Lease); err != nil {
			return CommitResult{}, err
		}
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
			if err := validateCommitExecutionLease(existing, commit); err != nil {
				return CommitResult{}, err
			}
			if !executionLeasesEqual(existing.ExecutionLease, run.ExecutionLease) {
				return CommitResult{}, fmt.Errorf("%w: runtime commit cannot change run %q execution lease", ErrRunControlNotAllowed, run.RunID)
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
		if err := validateCommitExecutionLease(run, commit); err != nil {
			return CommitResult{}, err
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
			if err := validateStepEffectTransition(existing, step); err != nil {
				return CommitResult{}, err
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
		if err := validateCommitExecutionLease(run, commit); err != nil {
			return CommitResult{}, err
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
		if err := validateCommitExecutionLease(run, commit); err != nil {
			return CommitResult{}, err
		}
		if err := ensureMemoryRunWritable(deletions, event.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := ensureRunNotDeleting(run, "publish an event"); err != nil {
			return CommitResult{}, err
		}
		events[event.RunID] = append(events[event.RunID], cloneEvent(event))
	}
	for _, stage := range commit.Artifacts {
		run, exists := runs[stage.Ref.RunID]
		if !exists {
			return CommitResult{}, ErrRunnerRecordNotFound
		}
		if err := validateCommitExecutionLease(run, commit); err != nil {
			return CommitResult{}, err
		}
		if err := ensureMemoryRunWritable(deletions, stage.Ref.RunID); err != nil {
			return CommitResult{}, err
		}
		if err := ensureRunNotDeleting(run, "finalize an artifact"); err != nil {
			return CommitResult{}, err
		}
	}
	return result, nil
}

func validateRuntimeCommit(commit Commit) error {
	if err := validateRunnerStorageID("transaction ID", commit.TransactionID); err != nil {
		return err
	}
	if commit.Lease != nil {
		if err := validateRunnerStorageID("execution lease guard run ID", commit.Lease.RunID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("execution lease guard token", commit.Lease.Token); err != nil {
			return err
		}
		if commit.Lease.Epoch == 0 {
			return errors.New("execution lease guard epoch must be greater than zero")
		}
		runID, err := singleCommitRunID(commit)
		if err != nil {
			return err
		}
		if runID != commit.Lease.RunID {
			return fmt.Errorf("execution lease guard run %q does not match commit run %q", commit.Lease.RunID, runID)
		}
	}
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
		if err := validateStepEffect(write.Step); err != nil {
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
	if err := validateArtifactStages(commit.TransactionID, commit.Artifacts); err != nil {
		return err
	}
	return nil
}

func validateStepEffect(step StepRecord) error {
	if step.OperationKey == "" {
		if step.EffectClass != "" || step.EffectStatus != "" {
			return fmt.Errorf("step %q effect metadata requires an operation key", step.StepID)
		}
		return nil
	}
	if err := validateRunnerStorageID("step operation key", step.OperationKey); err != nil {
		return err
	}
	if core.NormalizeEffectClass(step.EffectClass) != step.EffectClass {
		return fmt.Errorf("step %q has invalid effect class %q", step.StepID, step.EffectClass)
	}
	switch step.EffectStatus {
	case core.EffectIntent, core.EffectSucceeded, core.EffectFailed, core.EffectUnknown, core.EffectNotApplied, core.EffectCompensated:
	default:
		return fmt.Errorf("step %q has invalid effect status %q", step.StepID, step.EffectStatus)
	}
	return validateEffectResolution(step)
}

func validateArtifactStages(transactionID string, stages []ArtifactStage) error {
	for _, stage := range stages {
		if stage.TransactionID != transactionID {
			return fmt.Errorf("artifact stage transaction %q does not match transaction %q", stage.TransactionID, transactionID)
		}
		if err := validateRunnerStorageID("run ID", stage.Ref.RunID); err != nil {
			return err
		}
		if err := validateRunnerStorageID("artifact ID", stage.Ref.ID); err != nil {
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
	return nil, fmt.Errorf("custom runtime stores require WithRuntimeTransactionStore")
}

func firstTransactionalEventSink(sink EventSink) EventSink {
	if _, ok := sink.(TransactionStore); ok {
		return sink
	}
	switch typed := sink.(type) {
	case *MemoryRuntimeStore, *MemoryEventSink:
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
	}
	sinkStore, ok := sink.(TransactionStore)
	return ok && sinkStore == transactionStore
}

var _ TransactionStore = (*MemoryRuntimeStore)(nil)
var _ RunDeleter = (*MemoryRuntimeStore)(nil)
var _ RunDeletionFencer = (*MemoryRuntimeStore)(nil)
var _ RunDeletionManifestStore = (*MemoryRuntimeStore)(nil)
var _ ExecutionStore = (*MemoryRuntimeStore)(nil)
var _ CheckpointStore = (*MemoryRuntimeStore)(nil)
var _ EventSink = (*MemoryRuntimeStore)(nil)
