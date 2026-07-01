package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
	"sort"
	"strings"
	"sync"

	langgraph "github.com/smallnest/langgraphgo/graph"
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

type runnerPendingControl struct {
	kind         runnerControlKind
	nodeID       string
	checkpointID string
	message      string
	hit          *state.BreakpointHit
}

type runnerActiveStep struct {
	step               StepRecord
	attempts           int
	beforeCheckpointID string
	beforeInterrupted  bool
}

type runnerCompletedStep struct {
	step              StepRecord
	afterCheckpointID string
}

const parallelBarrierNodeID = "__parallel_barrier__"

type graphRunnerExecution struct {
	runner         *GraphRunner
	run            RunRecord
	skip           *breakpointSkip
	lastState      *state.State
	artifacts      []state.ArtifactRef
	active         map[string]*runnerActiveStep
	lastCompleted  *runnerCompletedStep
	completed      map[string]*runnerCompletedStep
	waves          map[*state.State]string
	pending        *runnerPendingControl
	contractPolicy ContractPolicy
	nodeContracts  map[string]state.Contract
	patchRecorder  BranchPatchRecorder
	callbackErr    error
	cancelInvoke   context.CancelFunc
	mu             sync.Mutex
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
		waves:          map[*state.State]string{},
		contractPolicy: runner.contractPolicy(),
		nodeContracts:  runner.NodeContracts,
		cancelInvoke:   cancelInvoke,
	}
}

func (e *graphRunnerExecution) ExecuteNode(ctx context.Context, nodeID string, executor RunnerNode, currentState *state.State) (*state.State, error) {
	nodeCtx, err := e.beforeNode(ctx, nodeID, currentState)
	if err != nil {
		return currentState, err
	}

	contract, hasContract := e.nodeContracts[nodeID]
	policy := e.contractPolicy
	inputState := currentState.Clone()
	if hasContract && policy.Enabled() {
		validateInputs := policy.Mode != core.ContractValidationOff || policy.EnforceProjection
		if validateInputs {
			if issues := state.ValidateRequiredReads(currentState, contract); len(issues) > 0 {
				violations := issuesToContractViolations(nodeID, issues)
				e.reportContractViolations(nodeCtx, nodeID, violations)
				if policy.Mode == core.ContractValidationStrict {
					return currentState, fmt.Errorf("%s", violations[0].Message)
				}
			}
		}
		if policy.EnforceProjection {
			inputState = state.ProjectStateByContract(currentState, contract)
		}
		if policy.RecordArtifacts {
			e.recordContractStateArtifact(nodeCtx, nodeID, contractInputViewArtifactType, contract, inputState)
		}
	}

	if executor == nil {
		return currentState, fmt.Errorf("node %q is not executable", nodeID)
	}
	result, invokeErr := core.ExecuteNodeWithOptions(nodeCtx, currentState, executor, core.NodeExecutionOptions{
		Registry:          e.runner.stateRegistry(),
		Contract:          contractOption(contract, hasContract),
		InputState:        inputState,
		ApplyPatchToInput: hasContract && policy.EnforceProjection,
	})
	if invokeErr != nil {
		var interrupt *langgraph.NodeInterrupt
		if errors.As(invokeErr, &interrupt) {
			e.markNodeInterrupt(nodeID, fmt.Sprint(interrupt.Value))
		}
		return currentState, invokeErr
	}
	if hasContract && policy.RecordArtifacts {
		patchView, err := result.Patch.Apply(inputState)
		if err != nil {
			return currentState, err
		}
		e.recordContractStateArtifact(nodeCtx, nodeID, contractOutputPatchArtifactType, contract, patchView)
	}

	patch := result.Patch
	e.recordBranchPatch(currentState, nodeID, patch)
	if hasContract && policy.Enabled() {
		validateWrites := policy.Mode != core.ContractValidationOff || policy.EnforceWrites
		if validateWrites {
			if issues := state.ValidatePatchByContract(patch, contract); len(issues) > 0 {
				violations := issuesToContractViolations(nodeID, issues)
				e.reportContractViolations(nodeCtx, nodeID, violations)
				if policy.EnforceWrites || policy.Mode == core.ContractValidationStrict {
					return currentState, fmt.Errorf("%s", violations[0].Message)
				}
			}
		}
	}
	mergedState := result.State
	if hasContract && policy.RecordArtifacts {
		e.recordContractStateArtifact(nodeCtx, nodeID, contractMergedStateArtifactType, contract, mergedState)
	}
	if err := e.afterNode(ctx, nodeID, currentState, mergedState); err != nil {
		return currentState, err
	}
	return mergedState, nil
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
	Snapshot state.StateSnapshot       `json:"snapshot"`
}

type contractStateArtifactInfo struct {
	StateKeys            int `json:"state_keys"`
	StateScopes          int `json:"state_scopes"`
	ConversationMessages int `json:"conversation_messages"`
}

func (e *graphRunnerExecution) recordContractStateArtifact(ctx context.Context, nodeID string, artifactType string, contract state.Contract, currentState *state.State) {
	if ctx == nil || strings.TrimSpace(nodeID) == "" || strings.TrimSpace(artifactType) == "" {
		return
	}
	snapshot, err := state.SnapshotFromState(currentState)
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
			StateKeys:            state.CountKeys(currentState),
			StateScopes:          state.CountScopes(currentState),
			ConversationMessages: state.CountConversationMessages(currentState),
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

func (e *graphRunnerExecution) beforeNode(ctx context.Context, nodeID string, currentState *state.State) (core.Context, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return core.NewContext(ctx), err
	}

	if latestRun, err := e.runner.ExecutionStore.GetRun(ctx, e.run.RunID); err == nil {
		e.run = latestRun
	}

	if e.run.CancelRequested {
		logger.Info("cancel interrupt requested",
			zap.String("run_id", e.run.RunID),
			zap.String("node_id", nodeID),
		)
		e.pending = &runnerPendingControl{kind: runnerControlCancel, nodeID: nodeID}
		e.recordBranchPatchLocked(currentState, nodeID, state.Patch{})
		return core.NewContext(ctx), &langgraph.NodeInterrupt{Node: nodeID, Value: string(runnerControlCancel)}
	}

	active := e.active[nodeID]
	if active == nil || active.step.NodeID != nodeID {
		step := StepRecord{
			StepID:    newRunnerID(),
			RunID:     e.run.RunID,
			NodeID:    nodeID,
			NodeName:  e.runner.nodeName(nodeID),
			Status:    StepStatusScheduled,
			StartedAt: e.runner.now(),
			UpdatedAt: e.runner.now(),
			Attempt:   1,
		}
		if err := e.runner.ExecutionStore.AppendStep(ctx, step); err != nil {
			return core.NewContext(ctx), err
		}

		e.run.CurrentNodeID = step.NodeID
		e.run.LastStepID = step.StepID
		e.run.UpdatedAt = e.runner.now()
		if err := e.runner.ExecutionStore.UpdateRun(ctx, e.run); err != nil {
			return core.NewContext(ctx), err
		}

		beforeID, err := e.runner.saveCheckpoint(ctx, e.run, step, nodeID, CheckpointBeforeNode, currentState, 0, nil, state.CloneArtifactRefs(e.artifacts))
		if err != nil {
			return core.NewContext(ctx), err
		}

		step.CheckpointBeforeID = beforeID
		step.Status = StepStatusRunning
		step.UpdatedAt = e.runner.now()
		if err := e.runner.ExecutionStore.UpdateStep(ctx, step); err != nil {
			return core.NewContext(ctx), err
		}

		active = &runnerActiveStep{
			step:               step,
			beforeCheckpointID: beforeID,
		}
		e.active[nodeID] = active
		logger.Debug("nodes scheduled", stepLogFields(step)...)
	}

	if active.attempts == 0 {
		active.attempts = 1
	} else {
		active.attempts++
	}
	step := active.step
	logStep := step
	logStep.Attempt = active.attempts

	if active.attempts == 1 {
		if e.run.PauseRequested {
			active.beforeInterrupted = true
			e.pending = &runnerPendingControl{kind: runnerControlPause, nodeID: nodeID}
			e.recordBranchPatchLocked(currentState, nodeID, state.Patch{})
			logger.Info("pause interrupt requested", stepLogFields(logStep)...)
			return core.NewContext(ctx), &langgraph.NodeInterrupt{Node: nodeID, Value: string(runnerControlPause)}
		}
		if hit := e.runner.matchBreakpoint(step.NodeID, string(CheckpointBeforeNode), e.skip); hit != nil {
			active.beforeInterrupted = true
			e.pending = &runnerPendingControl{kind: runnerControlPause, nodeID: nodeID, hit: hit}
			e.recordBranchPatchLocked(currentState, nodeID, state.Patch{})
			fields := append(stepLogFields(logStep),
				zap.String("breakpoint_id", hit.BreakpointID),
				zap.String("breakpoint_stage", hit.Stage),
			)
			logger.Info("breakpoint hit before nodes", fields...)
			return core.NewContext(ctx), &langgraph.NodeInterrupt{Node: nodeID, Value: hit}
		}

		if err := e.runner.publishEvent(ctx, e.run, step.StepID, step.NodeID, EventNodeStarted, map[string]any{
			"node_name": step.NodeName,
		}); err != nil {
			return core.NewContext(ctx), err
		}
		logger.Info("nodes started", append(stepLogFields(logStep), state.SummaryFields(currentState)...)...)
	} else {
		if err := e.runner.publishEvent(ctx, RunRecord{RunID: e.run.RunID}, step.StepID, step.NodeID, EventNodeRetry, map[string]any{
			"attempt": active.attempts - 1,
		}); err != nil {
			return core.NewContext(ctx), err
		}
		logger.Warn("nodes retrying", stepLogFields(logStep)...)
	}

	stepID := step.StepID
	nodeID = step.NodeID
	runID := e.run.RunID
	nodeCtx := WithRunnerEventPublisher(ctx, func(eventType EventType, payload any) error {
		return e.runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, eventType, payload)
	})
	nodeCtx = WithRunnerMetadata(nodeCtx, RunnerMetadata{
		RunID:   runID,
		StepID:  stepID,
		NodeID:  nodeID,
		Attempt: active.attempts,
	})
	nodeCtx = WithRunnerArtifactRecorder(nodeCtx, func(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
		ref, err := e.runner.recordArtifact(ctx, artifact)
		if err != nil {
			return state.ArtifactRef{}, err
		}
		e.appendArtifact(ref)
		return ref, nil
	})
	return withRunnerEventContext(nodeCtx, e.runner, runID, stepID, nodeID), nil
}

func (e *graphRunnerExecution) OnGraphStep(ctx context.Context, nodeID string, currentState *state.State) error {
	branchNodeIDs := parseParallelStepNodeIDs(nodeID)
	if len(branchNodeIDs) <= 1 {
		return nil
	}

	e.mu.Lock()
	if e.pending != nil {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	nextNodeIDs, err := e.resolveNextNodesForWave(ctx, currentState, branchNodeIDs)
	if err != nil {
		return err
	}

	e.mu.Lock()
	run := e.run
	stepIDs := make([]string, 0, len(branchNodeIDs))
	for _, branchNodeID := range branchNodeIDs {
		completed := e.completed[branchNodeID]
		if completed == nil {
			e.mu.Unlock()
			return fmt.Errorf("parallel wave barrier missing completed branch step for %q", branchNodeID)
		}
		stepIDs = append(stepIDs, completed.step.StepID)
	}
	waveID := e.waveIDForNodesLocked(branchNodeIDs)
	barrierRun := run
	barrierRun.CurrentNodeIDs = append([]string(nil), branchNodeIDs...)
	barrierRun.CurrentStepIDs = append([]string(nil), stepIDs...)
	barrierRun.NextNodeIDs = append([]string(nil), nextNodeIDs...)
	barrierRun.ParallelWaveID = waveID
	e.mu.Unlock()

	barrierID, err := e.runner.saveCheckpoint(ctx, barrierRun, StepRecord{}, parallelBarrierNodeID, CheckpointAfterParallelWave, currentState, 0, nil, e.snapshotArtifacts())
	if err != nil {
		return err
	}

	latestRun, err := e.runner.ExecutionStore.GetRun(ctx, barrierRun.RunID)
	if err == nil {
		barrierRun.PauseRequested = latestRun.PauseRequested
		barrierRun.CancelRequested = latestRun.CancelRequested
	}

	barrierRun.LastCheckpointID = barrierID
	barrierRun.CurrentNodeIDs = nil
	barrierRun.CurrentStepIDs = nil
	barrierRun.ParallelWaveID = ""
	barrierRun.UpdatedAt = e.runner.now()
	if err := e.runner.ExecutionStore.UpdateRun(ctx, barrierRun); err != nil {
		return err
	}

	e.mu.Lock()
	e.run = barrierRun
	e.lastState = currentState.Clone()
	for _, branchNodeID := range branchNodeIDs {
		delete(e.completed, branchNodeID)
	}
	var control *runnerPendingControl
	switch {
	case barrierRun.CancelRequested:
		control = &runnerPendingControl{kind: runnerControlCancel, nodeID: parallelBarrierNodeID, checkpointID: barrierID}
	case barrierRun.PauseRequested:
		control = &runnerPendingControl{kind: runnerControlPause, nodeID: parallelBarrierNodeID, checkpointID: barrierID}
	}
	if control != nil {
		e.pending = control
		if e.cancelInvoke != nil {
			e.cancelInvoke()
		}
	}
	e.mu.Unlock()
	if control != nil {
		return &langgraph.GraphInterrupt{
			Node:           parallelBarrierNodeID,
			State:          currentState,
			InterruptValue: string(control.kind),
			NextNodes:      append([]string(nil), nextNodeIDs...),
		}
	}
	return nil
}

func (e *graphRunnerExecution) OnParallelWave(base *state.State, nodeIDs []string) {
	if e == nil || base == nil || len(nodeIDs) <= 1 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	waveID := e.waves[base]
	if waveID == "" {
		waveID = newRunnerID()
		e.waves[base] = waveID
	}
	for _, nodeID := range nodeIDs {
		completed := e.completed[nodeID]
		if completed == nil {
			continue
		}
		completed.step.WaveID = waveID
		_ = e.runner.ExecutionStore.UpdateStep(context.Background(), completed.step)
	}
	e.run.ParallelWaveID = waveID
	e.run.CurrentNodeIDs = append([]string(nil), nodeIDs...)
	e.run.UpdatedAt = e.runner.now()
	_ = e.runner.ExecutionStore.UpdateRun(context.Background(), e.run)
}

func parseParallelStepNodeIDs(stepNodeID string) []string {
	stepNodeID = strings.TrimSpace(stepNodeID)
	if !strings.HasPrefix(stepNodeID, "step:[") || !strings.HasSuffix(stepNodeID, "]") {
		return nil
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(stepNodeID, "step:["), "]")
	fields := strings.Fields(inner)
	if len(fields) <= 1 {
		return nil
	}
	out := append([]string(nil), fields...)
	sort.Strings(out)
	return out
}

func (e *graphRunnerExecution) resolveNextNodesForWave(ctx context.Context, currentState *state.State, branchNodeIDs []string) ([]string, error) {
	graph := e.runner.runnerGraph()
	if graph == nil {
		return nil, errors.New("graph runner graph is nil")
	}
	seen := map[string]struct{}{}
	nextNodeIDs := make([]string, 0)
	for _, branchNodeID := range branchNodeIDs {
		targets, err := graph.ResolveNextNodes(ctx, branchNodeID, currentState)
		if err != nil {
			return nil, err
		}
		for _, target := range targets {
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			nextNodeIDs = append(nextNodeIDs, target)
		}
	}
	sort.Strings(nextNodeIDs)
	return nextNodeIDs, nil
}

func (e *graphRunnerExecution) SetBranchPatchRecorder(recorder BranchPatchRecorder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.patchRecorder = recorder
}

func (e *graphRunnerExecution) recordBranchPatch(base *state.State, nodeID string, patch state.Patch) {
	e.mu.Lock()
	recorder := e.patchRecorder
	e.mu.Unlock()
	if recorder != nil {
		recorder.RecordBranchPatch(base, nodeID, patch)
	}
}

func (e *graphRunnerExecution) recordBranchPatchLocked(base *state.State, nodeID string, patch state.Patch) {
	if e == nil || e.patchRecorder == nil {
		return
	}
	e.patchRecorder.RecordBranchPatch(base, nodeID, patch)
}

func (e *graphRunnerExecution) afterNode(ctx context.Context, nodeID string, beforeState *state.State, currentState *state.State) error {
	e.mu.Lock()
	active := e.active[nodeID]
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
	run := e.run
	before := beforeState.Clone()
	e.mu.Unlock()

	afterID, err := e.runner.saveCheckpoint(ctx, run, step, nodeID, CheckpointAfterNode, currentState, attempts, nil, e.snapshotArtifacts())
	if err != nil {
		return err
	}
	changes, err := e.runner.computeStateDiff(before, currentState)
	if err != nil {
		return err
	}
	if err := e.runner.publishStateDiffChanges(ctx, run, step, changes); err != nil {
		return err
	}

	now := e.runner.now()
	step.Attempt = attempts
	step.Status = StepStatusSucceeded
	step.CheckpointAfterID = afterID
	step.FinishedAt = &now
	step.UpdatedAt = now
	if err := e.runner.ExecutionStore.UpdateStep(ctx, step); err != nil {
		return err
	}

	if err := e.runner.publishEvent(ctx, run, step.StepID, step.NodeID, EventNodeFinished, map[string]any{
		"attempt": attempts,
	}); err != nil {
		return err
	}

	run.LastCheckpointID = afterID
	if latestRun, err := e.runner.ExecutionStore.GetRun(ctx, run.RunID); err == nil {
		run.PauseRequested = latestRun.PauseRequested
		run.CancelRequested = latestRun.CancelRequested
	}
	run.UpdatedAt = e.runner.now()
	if err := e.runner.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return err
	}
	fields := append(stepLogFields(step),
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
	e.completed[nodeID] = &runnerCompletedStep{
		step:              step,
		afterCheckpointID: afterID,
	}
	delete(e.active, nodeID)
	if e.pending != nil && e.pending.nodeID == nodeID {
		e.pending = nil
	}
	var control *runnerPendingControl
	if !e.runner.runnerGraph().IsParallelBranchTarget(nodeID) {
		switch {
		case run.CancelRequested:
			control = &runnerPendingControl{kind: runnerControlCancel, nodeID: nodeID, checkpointID: afterID}
			e.pending = control
			if e.cancelInvoke != nil {
				e.cancelInvoke()
			}
		case run.PauseRequested:
			control = &runnerPendingControl{kind: runnerControlPause, nodeID: nodeID, checkpointID: afterID}
			e.pending = control
			if e.cancelInvoke != nil {
				e.cancelInvoke()
			}
		}
	}
	e.mu.Unlock()
	if control != nil {
		return &langgraph.GraphInterrupt{
			Node:           control.nodeID,
			State:          currentState,
			InterruptValue: string(control.kind),
		}
	}
	return nil
}

func (e *graphRunnerExecution) validateContract(ctx context.Context, run RunRecord, step StepRecord, nodeID string, currentState *state.State, changes []state.StateChange) error {
	policy := e.contractPolicy
	if !policy.Enabled() || policy.Mode == core.ContractValidationOff || e.nodeContracts == nil {
		return nil
	}
	contract, ok := e.nodeContracts[nodeID]
	if !ok {
		return nil
	}
	violations := issuesToContractViolations(nodeID, state.ValidateRequiredReads(currentState, contract))
	if len(violations) == 0 {
		return nil
	}
	e.reportContractViolationsWithRun(ctx, run, step, violations)
	if policy.Mode == core.ContractValidationStrict && policy.EnforceWrites {
		return fmt.Errorf("state contract violation in node %q: %d violation(s) detected", nodeID, len(violations))
	}
	return nil
}

func (e *graphRunnerExecution) waveIDForNodesLocked(nodeIDs []string) string {
	for _, nodeID := range nodeIDs {
		if completed := e.completed[nodeID]; completed != nil && completed.step.WaveID != "" {
			return completed.step.WaveID
		}
	}
	return newRunnerID()
}

func (e *graphRunnerExecution) reportContractViolations(ctx context.Context, nodeID string, violations []core.ContractViolation) {
	e.mu.Lock()
	run := e.run
	var step StepRecord
	if active := e.active[nodeID]; active != nil {
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
	_ = e.runner.publishEvent(ctx, run, step.StepID, step.NodeID, EventContractViolation, map[string]any{
		"violations": violations,
	})
}

func (e *graphRunnerExecution) finalizeFailure(ctx context.Context, err error) error {
	e.mu.Lock()
	if len(e.active) == 0 {
		e.mu.Unlock()
		return nil
	}
	items := make([]runnerActiveStep, 0, len(e.active))
	for nodeID, active := range e.active {
		if active == nil {
			continue
		}
		items = append(items, *active)
		delete(e.active, nodeID)
	}
	run := e.run
	e.pending = nil
	e.mu.Unlock()

	for _, item := range items {
		step := item.step
		attempts := item.attempts
		now := e.runner.now()
		step.Attempt = attempts
		step.Status = StepStatusFailed
		step.ErrorCode = "node_failed"
		step.ErrorMessage = err.Error()
		step.FinishedAt = &now
		step.UpdatedAt = now
		if updateErr := e.runner.ExecutionStore.UpdateStep(ctx, step); updateErr != nil {
			return updateErr
		}
		logger.Error("nodes failed", append(stepLogFields(step), zap.Error(err))...)

		if publishErr := e.runner.publishEvent(ctx, run, step.StepID, step.NodeID, EventNodeFailed, map[string]any{
			"error":   err.Error(),
			"attempt": attempts,
		}); publishErr != nil {
			return publishErr
		}
	}
	return nil
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
	if control.nodeID != "" {
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
	nodeID := e.run.CurrentNodeID
	if e.pending == nil || e.pending.kind != runnerControlCancel {
		e.pending = &runnerPendingControl{kind: runnerControlCancel, nodeID: nodeID}
	}
	cancel := e.cancelInvoke
	e.mu.Unlock()
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
	var (
		nodeID string
		cancel context.CancelFunc
	)
	if len(e.active) == 1 {
		for id := range e.active {
			nodeID = id
		}
		if nodeID != "" && !e.runner.runnerGraph().IsParallelBranchTarget(nodeID) {
			if e.pending == nil {
				e.pending = &runnerPendingControl{kind: runnerControlPause, nodeID: nodeID, message: "pause requested"}
			}
			cancel = e.cancelInvoke
		}
	}
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *graphRunnerExecution) consumeLastCompleted(nodeID string) *runnerCompletedStep {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastCompleted == nil {
		return nil
	}
	if nodeID != "" && e.lastCompleted.step.NodeID != nodeID {
		return nil
	}
	completed := *e.lastCompleted
	e.lastCompleted = nil
	return &completed
}

func (e *graphRunnerExecution) appendArtifact(ref state.ArtifactRef) {
	if ref.ID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artifacts = append(e.artifacts, ref)
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
	return graph.AfterInterruptNodes(e.runner.Breakpoints)
}

func (e *graphRunnerExecution) markNodeInterrupt(nodeID string, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := e.active[nodeID]
	if active == nil {
		return
	}
	/// make sure the nodes resume at the same nodes after restart
	active.beforeInterrupted = true
	e.pending = &runnerPendingControl{kind: runnerControlPause, nodeID: nodeID, message: message}
	logStep := active.step
	logStep.Attempt = active.attempts
	logger.Info("nodes interrupt captured", stepLogFields(logStep)...)
}

func (e *graphRunnerExecution) firstActiveStepLocked(nodeID string) *runnerActiveStep {
	if e == nil || len(e.active) == 0 {
		return nil
	}
	if nodeID != "" {
		return e.active[nodeID]
	}
	for _, active := range e.active {
		return active
	}
	return nil
}

type runnerGraphCallbacks struct {
	langgraph.NoOpCallbackHandler
	execution *graphRunnerExecution
}

func (c *runnerGraphCallbacks) OnGraphStep(ctx context.Context, stepNodeID string, value any) {
	if c == nil || c.execution == nil {
		return
	}
	typed, ok := value.(*state.State)
	if !ok {
		return
	}
	if err := c.execution.OnGraphStep(ctx, stepNodeID, typed); err != nil {
		c.execution.recordCallbackError(err)
	}
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
