package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
	"go.uber.org/zap"
)

type runnerControlKind string

const (
	contractInputViewArtifactType   = "contract.input_view"
	contractOutputPatchArtifactType = "contract.output_patch"
	contractMergedStateArtifactType = "contract.merged_state"
)

var logger = zap.NewNop()

func SetLogger(l *zap.Logger) {
	logger = l
}

const (
	runnerControlPause  runnerControlKind = "pause"
	runnerControlCancel runnerControlKind = "cancel"
)

func controlRequestEventType(kind runnerControlKind) (EventType, error) {
	switch kind {
	case runnerControlPause:
		return EventRunPauseRequested, nil
	case runnerControlCancel:
		return EventRunCancelRequested, nil
	default:
		return "", fmt.Errorf("unsupported runner control %q", kind)
	}
}

type runnerPendingControl struct {
	kind         runnerControlKind
	taskID       string
	nodeID       string
	checkpointID string
	message      string
	hit          *state.BreakpointHit
}

type runnerActiveStep struct {
	step               StepRecord
	task               GraphTask
	attempts           int
	transactionID      string
	artifactSequence   int
	artifactStages     []ArtifactStage
	beforeCheckpointID string
	beforeInterrupted  bool
	lastError          error
}

type runnerCompletedStep struct {
	step              StepRecord
	afterCheckpointID string
}

type runnerStepTransition struct {
	writes       []StepWrite
	events       []Event
	steps        []StepRecord
	taskFailures []TaskFailureTransition
}

type runnerChildRun struct {
	runner *GraphRunner
	runID  string
}

const waveCheckpointNodeID = "__wave__"

type graphRunnerExecution struct {
	runner         *GraphRunner
	run            RunRecord
	skip           *breakpointSkip
	lastState      *state.State
	artifacts      []state.ArtifactRef
	active         map[string]*runnerActiveStep
	lastCompleted  *runnerCompletedStep
	completed      map[string]*runnerCompletedStep
	children       map[string]map[string]runnerChildRun
	waves          map[*state.State]string
	pending        *runnerPendingControl
	contractPolicy ContractPolicy
	nodeContracts  map[string]state.Contract
	patchRecorder  BranchPatchRecorder
	callbackErr    error
	cancelInvoke   context.CancelFunc
	mu             sync.Mutex
	nodeMu         sync.Mutex
	nodeLocks      map[string]*sync.Mutex
	nodeTaskLeases map[string]TaskLease
	runPersistMu   sync.Mutex
}

func newGraphRunnerExecution(runner *GraphRunner, run RunRecord, initialState *state.State, initialArtifacts []state.ArtifactRef, skip *breakpointSkip, cancelInvoke context.CancelFunc) *graphRunnerExecution {
	currentState := state.NewState()
	if initialState != nil {
		currentState = initialState.Clone()
	}
	return &graphRunnerExecution{
		runner:         runner,
		run:            run,
		skip:           skip,
		lastState:      currentState,
		artifacts:      state.CloneArtifactRefs(initialArtifacts),
		active:         map[string]*runnerActiveStep{},
		completed:      map[string]*runnerCompletedStep{},
		children:       map[string]map[string]runnerChildRun{},
		waves:          map[*state.State]string{},
		nodeLocks:      map[string]*sync.Mutex{},
		nodeTaskLeases: map[string]TaskLease{},
		contractPolicy: runner.effectiveContractPolicy(),
		nodeContracts:  cloneContracts(runner.nodeContracts),
		cancelInvoke:   cancelInvoke,
	}
}

func (e *graphRunnerExecution) lockForTask(taskID string) *sync.Mutex {
	e.nodeMu.Lock()
	defer e.nodeMu.Unlock()
	if e.nodeLocks == nil {
		e.nodeLocks = map[string]*sync.Mutex{}
	}
	if lock := e.nodeLocks[taskID]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	e.nodeLocks[taskID] = lock
	return lock
}

func (e *graphRunnerExecution) persistRun(ctx context.Context, update func(*RunRecord)) (RunRecord, error) {
	run, _, err := e.persistRunChecked(ctx, func(run *RunRecord) (bool, error) {
		if update != nil {
			update(run)
		}
		return true, nil
	})
	return run, err
}

func (e *graphRunnerExecution) persistRunChecked(ctx context.Context, update func(*RunRecord) (bool, error)) (RunRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	e.runPersistMu.Lock()
	defer e.runPersistMu.Unlock()

	revisionConflicts := 0
	for {
		e.mu.Lock()
		localRun := e.run
		e.mu.Unlock()
		run, err := e.runner.executionStore.GetRun(ctx, localRun.RunID)
		if err != nil {
			return RunRecord{}, false, err
		}
		if err := validateRunExecutionOwner(ctx, run); err != nil {
			return RunRecord{}, false, err
		}
		changed := run.PauseRequested != (run.PauseRequested || localRun.PauseRequested) ||
			run.CancelRequested != (run.CancelRequested || localRun.CancelRequested)
		run.PauseRequested = run.PauseRequested || localRun.PauseRequested
		run.CancelRequested = run.CancelRequested || localRun.CancelRequested
		if update != nil {
			updated, updateErr := update(&run)
			if updateErr != nil {
				return RunRecord{}, false, updateErr
			}
			changed = changed || updated
		}
		if !changed {
			e.mu.Lock()
			controlsChanged := e.run.PauseRequested && !run.PauseRequested || e.run.CancelRequested && !run.CancelRequested
			if !controlsChanged {
				e.run = run
				e.mu.Unlock()
				return run, false, nil
			}
			e.mu.Unlock()
			continue
		}
		commitResult, err := e.runner.commitRuntime(ctx, Commit{
			Run: &RunWrite{Mode: RunWriteUpdate, Run: run},
		})
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunRecord{}, false, runRevisionRetriesExceeded("persist run")
			}
			continue
		}
		if err != nil {
			return RunRecord{}, false, err
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}

		e.mu.Lock()
		controlsChanged := e.run.PauseRequested && !run.PauseRequested || e.run.CancelRequested && !run.CancelRequested
		if !controlsChanged {
			e.run = run
			e.mu.Unlock()
			return run, true, nil
		}
		e.mu.Unlock()
	}
}

func (e *graphRunnerExecution) controlPersistenceContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() == nil {
		return ctx
	}
	e.mu.Lock()
	controlled := e.run.PauseRequested || e.run.CancelRequested || e.pending != nil
	e.mu.Unlock()
	if controlled {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

func (e *graphRunnerExecution) persistControlRequest(ctx context.Context, kind runnerControlKind) (RunRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	e.runPersistMu.Lock()
	defer e.runPersistMu.Unlock()

	e.mu.Lock()
	runID := e.run.RunID
	guard, guarded := executionLeaseGuard(e.run)
	e.mu.Unlock()
	if !guarded {
		return RunRecord{}, fmt.Errorf("%w: active run %q has no execution lease", ErrExecutionLeaseLost, runID)
	}
	ctx = withExecutionLeaseGuard(ctx, guard)
	revisionConflicts := 0
	for {
		run, err := e.runner.executionStore.GetRun(ctx, runID)
		if err != nil {
			return RunRecord{}, err
		}
		if run.Status != "" && !isActiveDeleteRunStatus(run.Status) {
			return run, fmt.Errorf("%w: run %q status %q cannot be controlled", ErrRunControlNotAllowed, run.RunID, run.Status)
		}
		switch kind {
		case runnerControlPause:
			run.PauseRequested = true
		case runnerControlCancel:
			run.PauseRequested = false
			run.CancelRequested = true
		default:
			return RunRecord{}, fmt.Errorf("unsupported runner control %q", kind)
		}
		run.UpdatedAt = e.runner.currentTime()
		eventType, err := controlRequestEventType(kind)
		if err != nil {
			return RunRecord{}, err
		}
		event, err := e.runner.buildEvent(run, "", "", "", eventType, nil)
		if err != nil {
			return RunRecord{}, err
		}
		commitResult, err := e.runner.commitRuntime(ctx, Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
			Events: []Event{event},
		})
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunRecord{}, runRevisionRetriesExceeded("persist run control request")
			}
			continue
		}
		if err != nil {
			return RunRecord{}, err
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		e.mu.Lock()
		e.run = run
		e.mu.Unlock()
		return run, nil
	}
}

func (e *graphRunnerExecution) ExecuteNode(ctx context.Context, task GraphTask, executor core.Node, baseState *state.State, inputState *state.State) (result core.ExecutionResult, executionErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if executionErr != nil {
			e.recordTaskError(task.TaskID, executionErr)
		}
	}()
	taskLock := e.lockForTask(task.TaskID)
	taskLock.Lock()
	defer taskLock.Unlock()
	nodeCtx := core.NewContext(ctx)
	if task.Failure != nil {
		nodeCtx = core.NewContext(core.WithFailure(nodeCtx, core.FailureContext(*task.Failure)))
	}

	contract, hasContract := e.nodeContracts[task.NodeID]
	policy := e.contractPolicy
	if task.Dynamic {
		if issues := state.ValidateInputPatch(task.Input); len(issues) > 0 {
			return core.ExecutionResult{}, state.NewValidationError("send input", issues)
		}
	}
	if task.Dynamic && hasContract {
		if issues := state.ValidateAppliedInputPatchByContract(inputState, task.Input, contract); len(issues) > 0 {
			return core.ExecutionResult{}, state.NewValidationError("send input", issues)
		}
	}
	executionInput := state.ProjectStateByContract(inputState, state.Contract{WildcardRead: true})
	if hasContract && policy.Enabled() {
		validateInputs := policy.Mode != core.ContractValidationOff || policy.EnforceProjection
		if validateInputs {
			if issues := state.ValidateRequiredReads(executionInput, contract); len(issues) > 0 {
				violations := issuesToContractViolations(task.NodeID, issues)
				e.reportContractViolations(nodeCtx, task.TaskID, violations)
				if policy.Mode == core.ContractValidationStrict {
					return core.ExecutionResult{}, state.NewValidationError("node input", issues)
				}
			}
		}
		if policy.EnforceProjection {
			executionInput = state.ProjectStateByContract(executionInput, contract)
		}
	}
	isolatedInput, err := executionInput.CloneStrict()
	if err != nil {
		return core.ExecutionResult{}, core.NewExecutionError(core.ErrorInvalidInput, fmt.Sprintf("node input state cannot be safely cloned: %v", err), err, nil)
	}
	executionInput = isolatedInput
	if hasContract && policy.RecordArtifacts {
		e.recordContractStateArtifact(nodeCtx, task.NodeID, contractInputViewArtifactType, contract, executionInput)
	}

	if executor == nil {
		return core.ExecutionResult{}, fmt.Errorf("node %q is not executable", task.NodeID)
	}
	result, invokeErr := core.ExecuteNodeWithOptions(nodeCtx, inputState, executor, core.NodeExecutionOptions{
		Contract:          contractOption(contract, hasContract),
		InputState:        executionInput,
		ApplyPatchToInput: true,
		Reducers:          e.runner.reducers,
	})
	if invokeErr != nil {
		var interrupt *core.NodeInterrupt
		if errors.As(invokeErr, &interrupt) {
			e.markNodeInterrupt(nodeCtx, task, fmt.Sprint(interrupt.Value))
		}
		return core.ExecutionResult{}, invokeErr
	}
	if err := validateNodeResultDrafts(result.Node); err != nil {
		if acceptErr := acceptNodeAttempt(ctx); acceptErr != nil {
			return core.ExecutionResult{}, acceptErr
		}
		return core.ExecutionResult{}, err
	}
	if _, err := state.SnapshotFromState(result.State); err != nil {
		if acceptErr := acceptNodeAttempt(ctx); acceptErr != nil {
			return core.ExecutionResult{}, acceptErr
		}
		return core.ExecutionResult{}, core.NewExecutionError(core.ErrorNonRetryable, fmt.Sprintf("node result state cannot be checkpointed: %v", err), err, nil)
	}
	var patchView *state.State
	if hasContract && policy.RecordArtifacts {
		patchView = result.State
	}

	patch := result.Patch
	e.recordBranchPatch(baseState, task, patch)
	var writeViolations []core.ContractViolation
	if hasContract && policy.Enabled() {
		validateWrites := policy.Mode != core.ContractValidationOff || policy.EnforceWrites
		if validateWrites {
			if issues := state.ValidateAppliedPatchResultByContract(result.State, patch, contract); len(issues) > 0 {
				writeViolations = issuesToContractViolations(task.NodeID, issues)
				if policy.EnforceWrites || policy.Mode == core.ContractValidationStrict {
					if err := acceptNodeAttempt(ctx); err != nil {
						return core.ExecutionResult{}, err
					}
					e.recordNodeOutputObservations(nodeCtx, task, contract, patchView, nil, writeViolations)
					return core.ExecutionResult{}, state.NewValidationError("node output", issues)
				}
			}
		}
	}
	mergedState := result.State
	persistResult := func() error {
		e.recordNodeOutputObservations(nodeCtx, task, contract, patchView, mergedState, writeViolations)
		return e.recordNodeResultArtifacts(nodeCtx, result.Node.Artifacts)
	}
	completionCtx := ctx
	if lease, ok := e.nodeTaskLease(task.OperationID); ok {
		completionCtx = withTaskCompletion(ctx, lease)
	}
	if err := e.afterNode(completionCtx, task, baseState, mergedState, result.Node.Command, result.Node.Events, persistResult); err != nil {
		return core.ExecutionResult{}, err
	}
	return result, nil
}

func acceptNodeAttempt(ctx context.Context) error {
	attempt, ok := NodeAttemptFromContext(ctx)
	if !ok || attempt.TryAccept() {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return errors.New("node attempt was abandoned")
}

func (e *graphRunnerExecution) recordNodeOutputObservations(ctx context.Context, task GraphTask, contract state.Contract, patchView, mergedState *state.State, violations []core.ContractViolation) {
	if patchView != nil {
		e.recordContractStateArtifact(ctx, task.NodeID, contractOutputPatchArtifactType, contract, patchView)
	}
	if len(violations) > 0 {
		e.reportContractViolations(ctx, task.TaskID, violations)
	}
	if mergedState != nil && patchView != nil {
		e.recordContractStateArtifact(ctx, task.NodeID, contractMergedStateArtifactType, contract, mergedState)
	}
}

func validateNodeResultDrafts(result core.NodeResult) error {
	for index, send := range result.Command.Send {
		if _, err := json.Marshal(send.Input); err != nil {
			return fmt.Errorf("encode node result send %d input: %w", index, err)
		}
	}
	if result.Command.Suspend != nil {
		if _, err := json.Marshal(result.Command.Suspend.Value); err != nil {
			return fmt.Errorf("encode node result suspend value: %w", err)
		}
	}
	if result.Command.Return != nil {
		if _, err := json.Marshal(result.Command.Return.Value); err != nil {
			return fmt.Errorf("encode node result return value: %w", err)
		}
	}
	for _, draft := range result.Events {
		if strings.TrimSpace(draft.Type) == "" {
			return fmt.Errorf("node result event type is required")
		}
		if _, err := json.Marshal(draft.Payload); err != nil {
			return fmt.Errorf("encode node result event %q payload: %w", draft.Type, err)
		}
	}
	for _, draft := range result.Artifacts {
		if strings.TrimSpace(draft.Type) == "" {
			return fmt.Errorf("node result artifact type is required")
		}
	}
	return nil
}

func (e *graphRunnerExecution) recordNodeResultArtifacts(ctx context.Context, drafts []core.ArtifactDraft) error {
	for _, draft := range drafts {
		if _, err := SaveArtifact(ctx, Artifact{
			Type:     strings.TrimSpace(draft.Type),
			MIMEType: strings.TrimSpace(draft.MIMEType),
			Data:     append([]byte(nil), draft.Data...),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *graphRunnerExecution) PrepareNode(ctx context.Context, task GraphTask, currentState *state.State) (context.Context, error) {
	nodeCtx, err := e.beforeNode(ctx, task, currentState)
	if err != nil || e.runner.taskQueue == nil {
		return nodeCtx, err
	}
	metadata, ok := RunnerMetadataFromContext(nodeCtx)
	if !ok {
		return nodeCtx, errors.New("node task metadata is required")
	}
	now := e.runner.currentTime()
	queueTaskID := stableRuntimeID("task", metadata.RunID, task.OperationID)
	queued, enqueueErr := e.runner.taskQueue.GetTask(nodeCtx, queueTaskID)
	if enqueueErr != nil {
		return nodeCtx, enqueueErr
	}
	if queued.Status != TaskStatusQueued && queued.Status != TaskStatusRunning {
		return nodeCtx, fmt.Errorf("%w: node task %q status %q cannot start an attempt", ErrTaskConflict, queued.ID, queued.Status)
	}
	claimed, _, claimErr := e.runner.taskQueue.Claim(nodeCtx, WorkerIdentity{ID: e.runner.executionLeaseOwnerID()}, TaskClaimOptions{
		TaskID: queueTaskID, Kinds: []string{TaskKindGraphNode}, Now: now, TTL: e.runner.executionLeaseTTL(),
	})
	if claimErr != nil {
		return nodeCtx, claimErr
	}
	if claimed.ID != queueTaskID || claimed.Lease == nil {
		return nodeCtx, fmt.Errorf("claimed node task %q instead of %q", claimed.ID, queueTaskID)
	}
	e.mu.Lock()
	e.nodeTaskLeases[task.OperationID] = *claimed.Lease
	e.mu.Unlock()
	return nodeCtx, nil
}

func (e *graphRunnerExecution) nodeQueueTask(run RunRecord, step StepRecord, task GraphTask, now time.Time) Task {
	return Task{
		ID: stableRuntimeID("task", run.RunID, task.OperationID), Kind: TaskKindGraphNode,
		RunID: run.RunID, StepID: step.StepID, GraphTaskID: task.TaskID, OperationID: task.OperationID,
		GraphID: run.GraphID, GraphSessionID: run.GraphSessionID, CheckpointID: step.CheckpointBeforeID,
		Status: TaskStatusQueued, MaxAttempts: 100, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
}

func (e *graphRunnerExecution) OnTaskAttempt(ctx context.Context, task GraphTask, _ core.ExecutionResult, attemptErr error, retry bool) error {
	if e == nil || e.runner == nil || e.runner.taskQueue == nil {
		return nil
	}
	e.mu.Lock()
	lease, ok := e.nodeTaskLeases[task.OperationID]
	e.mu.Unlock()
	if !ok {
		queueTaskID := stableRuntimeID("task", e.run.RunID, task.OperationID)
		persisted, err := e.runner.taskQueue.GetTask(ctx, queueTaskID)
		if err == nil && persisted.Status == TaskStatusCompleted && attemptErr == nil {
			return nil
		}
		if errors.Is(err, ErrTaskNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("node task %q lease is missing with task %q status %q", task.OperationID, queueTaskID, persisted.Status)
	}
	if attemptErr == nil {
		persisted, err := e.runner.taskQueue.GetTask(context.WithoutCancel(ctx), lease.TaskID)
		if err == nil && persisted.Status == TaskStatusCompleted {
			e.deleteNodeTaskLease(task.OperationID, lease)
			return nil
		}
		_, err = e.runner.taskQueue.Complete(context.WithoutCancel(ctx), lease, TaskResult{})
		if err == nil {
			e.deleteNodeTaskLease(task.OperationID, lease)
		}
		return err
	}
	if !retry {
		var nodeInterrupt *core.NodeInterrupt
		var graphInterrupt *GraphInterrupt
		if !errors.As(attemptErr, &nodeInterrupt) && !errors.As(attemptErr, &graphInterrupt) {
			return nil
		}
	}
	_, err := e.runner.taskQueue.Fail(context.WithoutCancel(ctx), lease, TaskFailure{
		Message: attemptErr.Error(), Retryable: retry, RetryAt: e.runner.currentTime(),
	})
	if err == nil {
		e.deleteNodeTaskLease(task.OperationID, lease)
	}
	return err
}

func (e *graphRunnerExecution) deleteNodeTaskLease(operationID string, lease TaskLease) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, ok := e.nodeTaskLeases[operationID]
	if ok && current.Token == lease.Token && current.Epoch == lease.Epoch {
		delete(e.nodeTaskLeases, operationID)
	}
}

func (e *graphRunnerExecution) deleteNodeTaskFailureLeases(failures []TaskFailureTransition) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for operationID, current := range e.nodeTaskLeases {
		for _, failure := range failures {
			lease := failure.Lease
			if current.TaskID == lease.TaskID && current.Token == lease.Token && current.Epoch == lease.Epoch {
				delete(e.nodeTaskLeases, operationID)
				break
			}
		}
	}
}

func (e *graphRunnerExecution) nodeTaskLease(operationID string) (TaskLease, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	lease, ok := e.nodeTaskLeases[operationID]
	return lease, ok
}

func (e *graphRunnerExecution) heartbeatNodeTaskLeases(ctx context.Context, now time.Time, ttl time.Duration) error {
	if e == nil || e.runner == nil || e.runner.taskQueue == nil {
		return nil
	}
	e.mu.Lock()
	leases := make(map[string]TaskLease, len(e.nodeTaskLeases))
	for operationID, lease := range e.nodeTaskLeases {
		leases[operationID] = lease
	}
	e.mu.Unlock()
	for operationID, lease := range leases {
		updated, err := e.runner.taskQueue.Heartbeat(ctx, lease, now, ttl)
		if err != nil {
			return err
		}
		e.mu.Lock()
		current, exists := e.nodeTaskLeases[operationID]
		if exists && current.Token == lease.Token && current.Epoch == lease.Epoch {
			e.nodeTaskLeases[operationID] = updated
		}
		e.mu.Unlock()
	}
	return nil
}

func contractOption(contract state.Contract, ok bool) *state.Contract {
	if !ok {
		return nil
	}
	cloned := contract.Clone()
	return &cloned
}

func issuesToContractViolations(nodeID string, issues []state.ValidationIssue) []core.ContractViolation {
	if len(issues) == 0 {
		return nil
	}
	violations := make([]core.ContractViolation, len(issues))
	for i, issue := range issues {
		violations[i] = core.ContractViolation{
			NodeID:  nodeID,
			Path:    issue.Path,
			Kind:    issue.Kind,
			Message: issue.Message,
		}
	}
	return violations
}

type contractStateArtifact struct {
	NodeID   string                    `json:"node_id,omitempty"`
	Stage    string                    `json:"stage,omitempty"`
	Contract state.Contract            `json:"contract"`
	Summary  contractStateArtifactInfo `json:"summary"`
	Snapshot state.Snapshot            `json:"snapshot"`
}

type contractStateArtifactInfo struct {
	StateKeys   int `json:"state_keys"`
	StateScopes int `json:"state_scopes"`
}

func (e *graphRunnerExecution) recordContractStateArtifact(ctx context.Context, nodeID string, artifactType string, contract state.Contract, currentState *state.State) {
	if ctx == nil || strings.TrimSpace(nodeID) == "" || strings.TrimSpace(artifactType) == "" {
		return
	}
	observedState := projectContractObservationState(currentState, artifactType, contract)
	snapshot, err := state.SnapshotFromState(observedState)
	if err != nil {
		logger.Warn("contract state artifact snapshot failed",
			zap.String("node_id", nodeID),
			zap.String("artifact_type", artifactType),
			zap.Error(err),
		)
		return
	}
	payload := contractStateArtifact{
		NodeID:   nodeID,
		Stage:    contractArtifactStage(artifactType),
		Contract: contract,
		Summary: contractStateArtifactInfo{
			StateKeys:   state.CountKeys(observedState),
			StateScopes: state.CountScopes(observedState),
		},
		Snapshot: snapshot,
	}
	if _, err := SaveJSONArtifactBestEffort(ctx, artifactType, payload); err != nil {
		logger.Warn("contract state artifact recording failed",
			zap.String("node_id", nodeID),
			zap.String("artifact_type", artifactType),
			zap.Error(err),
		)
	}
}

func projectContractObservationState(current *state.State, artifactType string, contract state.Contract) *state.State {
	if contract.WildcardRead {
		return state.ProjectStateByContract(current, state.Contract{WildcardRead: true})
	}
	if contract.WildcardWrite && artifactType != contractInputViewArtifactType {
		return state.ProjectStateByContract(current, state.Contract{WildcardRead: true})
	}
	fields := make([]state.FieldAccess, 0, len(contract.Fields))
	seen := make(map[string]struct{}, len(contract.Fields))
	for _, field := range contract.Fields {
		if field.Path.Empty() {
			continue
		}
		path := field.Path.String()
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		fields = append(fields, state.FieldAccess{Path: field.Path, Mode: state.AccessRead})
	}
	return state.ProjectStateByContract(current, state.NewContract(fields...))
}

func contractArtifactStage(artifactType string) string {
	switch artifactType {
	case contractInputViewArtifactType:
		return "input_view"
	case contractOutputPatchArtifactType:
		return "output_patch"
	case contractMergedStateArtifactType:
		return "merged_state"
	default:
		return artifactType
	}
}

func (e *graphRunnerExecution) beforeNode(ctx context.Context, task GraphTask, currentState *state.State) (core.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return core.NewContext(ctx), err
	}

	e.runPersistMu.Lock()
	e.mu.Lock()
	run := e.run
	e.mu.Unlock()
	latestRun, err := e.runner.executionStore.GetRun(ctx, run.RunID)
	if err != nil {
		e.runPersistMu.Unlock()
		return core.NewContext(ctx), err
	}
	if err := validateNodeExecutionRun(run, latestRun); err != nil {
		e.runPersistMu.Unlock()
		return core.NewContext(ctx), err
	}
	latestRun.PauseRequested = latestRun.PauseRequested || run.PauseRequested
	latestRun.CancelRequested = latestRun.CancelRequested || run.CancelRequested
	run = latestRun
	e.mu.Lock()
	e.run = run
	e.mu.Unlock()
	e.runPersistMu.Unlock()

	e.mu.Lock()
	if e.run.CancelRequested {
		runID := e.run.RunID
		e.pending = &runnerPendingControl{kind: runnerControlCancel, taskID: task.TaskID, nodeID: task.NodeID}
		e.mu.Unlock()
		logger.Info("cancel interrupt requested",
			zap.String("run_id", runID),
			zap.String("task_id", task.TaskID),
			zap.String("node_id", task.NodeID),
		)
		e.recordBranchPatch(currentState, task, state.Patch{})
		return core.NewContext(ctx), &core.NodeInterrupt{NodeID: task.NodeID, Value: string(runnerControlCancel)}
	}

	active := e.active[task.TaskID]
	runID := e.run.RunID
	e.mu.Unlock()
	declaredOperation, _ := core.EffectOperationFromContext(ctx)
	declaredOperation.Class = core.NormalizeEffectClass(declaredOperation.Class)
	if active == nil || active.step.TaskID != task.TaskID {
		checkpointState := currentState.Clone()
		schedule, _, err := LoadGraphSchedule(currentState)
		if err != nil {
			return core.NewContext(ctx), fmt.Errorf("load graph schedule: %w", err)
		}
		if err := StoreGraphSchedule(checkpointState, GraphSchedule{
			CurrentTasks:      []GraphTask{task},
			NextTasks:         schedule.NextTasks,
			PendingFanInTasks: CloneGraphTasks(schedule.PendingFanInTasks),
		}); err != nil {
			return core.NewContext(ctx), err
		}

		stepID := newRunnerID()
		startedAt := e.runner.currentTime()
		var setupHit *state.BreakpointHit
		var skipBreakpoint bool
		e.runPersistMu.Lock()
		revisionConflicts := 0
		for {
			e.mu.Lock()
			localRun := e.run
			e.mu.Unlock()
			run, err = e.runner.executionStore.GetRun(ctx, localRun.RunID)
			if err != nil {
				e.runPersistMu.Unlock()
				return core.NewContext(ctx), err
			}
			if err := validateNodeExecutionRun(localRun, run); err != nil {
				e.runPersistMu.Unlock()
				return core.NewContext(ctx), err
			}
			run.PauseRequested = run.PauseRequested || localRun.PauseRequested
			run.CancelRequested = run.CancelRequested || localRun.CancelRequested
			if run.CancelRequested {
				e.mu.Lock()
				e.run = run
				e.pending = &runnerPendingControl{kind: runnerControlCancel, taskID: task.TaskID, nodeID: task.NodeID}
				e.mu.Unlock()
				e.runPersistMu.Unlock()
				logger.Info("cancel interrupt requested",
					zap.String("run_id", run.RunID),
					zap.String("task_id", task.TaskID),
					zap.String("node_id", task.NodeID),
				)
				e.recordBranchPatch(currentState, task, state.Patch{})
				return core.NewContext(ctx), &core.NodeInterrupt{NodeID: task.NodeID, Value: string(runnerControlCancel)}
			}

			step := StepRecord{
				StepID:      stepID,
				RunID:       runID,
				TaskID:      task.TaskID,
				ParentRunID: run.ParentRunID, ParentStepID: run.ParentStepID, ParentTaskID: run.ParentTaskID,
				RootRunID:    run.RootRunID,
				RunPath:      append([]string(nil), run.RunPath...),
				Namespace:    run.Namespace,
				NodeID:       task.NodeID,
				NodeName:     e.runner.nodeName(task.NodeID),
				OperationKey: stableRuntimeID("operation", run.RunID, task.OperationID, "node"),
				EffectClass:  declaredOperation.Class,
				EffectStatus: core.EffectIntent,
				Status:       StepStatusRunning,
				StartedAt:    startedAt,
				UpdatedAt:    e.runner.currentTime(),
				Attempt:      1,
			}
			run.CurrentNodeID = step.NodeID
			run.LastStepID = step.StepID
			run.UpdatedAt = e.runner.currentTime()

			setupHit = nil
			skipBreakpoint = false
			if !run.PauseRequested {
				setupHit, skipBreakpoint = e.runner.previewBreakpoint(step.NodeID, string(CheckpointBeforeNode), e.skip)
			}
			checkpointWrite, checkpointEvent, buildErr := e.runner.buildCheckpointWrite(ctx, run, step, task.NodeID, CheckpointBeforeNode, checkpointState, 0, setupHit, e.snapshotArtifacts())
			if buildErr != nil {
				e.runPersistMu.Unlock()
				return core.NewContext(ctx), buildErr
			}
			step.CheckpointBeforeID = checkpointWrite.Record.CheckpointID
			events := []Event{checkpointEvent}
			if !run.PauseRequested && setupHit == nil {
				startedEvent, buildErr := e.runner.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventNodeStarted, map[string]any{
					"node_name": step.NodeName, "operation_key": step.OperationKey, "effect_class": step.EffectClass,
				})
				if buildErr != nil {
					e.runPersistMu.Unlock()
					return core.NewContext(ctx), buildErr
				}
				events = append(events, startedEvent)
			}
			commit := Commit{
				Run:         &RunWrite{Mode: RunWriteUpdate, Run: run},
				Steps:       []StepWrite{{Mode: StepWriteAppend, Step: step}},
				Checkpoints: []CheckpointWrite{checkpointWrite},
				Events:      events,
			}
			var commitResult CommitResult
			var commitErr error
			if e.runner.taskQueue != nil {
				queue, ok := e.runner.taskQueue.(AtomicTaskQueue)
				if !ok {
					e.runPersistMu.Unlock()
					return core.NewContext(ctx), errors.New("node task enqueue requires an atomic task queue")
				}
				guardedCommit, guardErr := e.runner.guardExecutionCommit(ctx, commit)
				if guardErr != nil {
					e.runPersistMu.Unlock()
					return core.NewContext(ctx), guardErr
				}
				_, commitResult, commitErr = queue.EnqueueWithCommit(ctx, e.nodeQueueTask(run, step, task, startedAt), guardedCommit)
			} else {
				commitResult, commitErr = e.runner.commitRuntime(ctx, commit)
			}
			if errors.Is(commitErr, ErrRunRevisionConflict) {
				revisionConflicts++
				if revisionConflicts >= runRevisionRetryLimit {
					e.runPersistMu.Unlock()
					return core.NewContext(ctx), runRevisionRetriesExceeded("start node")
				}
				continue
			}
			if commitErr != nil {
				e.runPersistMu.Unlock()
				return core.NewContext(ctx), commitErr
			}
			if commitResult.Run != nil {
				run = *commitResult.Run
			}
			active = &runnerActiveStep{
				step:               step,
				task:               task,
				attempts:           1,
				transactionID:      stableRuntimeID("transaction", step.OperationKey, "result"),
				beforeCheckpointID: step.CheckpointBeforeID,
				beforeInterrupted:  run.PauseRequested || setupHit != nil,
			}
			e.mu.Lock()
			e.run = run
			e.active[task.TaskID] = active
			if skipBreakpoint && e.skip != nil {
				e.skip.Consumed = true
			}
			switch {
			case run.PauseRequested:
				e.pending = &runnerPendingControl{kind: runnerControlPause, taskID: task.TaskID, nodeID: task.NodeID}
			case setupHit != nil:
				e.pending = &runnerPendingControl{kind: runnerControlPause, taskID: task.TaskID, nodeID: task.NodeID, hit: setupHit}
			}
			e.mu.Unlock()
			break
		}
		e.runPersistMu.Unlock()
		logger.Debug("nodes scheduled", stepLogFields(ctx, active.step)...)
		if run.PauseRequested {
			e.recordBranchPatch(currentState, task, state.Patch{})
			logger.Info("pause interrupt requested", stepLogFields(ctx, active.step)...)
			return core.NewContext(ctx), &core.NodeInterrupt{NodeID: task.NodeID, Value: string(runnerControlPause)}
		}
		if setupHit != nil {
			e.recordBranchPatch(currentState, task, state.Patch{})
			fields := append(stepLogFields(ctx, active.step),
				zap.String("breakpoint_id", setupHit.BreakpointID),
				zap.String("breakpoint_stage", setupHit.Stage),
			)
			logger.Info("breakpoint hit before nodes", fields...)
			return core.NewContext(ctx), &core.NodeInterrupt{NodeID: task.NodeID, Value: setupHit}
		}
		logger.Info("nodes started", append(stepLogFields(ctx, active.step), state.SummaryFields(currentState)...)...)
	} else {
		e.mu.Lock()
		if active.attempts == 0 {
			active.attempts = 1
		} else {
			active.attempts++
		}
		active.lastError = nil
		active.artifactSequence = 0
		active.artifactStages = nil
		e.mu.Unlock()
		logStep := active.step
		logStep.Attempt = active.attempts
		logger.Warn("nodes retrying", stepLogFields(ctx, logStep)...)
	}

	e.mu.Lock()
	step := active.step
	logStep := step
	logStep.Attempt = active.attempts
	run = e.run
	e.mu.Unlock()

	stepID := step.StepID
	nodeID := step.NodeID
	taskID := step.TaskID
	runID = run.RunID
	attempt := logStep.Attempt
	operation := core.EffectOperation{
		Key: step.OperationKey, Kind: "node", Name: nodeID, Class: step.EffectClass,
		Status: core.EffectIntent, Attempt: attempt, IdempotencyKey: step.OperationKey,
	}
	nodeCtx := core.WithEffectOperation(ctx, operation)
	nodeCtx = core.WithEffectJournal(nodeCtx, core.EffectJournalFunc(e.recordEffect))
	nodeCtx = WithRunnerEventPublisher(nodeCtx, func(eventType EventType, payload any) error {
		return e.runner.publishEventWithTask(ctx, run, stepID, taskID, nodeID, eventType, payload)
	})
	if task.Failure != nil {
		nodeCtx = core.WithFailure(nodeCtx, core.FailureContext(*task.Failure))
	}
	nodeCtx = WithRunnerMetadata(nodeCtx, RunnerMetadata{
		RunID: runID, StepID: stepID, TaskID: taskID, NodeID: nodeID,
		ParentRunID: run.ParentRunID, ParentStepID: run.ParentStepID, ParentTaskID: run.ParentTaskID,
		RootRunID: run.RootRunID, RunPath: append([]string(nil), run.RunPath...), Namespace: run.Namespace,
		Attempt: attempt,
	})
	nodeCtx = WithGraphRunner(nodeCtx, e.runner)
	nodeCtx = WithChildRunController(nodeCtx, e)
	nodeCtx = WithRunnerArtifactRecorder(nodeCtx, func(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
		transactionID, artifactID, err := e.nextArtifactStageIdentity(task.TaskID, artifact.Type)
		if err != nil {
			return state.ArtifactRef{}, err
		}
		if artifact.ID == "" {
			if operation, ok := core.EffectOperationFromContext(ctx); ok && operation.Key != "" && operation.Key != step.OperationKey {
				artifactID = stableRuntimeID("artifact", transactionID, operation.Key, strings.TrimSpace(artifact.Type))
			}
			artifact.ID = artifactID
		}
		stage, err := e.runner.recordArtifact(ctx, transactionID, artifact)
		if err != nil {
			return state.ArtifactRef{}, err
		}
		if stage.Ref.ID == "" {
			return state.ArtifactRef{}, nil
		}
		if err := e.appendArtifactStage(task.TaskID, stage); err != nil {
			return state.ArtifactRef{}, err
		}
		return stage.Ref, nil
	})
	return withRunnerEventContext(nodeCtx, e.runner, runID, stepID, nodeID), nil
}

func validateNodeExecutionRun(localRun, persistedRun RunRecord) error {
	if persistedRun.Deletion != nil {
		return fmt.Errorf("%w: run %q is reserved for deletion", ErrRunControlNotAllowed, persistedRun.RunID)
	}
	switch persistedRun.Status {
	case RunStatusPending, RunStatusRunning:
	case RunStatusPaused, RunStatusCompleted, RunStatusFailed, RunStatusCanceled:
		return fmt.Errorf("%w: run %q status %q cannot execute a node", ErrRunControlNotAllowed, persistedRun.RunID, persistedRun.Status)
	default:
		return fmt.Errorf("%w: run %q has unsupported status %q", ErrRunControlNotAllowed, persistedRun.RunID, persistedRun.Status)
	}
	if !executionLeaseIdentitiesEqual(localRun.ExecutionLease, persistedRun.ExecutionLease) {
		return fmt.Errorf("%w: run %q execution lease changed", ErrExecutionLeaseLost, persistedRun.RunID)
	}
	return nil
}

func (e *graphRunnerExecution) OnSchedulerEvent(ctx context.Context, event SchedulerEvent) error {
	if e == nil {
		return nil
	}
	run := e.currentRun()
	eventType := EventWarning
	switch event.Type {
	case SchedulerEventLimitExceeded:
		eventType = EventRunLimitExceeded
	case SchedulerEventRetryScheduled:
		eventType = EventNodeRetry
	case SchedulerEventConditionFailed:
		eventType = EventConditionFailed
	case SchedulerEventRouteDecision:
		eventType = EventConditionEvaluated
	case SchedulerEventFailureRouted:
		eventType = EventFailureRouted
	case SchedulerEventBackpressure:
		eventType = EventRunBackpressure
	}
	stepID := ""
	if active := e.firstActiveStep(event.NodeID); active != nil {
		stepID = active.step.StepID
	}
	return e.runner.publishEvent(context.WithoutCancel(normalizeRunnerContext(ctx)), run, stepID, event.NodeID, eventType, event.Payload)
}

func (e *graphRunnerExecution) OnFailureRouted(ctx context.Context, source GraphTask, cause error, next []GraphTask) error {
	if e == nil || cause == nil || len(next) == 0 {
		return nil
	}
	ctx = e.controlPersistenceContext(ctx)
	e.mu.Lock()
	active := e.active[source.TaskID]
	completed := e.completed[source.TaskID]
	run := e.run
	e.mu.Unlock()

	var step StepRecord
	markFailed := active != nil
	if markFailed {
		step = active.step
	} else if completed != nil {
		step = completed.step
	}
	failure := next[0].Failure
	payload := map[string]any{
		"source_task_id": source.TaskID,
		"source_node_id": source.NodeID,
		"next_node_ids":  GraphTaskNodeIDs(next),
	}
	if failure != nil {
		payload["stage"] = failure.Stage
		payload["error_class"] = failure.ErrorClass
		payload["error"] = failure.Error
		payload["details"] = failure.Details
	}
	e.runPersistMu.Lock()
	defer e.runPersistMu.Unlock()
	revisionConflicts := 0
	for {
		latestRun, err := e.runner.executionStore.GetRun(ctx, run.RunID)
		if err != nil {
			return err
		}
		if err := validateRunExecutionOwner(ctx, latestRun); err != nil {
			return err
		}
		latestRun.PauseRequested = latestRun.PauseRequested || run.PauseRequested
		latestRun.CancelRequested = latestRun.CancelRequested || run.CancelRequested
		latestRun.UpdatedAt = e.runner.currentTime()

		events := make([]Event, 0, 2)
		stepWrites := make([]StepWrite, 0, 1)
		if markFailed {
			now := e.runner.currentTime()
			step.Attempt = active.attempts
			step.Status = StepStatusFailed
			step.EffectStatus = effectFailureStatus(step.EffectClass)
			step.ErrorCode = string(core.ClassifyError(cause))
			step.ErrorMessage = cause.Error()
			step.FinishedAt = &now
			step.UpdatedAt = now
			failedEvent, buildErr := e.runner.buildEvent(latestRun, step.StepID, step.TaskID, step.NodeID, EventNodeFailed, map[string]any{
				"error":       cause.Error(),
				"error_class": core.ClassifyError(cause),
				"attempt":     active.attempts,
			})
			if buildErr != nil {
				return buildErr
			}
			events = append(events, failedEvent)
			stepWrites = append(stepWrites, StepWrite{Mode: StepWriteUpdate, Step: step})
		}
		routedEvent, buildErr := e.runner.buildEvent(latestRun, step.StepID, source.TaskID, source.NodeID, EventFailureRouted, payload)
		if buildErr != nil {
			return buildErr
		}
		events = append(events, routedEvent)
		commitCtx := ctx
		var failedLease TaskLease
		if markFailed {
			if lease, ok := e.nodeTaskLease(source.OperationID); ok {
				failedLease = lease
				commitCtx = withTaskFailures(ctx, []TaskFailureTransition{{
					Lease:   lease,
					Failure: TaskFailure{Message: cause.Error()},
				}})
			}
		}
		commitResult, commitErr := e.runner.commitRuntime(commitCtx, Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: latestRun},
			Steps:  stepWrites,
			Events: events,
		})
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("record failure route")
			}
			continue
		}
		if commitErr != nil {
			return commitErr
		}
		if failedLease.TaskID != "" {
			e.deleteNodeTaskLease(source.OperationID, failedLease)
		}
		if commitResult.Run != nil {
			latestRun = *commitResult.Run
		}
		e.mu.Lock()
		e.run = latestRun
		if markFailed {
			e.completed[source.TaskID] = &runnerCompletedStep{step: step}
			delete(e.active, source.TaskID)
		}
		e.mu.Unlock()
		return nil
	}
}

func (e *graphRunnerExecution) firstActiveStep(identifier string) *runnerActiveStep {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.firstActiveStepLocked(identifier)
}

func (e *graphRunnerExecution) OnGraphStep(ctx context.Context, completedTasks []GraphTask, currentState *state.State) error {
	if e == nil || len(completedTasks) == 0 || currentState == nil {
		return nil
	}
	ctx = e.controlPersistenceContext(ctx)
	e.mu.Lock()
	if e.pending != nil && e.pending.taskID != "" {
		if active := e.active[e.pending.taskID]; active != nil && active.beforeInterrupted {
			e.mu.Unlock()
			return nil
		}
	}
	steps := make([]StepRecord, 0, len(completedTasks))
	stepIDs := make([]string, 0, len(completedTasks))
	for _, task := range completedTasks {
		completed := e.completed[task.TaskID]
		if completed == nil {
			e.mu.Unlock()
			return fmt.Errorf("wave checkpoint missing completed task %q for node %q", task.TaskID, task.NodeID)
		}
		steps = append(steps, completed.step)
		stepIDs = append(stepIDs, completed.step.StepID)
	}
	waveID := e.waveIDForTasksLocked(completedTasks)
	for index := range steps {
		steps[index].WaveID = waveID
	}
	nodeIDs := GraphTaskNodeIDs(completedTasks)
	schedule, _, err := LoadGraphSchedule(currentState)
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("load graph schedule: %w", err)
	}
	nextTasks := CloneGraphTasks(schedule.NextTasks)
	nextNodeIDs := GraphTaskNodeIDs(nextTasks)
	e.mu.Unlock()

	checkpointState := currentState.Clone()
	if err := StoreGraphSchedule(checkpointState, GraphSchedule{
		NextTasks:         nextTasks,
		PendingFanInTasks: CloneGraphTasks(schedule.PendingFanInTasks),
	}); err != nil {
		return err
	}

	e.runPersistMu.Lock()
	defer e.runPersistMu.Unlock()
	var barrierRun RunRecord
	var barrierID string
	revisionConflicts := 0
	for {
		e.mu.Lock()
		localRun := e.run
		e.mu.Unlock()
		latestRun, err := e.runner.executionStore.GetRun(ctx, localRun.RunID)
		if err != nil {
			return err
		}
		if err := validateRunExecutionOwner(ctx, latestRun); err != nil {
			return err
		}
		latestRun.PauseRequested = latestRun.PauseRequested || localRun.PauseRequested
		latestRun.CancelRequested = latestRun.CancelRequested || localRun.CancelRequested
		checkpointRun := latestRun
		checkpointRun.CurrentNodeIDs = append([]string(nil), nodeIDs...)
		checkpointRun.CurrentStepIDs = append([]string(nil), stepIDs...)
		checkpointRun.NextNodeIDs = append([]string(nil), nextNodeIDs...)
		checkpointRun.ParallelWaveID = waveID
		checkpointWrite, checkpointEvent, buildErr := e.runner.buildCheckpointWrite(ctx, checkpointRun, StepRecord{}, waveCheckpointNodeID, CheckpointAfterWave, checkpointState, 0, nil, e.snapshotArtifacts())
		if buildErr != nil {
			return buildErr
		}
		barrierID = checkpointWrite.Record.CheckpointID
		barrierRun = latestRun
		barrierRun.LastCheckpointID = barrierID
		barrierRun.CurrentNodeIDs = nil
		barrierRun.CurrentStepIDs = nil
		barrierRun.NextNodeIDs = append([]string(nil), nextNodeIDs...)
		barrierRun.ParallelWaveID = ""
		barrierRun.UpdatedAt = e.runner.currentTime()
		stepWrites := make([]StepWrite, 0, len(steps))
		for _, step := range steps {
			stepWrites = append(stepWrites, StepWrite{Mode: StepWriteUpdate, Step: step})
		}
		commitResult, commitErr := e.runner.commitRuntime(ctx, Commit{
			Run:         &RunWrite{Mode: RunWriteUpdate, Run: barrierRun},
			Steps:       stepWrites,
			Checkpoints: []CheckpointWrite{checkpointWrite},
			Events:      []Event{checkpointEvent},
		})
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("save graph step checkpoint")
			}
			continue
		}
		if commitErr != nil {
			return commitErr
		}
		if commitResult.Run != nil {
			barrierRun = *commitResult.Run
		}
		break
	}

	e.mu.Lock()
	e.run = barrierRun
	e.lastState = checkpointState.Clone()
	for _, step := range steps {
		if completed := e.completed[step.TaskID]; completed != nil && completed.step.StepID == step.StepID {
			completed.step = step
		}
		if e.lastCompleted != nil && e.lastCompleted.step.StepID == step.StepID {
			e.lastCompleted.step = step
		}
	}
	var control *runnerPendingControl
	switch {
	case barrierRun.CancelRequested:
		control = &runnerPendingControl{kind: runnerControlCancel, nodeID: waveCheckpointNodeID, checkpointID: barrierID}
	case barrierRun.PauseRequested:
		control = &runnerPendingControl{kind: runnerControlPause, nodeID: waveCheckpointNodeID, checkpointID: barrierID}
	}
	if control != nil {
		e.pending = control
		if e.cancelInvoke != nil {
			e.cancelInvoke()
		}
	}
	e.mu.Unlock()
	if control != nil {
		return &GraphInterrupt{
			NodeID:    waveCheckpointNodeID,
			State:     checkpointState,
			Value:     string(control.kind),
			NextTasks: nextTasks,
		}
	}
	return nil
}

func (e *graphRunnerExecution) OnParallelWave(ctx context.Context, base *state.State, tasks []GraphTask) error {
	if e == nil || base == nil || len(tasks) <= 1 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = e.controlPersistenceContext(ctx)
	e.mu.Lock()
	waveID := e.waves[base]
	if waveID == "" {
		waveID = newRunnerID()
		e.waves[base] = waveID
	}
	steps := make([]StepRecord, 0, len(tasks))
	for _, task := range tasks {
		completed := e.completed[task.TaskID]
		if completed == nil {
			continue
		}
		step := completed.step
		step.WaveID = waveID
		steps = append(steps, step)
	}
	e.mu.Unlock()

	e.runPersistMu.Lock()
	var run RunRecord
	revisionConflicts := 0
	for {
		e.mu.Lock()
		localRun := e.run
		e.mu.Unlock()
		var err error
		run, err = e.runner.executionStore.GetRun(ctx, localRun.RunID)
		if err != nil {
			e.runPersistMu.Unlock()
			return err
		}
		if err := validateRunExecutionOwner(ctx, run); err != nil {
			e.runPersistMu.Unlock()
			return err
		}
		run.PauseRequested = run.PauseRequested || localRun.PauseRequested
		run.CancelRequested = run.CancelRequested || localRun.CancelRequested
		run.ParallelWaveID = waveID
		run.CurrentNodeIDs = GraphTaskNodeIDs(tasks)
		run.UpdatedAt = e.runner.currentTime()
		stepWrites := make([]StepWrite, 0, len(steps))
		for _, step := range steps {
			stepWrites = append(stepWrites, StepWrite{Mode: StepWriteUpdate, Step: step})
		}
		commitResult, commitErr := e.runner.commitRuntime(ctx, Commit{
			Run:   &RunWrite{Mode: RunWriteUpdate, Run: run},
			Steps: stepWrites,
		})
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				e.runPersistMu.Unlock()
				return runRevisionRetriesExceeded("update parallel wave")
			}
			continue
		}
		if commitErr != nil {
			e.runPersistMu.Unlock()
			return commitErr
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		break
	}
	e.runPersistMu.Unlock()
	e.mu.Lock()
	e.run = run
	for _, step := range steps {
		if completed := e.completed[step.TaskID]; completed != nil && completed.step.StepID == step.StepID {
			completed.step = step
		}
	}
	e.mu.Unlock()
	return nil
}

func (e *graphRunnerExecution) SetBranchPatchRecorder(recorder BranchPatchRecorder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.patchRecorder = recorder
}

func (e *graphRunnerExecution) recordBranchPatch(base *state.State, task GraphTask, patch state.Patch) {
	e.mu.Lock()
	recorder := e.patchRecorder
	e.mu.Unlock()
	if recorder != nil {
		recorder.RecordBranchPatch(base, task, patch)
	}
}

func (e *graphRunnerExecution) recordTaskError(taskID string, taskErr error) {
	if e == nil || taskErr == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if active := e.active[taskID]; active != nil {
		active.lastError = taskErr
	}
}

func (e *graphRunnerExecution) OnTaskError(task GraphTask, taskErr error) {
	e.recordTaskError(task.TaskID, taskErr)
	e.mu.Lock()
	active := e.active[task.TaskID]
	transactionID := ""
	if active != nil {
		transactionID = active.transactionID
		active.artifactStages = nil
	}
	e.mu.Unlock()
	if transactionID != "" {
		if err := e.runner.artifactStore.Discard(context.Background(), transactionID); err != nil {
			logger.Warn("discard failed node artifact stages", zap.String("transaction_id", transactionID), zap.Error(err))
		}
	}
}

func (e *graphRunnerExecution) recordEffect(ctx context.Context, operation core.EffectOperation) error {
	if strings.TrimSpace(operation.Key) == "" {
		return errors.New("effect operation key is required")
	}
	metadata, ok := RunnerMetadataFromContext(ctx)
	if !ok || metadata.RunID == "" || metadata.TaskID == "" {
		return errors.New("effect operation runner metadata is required")
	}
	eventType := EventEffectOutcome
	if operation.Status == core.EffectIntent {
		eventType = EventEffectIntent
	}
	transactionID := stableRuntimeID("effect", operation.Key, string(operation.Status), fmt.Sprintf("%d", operation.Attempt))
	eventID := stableRuntimeID("effect-event", operation.Key, string(operation.Status), fmt.Sprintf("%d", operation.Attempt))
	for retry := 0; retry < runRevisionRetryLimit; retry++ {
		run, err := e.runner.executionStore.GetRun(ctx, metadata.RunID)
		if err != nil {
			return err
		}
		if err := validateRunExecutionOwner(ctx, run); err != nil {
			return err
		}
		event, err := e.runner.buildEvent(run, metadata.StepID, metadata.TaskID, metadata.NodeID, eventType, operation)
		if err != nil {
			return err
		}
		event.ID = eventID
		event.OperationKey = operation.Key
		_, err = e.runner.commitRuntime(ctx, Commit{
			TransactionID: transactionID,
			Run:           &RunWrite{Mode: RunWriteCheck, Run: run},
			Events:        []Event{event},
		})
		if errors.Is(err, ErrRunRevisionConflict) {
			continue
		}
		return err
	}
	return runRevisionRetriesExceeded("record effect operation")
}

func (e *graphRunnerExecution) isActiveAttempt(taskID, stepID string, attempts int) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	active := e.active[taskID]
	return active != nil &&
		active.step.StepID == stepID &&
		active.attempts == attempts &&
		!active.beforeInterrupted &&
		!e.run.CancelRequested &&
		isActiveDeleteRunStatus(e.run.Status)
}

func (e *graphRunnerExecution) afterNode(ctx context.Context, task GraphTask, beforeState *state.State, currentState *state.State, command core.Command, eventDrafts []core.EventDraft, persistResult func() error) error {
	ctx = e.controlPersistenceContext(ctx)
	e.mu.Lock()
	active := e.active[task.TaskID]
	if active == nil {
		e.mu.Unlock()
		return nil
	}
	if active.beforeInterrupted {
		e.mu.Unlock()
		return nil
	}

	step := active.step
	attempts := active.attempts
	before := beforeState.Clone()
	e.mu.Unlock()
	changes, err := e.runner.computeStateDiff(before, currentState)
	if err != nil {
		return err
	}

	e.runPersistMu.Lock()
	defer e.runPersistMu.Unlock()
	var run RunRecord
	var afterID string
	revisionConflicts := 0
	for {
		if !e.isActiveAttempt(task.TaskID, step.StepID, attempts) {
			return nil
		}
		e.mu.Lock()
		localRun := e.run
		e.mu.Unlock()
		run, err = e.runner.executionStore.GetRun(ctx, localRun.RunID)
		if err != nil {
			return err
		}
		if err := validateRunExecutionOwner(ctx, run); err != nil {
			return err
		}
		run.PauseRequested = run.PauseRequested || localRun.PauseRequested
		run.CancelRequested = run.CancelRequested || localRun.CancelRequested
		if !isActiveDeleteRunStatus(run.Status) || run.CancelRequested {
			return nil
		}
		if err := acceptNodeAttempt(ctx); err != nil {
			return err
		}
		if persistResult != nil {
			if err := persistResult(); err != nil {
				return err
			}
			persistResult = nil
		}
		artifactStages := e.snapshotArtifactStages(task.TaskID)
		checkpointArtifacts := e.snapshotArtifacts()
		for _, stage := range artifactStages {
			if stage.Ref.ID != "" {
				checkpointArtifacts = append(checkpointArtifacts, stage.Ref)
			}
		}

		checkpointState := currentState.Clone()
		if buildErr := storeAfterNodeCommand(checkpointState, task, command); buildErr != nil {
			return buildErr
		}
		checkpointWrite, checkpointEvent, buildErr := e.runner.buildCheckpointWrite(ctx, run, step, task.NodeID, CheckpointAfterNode, checkpointState, attempts, nil, checkpointArtifacts)
		if buildErr != nil {
			return buildErr
		}
		afterID = checkpointWrite.Record.CheckpointID
		now := e.runner.currentTime()
		step.Attempt = attempts
		step.Status = StepStatusSucceeded
		step.EffectStatus = core.EffectSucceeded
		step.CheckpointAfterID = afterID
		step.FinishedAt = &now
		step.UpdatedAt = now
		if task.ParallelWaveSize <= 1 {
			run.LastCheckpointID = afterID
		}
		run.UpdatedAt = now

		events := []Event{checkpointEvent}
		if len(changes) > 0 {
			stateEvent, buildErr := e.runner.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventStateChanged, map[string]any{"changes": changes})
			if buildErr != nil {
				return buildErr
			}
			events = append(events, stateEvent)
		}
		for _, draft := range eventDrafts {
			draftEvent, buildErr := e.runner.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventType(strings.TrimSpace(draft.Type)), draft.Payload)
			if buildErr != nil {
				return buildErr
			}
			events = append(events, draftEvent)
		}
		finishedEvent, buildErr := e.runner.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventNodeFinished, map[string]any{
			"attempt": attempts, "operation_key": step.OperationKey, "effect_class": step.EffectClass, "effect_status": step.EffectStatus,
		})
		if buildErr != nil {
			return buildErr
		}
		events = append(events, finishedEvent)
		commitResult, commitErr := e.runner.commitRuntime(ctx, Commit{
			TransactionID: active.transactionID,
			Run:           &RunWrite{Mode: RunWriteUpdate, Run: run},
			Steps:         []StepWrite{{Mode: StepWriteUpdate, Step: step}},
			Checkpoints:   []CheckpointWrite{checkpointWrite},
			Events:        events,
			Artifacts:     artifactStages,
		})
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("finish node")
			}
			continue
		}
		if commitErr != nil {
			return commitErr
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		for _, stage := range artifactStages {
			e.appendArtifact(stage.Ref)
		}
		break
	}
	fields := append(stepLogFields(ctx, step),
		zap.String("checkpoint_after_id", afterID),
	)
	fields = append(fields, state.SummaryFields(currentState)...)
	logger.Info("nodes completed", fields...)

	e.mu.Lock()
	e.run = run
	e.lastState = currentState.Clone()
	e.lastCompleted = &runnerCompletedStep{
		step:              step,
		afterCheckpointID: afterID,
	}
	e.completed[task.TaskID] = &runnerCompletedStep{
		step:              step,
		afterCheckpointID: afterID,
	}
	delete(e.active, task.TaskID)
	if e.pending != nil && e.pending.taskID == task.TaskID {
		e.pending = nil
	}
	e.mu.Unlock()
	return nil
}

func (e *graphRunnerExecution) waveIDForTasksLocked(tasks []GraphTask) string {
	for _, task := range tasks {
		if completed := e.completed[task.TaskID]; completed != nil && completed.step.WaveID != "" {
			return completed.step.WaveID
		}
	}
	return newRunnerID()
}

func (e *graphRunnerExecution) reportContractViolations(ctx context.Context, taskID string, violations []core.ContractViolation) {
	e.mu.Lock()
	run := e.run
	var step StepRecord
	if active := e.active[taskID]; active != nil {
		step = active.step
	}
	e.mu.Unlock()
	e.reportContractViolationsWithRun(ctx, run, step, violations)
}

func (e *graphRunnerExecution) reportContractViolationsWithRun(ctx context.Context, run RunRecord, step StepRecord, violations []core.ContractViolation) {
	if len(violations) == 0 {
		return
	}
	for _, v := range violations {
		logger.Warn("state contract violation",
			zap.String("node_id", v.NodeID),
			zap.String("path", v.Path),
			zap.String("kind", v.Kind),
			zap.String("message", v.Message),
		)
	}
	e.runner.publishBestEffortEvent(ctx, run, step.StepID, step.NodeID, EventContractViolation, map[string]any{
		"violations": violations,
	})
}

func (e *graphRunnerExecution) prepareFailedSteps(err error) (runnerStepTransition, error) {
	run, items := e.consumeActiveSteps()
	transition := runnerStepTransition{
		writes: make([]StepWrite, 0, len(items)),
		events: make([]Event, 0, len(items)),
		steps:  make([]StepRecord, 0, len(items)),
	}
	for _, item := range items {
		step := item.step
		if step.Status == StepStatusSucceeded || step.Status == StepStatusPaused || step.Status == StepStatusCanceled {
			continue
		}
		attempts := item.attempts
		stepErr := item.lastError
		if stepErr == nil {
			stepErr = err
		}
		now := e.runner.currentTime()
		step.Attempt = attempts
		step.Status = StepStatusFailed
		step.EffectStatus = effectFailureStatus(step.EffectClass)
		step.ErrorCode = string(core.ClassifyError(stepErr))
		if step.ErrorCode == string(core.ErrorUnknown) {
			step.ErrorCode = "node_failed"
		}
		step.ErrorMessage = stepErr.Error()
		step.FinishedAt = &now
		step.UpdatedAt = now
		failedEvent, buildErr := e.runner.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventNodeFailed, map[string]any{
			"error":         stepErr.Error(),
			"error_class":   core.ClassifyError(stepErr),
			"attempt":       attempts,
			"operation_key": step.OperationKey,
			"effect_class":  step.EffectClass,
			"effect_status": step.EffectStatus,
		})
		if buildErr != nil {
			return runnerStepTransition{}, buildErr
		}
		transition.writes = append(transition.writes, StepWrite{Mode: StepWriteUpdate, Step: step})
		transition.events = append(transition.events, failedEvent)
		transition.steps = append(transition.steps, step)
		if lease, ok := e.nodeTaskLease(item.task.OperationID); ok {
			transition.taskFailures = append(transition.taskFailures, TaskFailureTransition{
				Lease:   lease,
				Failure: TaskFailure{Message: stepErr.Error()},
			})
		}
	}
	return transition, nil
}

func (e *graphRunnerExecution) prepareCanceledSteps() (runnerStepTransition, error) {
	if e == nil {
		return runnerStepTransition{}, nil
	}
	run, items := e.consumeActiveSteps()
	transition := runnerStepTransition{
		writes: make([]StepWrite, 0, len(items)),
		events: make([]Event, 0, len(items)),
		steps:  make([]StepRecord, 0, len(items)),
	}
	for _, item := range items {
		step := item.step
		if step.Status == StepStatusSucceeded || step.Status == StepStatusPaused || step.Status == StepStatusCanceled {
			continue
		}
		attempts := item.attempts
		now := e.runner.currentTime()
		step.Attempt = attempts
		step.Status = StepStatusCanceled
		step.EffectStatus = effectFailureStatus(step.EffectClass)
		step.ErrorCode = "run_canceled"
		step.ErrorMessage = "run canceled"
		step.FinishedAt = &now
		step.UpdatedAt = now
		canceledEvent, buildErr := e.runner.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventNodeCanceled, map[string]any{
			"attempt":    attempts,
			"error_code": "run_canceled",
			"message":    "run canceled",
		})
		if buildErr != nil {
			return runnerStepTransition{}, buildErr
		}
		transition.writes = append(transition.writes, StepWrite{Mode: StepWriteUpdate, Step: step})
		transition.events = append(transition.events, canceledEvent)
		transition.steps = append(transition.steps, step)
	}
	return transition, nil
}

func effectFailureStatus(class core.EffectClass) core.EffectStatus {
	if core.IsWriteEffect(class) {
		return core.EffectUnknown
	}
	return core.EffectFailed
}

func (e *graphRunnerExecution) consumeActiveSteps() (RunRecord, []runnerActiveStep) {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := make([]runnerActiveStep, 0, len(e.active))
	for taskID, active := range e.active {
		if active == nil {
			continue
		}
		items = append(items, *active)
		delete(e.active, taskID)
	}
	e.pending = nil
	sort.Slice(items, func(leftIndex, rightIndex int) bool {
		left := items[leftIndex].step
		right := items[rightIndex].step
		if left.TaskID == right.TaskID {
			return left.StepID < right.StepID
		}
		return left.TaskID < right.TaskID
	})
	return e.run, items
}

func (e *graphRunnerExecution) currentRun() RunRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run
}

func (e *graphRunnerExecution) stateOrFallback(currentState *state.State) *state.State {
	if currentState != nil {
		return currentState
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastState.Clone()
}

func (e *graphRunnerExecution) consumePendingControl() (*runnerPendingControl, *runnerActiveStep) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil {
		return nil, nil
	}

	control := *e.pending
	e.pending = nil

	var activeCopy *runnerActiveStep
	if control.taskID != "" {
		activeCopy = e.firstActiveStepLocked(control.taskID)
	}
	if activeCopy == nil && control.nodeID != "" {
		activeCopy = e.firstActiveStepLocked(control.nodeID)
	}
	if activeCopy == nil {
		activeCopy = e.firstActiveStepLocked("")
	}
	if activeCopy != nil {
		copyStep := *activeCopy
		activeCopy = &copyStep
	}
	return &control, activeCopy
}

func (e *graphRunnerExecution) restorePendingControl(control *runnerPendingControl) {
	if e == nil || control == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil {
		copyControl := *control
		e.pending = &copyControl
	}
}

func (e *graphRunnerExecution) requestCancel() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.run.PauseRequested = false
	e.run.CancelRequested = true
	taskID := ""
	nodeID := e.run.CurrentNodeID
	for activeTaskID, active := range e.active {
		if active == nil {
			continue
		}
		taskID = activeTaskID
		nodeID = active.step.NodeID
		break
	}
	e.pending = &runnerPendingControl{kind: runnerControlCancel, taskID: taskID, nodeID: nodeID}
	children := e.childRunsLocked()
	cancel := e.cancelInvoke
	e.mu.Unlock()
	for _, child := range children {
		_ = child.runner.Cancel(context.Background(), child.runID)
	}
	if cancel != nil {
		cancel()
	}
}

func (e *graphRunnerExecution) requestPause() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.run.PauseRequested = true
	if e.run.CancelRequested {
		e.mu.Unlock()
		return
	}
	var cancel context.CancelFunc
	if len(e.active) == 1 {
		for taskID, active := range e.active {
			if active == nil {
				continue
			}
			if e.pending == nil {
				e.pending = &runnerPendingControl{kind: runnerControlPause, taskID: taskID, nodeID: active.step.NodeID, message: "pause requested"}
			}
			cancel = e.cancelInvoke
		}
	}
	children := e.childRunsLocked()
	e.mu.Unlock()
	for _, child := range children {
		_ = child.runner.Pause(context.Background(), child.runID)
	}
	if cancel != nil {
		cancel()
	}
}

func (e *graphRunnerExecution) RegisterChildRun(taskID string, runner *GraphRunner, runID string) {
	if e == nil || runner == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(runID) == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.children == nil {
		e.children = map[string]map[string]runnerChildRun{}
	}
	if e.children[taskID] == nil {
		e.children[taskID] = map[string]runnerChildRun{}
	}
	e.children[taskID][runID] = runnerChildRun{runner: runner, runID: runID}
}

func (e *graphRunnerExecution) UnregisterChildRun(taskID, runID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	children := e.children[taskID]
	delete(children, runID)
	if len(children) == 0 {
		delete(e.children, taskID)
	}
}

func (e *graphRunnerExecution) ReserveChildRun(ctx context.Context, parentRunID string, proposed PendingChildRun) (PendingChildRun, error) {
	if e == nil || strings.TrimSpace(parentRunID) == "" {
		return PendingChildRun{}, fmt.Errorf("parent run ID is required")
	}
	e.mu.Lock()
	actualParentRunID := e.run.RunID
	e.mu.Unlock()
	if actualParentRunID != parentRunID {
		return PendingChildRun{}, fmt.Errorf("child run parent mismatch: execution=%q request=%q", actualParentRunID, parentRunID)
	}
	var reservation PendingChildRun
	_, _, err := e.persistRunChecked(ctx, func(run *RunRecord) (bool, error) {
		var changed bool
		var reserveErr error
		reservation, changed, reserveErr = reservePendingChildRun(run, proposed, e.runner.currentTime())
		return changed, reserveErr
	})
	return reservation, err
}

func (e *graphRunnerExecution) FinalizeChildRun(ctx context.Context, parentRunID, requestKey, childRunID string) error {
	if e == nil || strings.TrimSpace(parentRunID) == "" {
		return fmt.Errorf("parent run ID is required")
	}
	e.mu.Lock()
	actualParentRunID := e.run.RunID
	e.mu.Unlock()
	if actualParentRunID != parentRunID {
		return fmt.Errorf("child run parent mismatch: execution=%q request=%q", actualParentRunID, parentRunID)
	}
	_, _, err := e.persistRunChecked(ctx, func(run *RunRecord) (bool, error) {
		return finalizePendingChildRun(run, requestKey, childRunID, e.runner.currentTime())
	})
	return err
}

func (e *graphRunnerExecution) childRunsLocked() []runnerChildRun {
	children := make([]runnerChildRun, 0)
	for _, byRunID := range e.children {
		for _, child := range byRunID {
			children = append(children, child)
		}
	}
	return children
}

func (e *graphRunnerExecution) consumeLastCompleted(identifier string) *runnerCompletedStep {
	e.mu.Lock()
	defer e.mu.Unlock()
	var completed *runnerCompletedStep
	completedTaskID := ""
	if identifier != "" {
		if item := e.completed[identifier]; item != nil {
			completed = item
			completedTaskID = identifier
		} else {
			for taskID, item := range e.completed {
				if item != nil && item.step.NodeID == identifier {
					completed = item
					completedTaskID = taskID
					break
				}
			}
		}
	} else {
		completed = e.lastCompleted
		if completed != nil {
			completedTaskID = completed.step.TaskID
		}
	}
	if completed == nil {
		return nil
	}
	copyCompleted := *completed
	delete(e.completed, completedTaskID)
	if e.lastCompleted != nil && e.lastCompleted.step.StepID == completed.step.StepID {
		e.lastCompleted = nil
	}
	return &copyCompleted
}

func (e *graphRunnerExecution) appendArtifact(ref state.ArtifactRef) {
	if ref.ID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifacts = append(e.artifacts, ref)
}

func (e *graphRunnerExecution) nextArtifactStageIdentity(taskID, artifactType string) (string, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := e.active[taskID]
	if active == nil || active.transactionID == "" {
		return "", "", errors.New("node artifact transaction is unavailable")
	}
	active.artifactSequence++
	artifactID := stableRuntimeID("artifact", active.transactionID, fmt.Sprintf("%d", active.artifactSequence), strings.TrimSpace(artifactType))
	return active.transactionID, artifactID, nil
}

func (e *graphRunnerExecution) appendArtifactStage(taskID string, stage ArtifactStage) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := e.active[taskID]
	if active == nil || active.transactionID != stage.TransactionID {
		return errors.New("node artifact transaction changed while staging")
	}
	active.artifactStages = append(active.artifactStages, stage)
	return nil
}

func (e *graphRunnerExecution) snapshotArtifactStages(taskID string) []ArtifactStage {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := e.active[taskID]
	if active == nil {
		return nil
	}
	return cloneArtifactStages(active.artifactStages)
}

func (e *graphRunnerExecution) snapshotArtifacts() []state.ArtifactRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return state.CloneArtifactRefs(e.artifacts)
}

func (e *graphRunnerExecution) afterInterruptNodes() ([]string, error) {
	graph := e.runner.runnerGraph()
	if graph == nil {
		return nil, errors.New("graph runner graph is nil")
	}
	return graph.AfterInterruptNodes(e.runner.breakpoints)
}

func (e *graphRunnerExecution) markNodeInterrupt(ctx context.Context, task GraphTask, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := e.active[task.TaskID]
	if active == nil {
		return
	}
	/// make sure the nodes resume at the same nodes after restart
	active.beforeInterrupted = true
	e.pending = &runnerPendingControl{kind: runnerControlPause, taskID: task.TaskID, nodeID: task.NodeID, message: message}
	logStep := active.step
	logStep.Attempt = active.attempts
	logger.Info("nodes interrupt captured", stepLogFields(ctx, logStep)...)
}

func (e *graphRunnerExecution) firstActiveStepLocked(identifier string) *runnerActiveStep {
	if e == nil || len(e.active) == 0 {
		return nil
	}
	if identifier != "" {
		if active := e.active[identifier]; active != nil {
			return active
		}
		for _, active := range e.active {
			if active != nil && active.step.NodeID == identifier {
				return active
			}
		}
		return nil
	}
	for _, active := range e.active {
		return active
	}
	return nil
}

func (e *graphRunnerExecution) recordCallbackError(err error) {
	if e == nil || err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.callbackErr == nil {
		e.callbackErr = err
	}
}

func (e *graphRunnerExecution) callbackError() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.callbackErr
}
