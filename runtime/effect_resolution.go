package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type EffectResolutionAction string

const (
	EffectResolutionConfirmNotApplied EffectResolutionAction = "confirm_not_applied"
	EffectResolutionCompensate        EffectResolutionAction = "compensate"
)

type EffectResolutionStatus string

const (
	EffectResolutionIntent    EffectResolutionStatus = "intent"
	EffectResolutionSucceeded EffectResolutionStatus = "succeeded"
	EffectResolutionUnknown   EffectResolutionStatus = "unknown"
)

type EffectResolution struct {
	ID              string                 `json:"id"`
	AttemptID       string                 `json:"attempt_id"`
	Action          EffectResolutionAction `json:"action"`
	Status          EffectResolutionStatus `json:"status"`
	Actor           string                 `json:"actor"`
	Reason          string                 `json:"reason"`
	CompensationKey string                 `json:"compensation_key,omitempty"`
	Error           string                 `json:"error,omitempty"`
	RequestedAt     time.Time              `json:"requested_at"`
	ResolvedAt      *time.Time             `json:"resolved_at,omitempty"`
}

type EffectResolutionRequest struct {
	ResolutionID string                 `json:"resolution_id"`
	StepID       string                 `json:"step_id"`
	Action       EffectResolutionAction `json:"action"`
	Actor        string                 `json:"actor"`
	Reason       string                 `json:"reason"`
	Continue     bool                   `json:"continue"`
}

type EffectResolutionResult struct {
	Run        RunRecord        `json:"run"`
	State      *state.State     `json:"state,omitempty"`
	Resolution EffectResolution `json:"resolution"`
	Continued  bool             `json:"continued"`
}

type effectCompensationGraph interface {
	CompensateEffect(context.Context, string, core.EffectCompensationRequest, *state.State) error
}

func (r *GraphRunner) ResolveEffect(ctx context.Context, runID string, request EffectResolutionRequest, input *state.State) (EffectResolutionResult, error) {
	if err := r.validate(); err != nil {
		return EffectResolutionResult{}, err
	}
	ctx = normalizeRunnerContext(ctx)
	request, err := normalizeEffectResolutionRequest(runID, request)
	if err != nil {
		return EffectResolutionResult{}, err
	}
	run, err := r.executionStore.GetRun(ctx, runID)
	if err != nil {
		return EffectResolutionResult{}, err
	}
	if err := r.validateRunGraphHash(run); err != nil {
		return EffectResolutionResult{}, err
	}
	step, err := r.executionStore.GetStep(ctx, request.StepID)
	if err != nil {
		return EffectResolutionResult{}, err
	}
	if step.RunID != run.RunID {
		return EffectResolutionResult{}, fmt.Errorf("step %q does not belong to run %q", step.StepID, run.RunID)
	}
	if step.EffectResolution != nil {
		return r.replayEffectResolution(ctx, run, step, request, input)
	}
	if run.Status != RunStatusFailed || run.Deletion != nil {
		return EffectResolutionResult{}, fmt.Errorf("%w: run %q status %q cannot resolve an effect", ErrRunControlNotAllowed, run.RunID, run.Status)
	}
	if step.Status != StepStatusFailed || step.EffectStatus != core.EffectUnknown {
		return EffectResolutionResult{}, fmt.Errorf("%w: step %q does not have an unresolved effect", ErrRunControlNotAllowed, step.StepID)
	}
	checkpoint, err := r.loadEffectResolutionCheckpoint(ctx, run, step)
	if err != nil {
		return EffectResolutionResult{}, err
	}
	if err := r.claimEffectResolution(run.RunID, step.StepID, request.ResolutionID); err != nil {
		return EffectResolutionResult{}, err
	}
	defer r.releaseEffectResolution(run.RunID, step.StepID, request.ResolutionID)
	resolution := EffectResolution{
		ID: request.ResolutionID, Action: request.Action, Status: EffectResolutionIntent,
		AttemptID: newRunnerID(), Actor: request.Actor, Reason: request.Reason, RequestedAt: r.currentTime(),
	}
	if request.Action == EffectResolutionCompensate {
		resolution.CompensationKey = stableRuntimeID("compensation", step.OperationKey, request.ResolutionID)
	}
	step.EffectResolution = &resolution
	requestedEvent, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventEffectResolutionRequested, resolution)
	if err != nil {
		return EffectResolutionResult{}, err
	}
	requestedEvent.OperationKey = step.OperationKey
	commitResult, err := r.commitRuntime(ctx, Commit{
		TransactionID: stableRuntimeID("effect-resolution", run.RunID, step.StepID, resolution.ID, "intent"),
		Run:           &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:         []StepWrite{{Mode: StepWriteUpdate, Step: step}},
		Events:        []Event{requestedEvent},
	})
	if err != nil {
		persistedStep, loadErr := r.executionStore.GetStep(ctx, request.StepID)
		if loadErr == nil && persistedStep.EffectResolution != nil {
			return r.replayEffectResolution(ctx, run, persistedStep, request, input)
		}
		return EffectResolutionResult{}, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	if request.Action == EffectResolutionCompensate {
		if err := r.executeEffectCompensation(ctx, run, step, checkpoint, resolution); err != nil {
			return r.persistUnknownEffectResolution(ctx, run, step, resolution, err)
		}
	}
	return r.completeEffectResolution(ctx, run, step, checkpoint, resolution, request, input)
}

func normalizeEffectResolutionRequest(runID string, request EffectResolutionRequest) (EffectResolutionRequest, error) {
	runID = strings.TrimSpace(runID)
	request.ResolutionID = strings.TrimSpace(request.ResolutionID)
	request.StepID = strings.TrimSpace(request.StepID)
	request.Actor = strings.TrimSpace(request.Actor)
	request.Reason = strings.TrimSpace(request.Reason)
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return EffectResolutionRequest{}, err
	}
	if err := validateRunnerStorageID("effect resolution ID", request.ResolutionID); err != nil {
		return EffectResolutionRequest{}, err
	}
	if err := validateRunnerStorageID("step ID", request.StepID); err != nil {
		return EffectResolutionRequest{}, err
	}
	if request.Actor == "" {
		return EffectResolutionRequest{}, errors.New("effect resolution actor is required")
	}
	if request.Reason == "" {
		return EffectResolutionRequest{}, errors.New("effect resolution reason is required")
	}
	switch request.Action {
	case EffectResolutionConfirmNotApplied, EffectResolutionCompensate:
	default:
		return EffectResolutionRequest{}, fmt.Errorf("unsupported effect resolution action %q", request.Action)
	}
	return request, nil
}

func validateEffectResolution(step StepRecord) error {
	resolution := step.EffectResolution
	if resolution == nil {
		if step.EffectStatus == core.EffectNotApplied || step.EffectStatus == core.EffectCompensated {
			return fmt.Errorf("step %q resolved effect status requires a resolution", step.StepID)
		}
		return nil
	}
	if err := validateRunnerStorageID("effect resolution ID", resolution.ID); err != nil {
		return fmt.Errorf("step %q: %w", step.StepID, err)
	}
	if err := validateRunnerStorageID("effect resolution attempt ID", resolution.AttemptID); err != nil {
		return fmt.Errorf("step %q: %w", step.StepID, err)
	}
	if resolution.Actor == "" || strings.TrimSpace(resolution.Actor) != resolution.Actor {
		return fmt.Errorf("step %q effect resolution actor is invalid", step.StepID)
	}
	if resolution.Reason == "" || strings.TrimSpace(resolution.Reason) != resolution.Reason {
		return fmt.Errorf("step %q effect resolution reason is invalid", step.StepID)
	}
	if resolution.RequestedAt.IsZero() {
		return fmt.Errorf("step %q effect resolution requested_at is required", step.StepID)
	}
	switch resolution.Action {
	case EffectResolutionConfirmNotApplied:
		if resolution.CompensationKey != "" {
			return fmt.Errorf("step %q confirmed effect resolution cannot have a compensation key", step.StepID)
		}
	case EffectResolutionCompensate:
		if err := validateRunnerStorageID("compensation key", resolution.CompensationKey); err != nil {
			return fmt.Errorf("step %q: %w", step.StepID, err)
		}
	default:
		return fmt.Errorf("step %q has invalid effect resolution action %q", step.StepID, resolution.Action)
	}
	switch resolution.Status {
	case EffectResolutionIntent:
		if resolution.ResolvedAt != nil {
			return fmt.Errorf("step %q intent effect resolution cannot be resolved", step.StepID)
		}
		if step.EffectStatus != core.EffectUnknown {
			return fmt.Errorf("step %q intent effect resolution requires unknown effect status", step.StepID)
		}
	case EffectResolutionUnknown:
		if resolution.ResolvedAt == nil || resolution.Error == "" || step.EffectStatus != core.EffectUnknown {
			return fmt.Errorf("step %q unknown effect resolution is incomplete", step.StepID)
		}
	case EffectResolutionSucceeded:
		if resolution.ResolvedAt == nil || resolution.Error != "" {
			return fmt.Errorf("step %q succeeded effect resolution is incomplete", step.StepID)
		}
		if resolution.Action == EffectResolutionConfirmNotApplied && step.EffectStatus != core.EffectNotApplied {
			return fmt.Errorf("step %q confirmed effect resolution status mismatch", step.StepID)
		}
		if resolution.Action == EffectResolutionCompensate && step.EffectStatus != core.EffectCompensated {
			return fmt.Errorf("step %q compensated effect resolution status mismatch", step.StepID)
		}
	default:
		return fmt.Errorf("step %q has invalid effect resolution status %q", step.StepID, resolution.Status)
	}
	return nil
}

func validateStepEffectTransition(existing, next StepRecord) error {
	if err := validateStepEffect(next); err != nil {
		return err
	}
	previous := existing.EffectResolution
	resolution := next.EffectResolution
	if previous == nil {
		if resolution == nil {
			return nil
		}
		if existing.EffectStatus != core.EffectUnknown || resolution.Status != EffectResolutionIntent {
			return fmt.Errorf("step %q effect resolution must start from unknown with intent", next.StepID)
		}
		return nil
	}
	if resolution == nil {
		return fmt.Errorf("step %q effect resolution cannot be removed", next.StepID)
	}
	if previous.ID != resolution.ID || previous.AttemptID != resolution.AttemptID || previous.Action != resolution.Action ||
		previous.Actor != resolution.Actor || previous.Reason != resolution.Reason || previous.CompensationKey != resolution.CompensationKey ||
		!previous.RequestedAt.Equal(resolution.RequestedAt) {
		return fmt.Errorf("step %q effect resolution identity cannot change", next.StepID)
	}
	switch previous.Status {
	case EffectResolutionIntent:
		switch resolution.Status {
		case EffectResolutionIntent, EffectResolutionSucceeded, EffectResolutionUnknown:
			return nil
		}
	case EffectResolutionSucceeded, EffectResolutionUnknown:
		previousJSON, previousErr := json.Marshal(previous)
		nextJSON, nextErr := json.Marshal(resolution)
		if previousErr == nil && nextErr == nil && string(previousJSON) == string(nextJSON) && existing.EffectStatus == next.EffectStatus {
			return nil
		}
	}
	return fmt.Errorf("step %q effect resolution status cannot change from %q to %q", next.StepID, previous.Status, resolution.Status)
}

func (r *GraphRunner) loadEffectResolutionCheckpoint(ctx context.Context, run RunRecord, step StepRecord) (RestoredCheckpoint, error) {
	if strings.TrimSpace(step.CheckpointBeforeID) == "" {
		return RestoredCheckpoint{}, fmt.Errorf("%w: step %q has no before-node checkpoint", ErrRunControlNotAllowed, step.StepID)
	}
	checkpoint, err := r.LoadCheckpointState(ctx, step.CheckpointBeforeID)
	if err != nil {
		return RestoredCheckpoint{}, err
	}
	if err := validateCheckpointRun(run, checkpoint); err != nil {
		return RestoredCheckpoint{}, err
	}
	if checkpoint.Record.Stage != CheckpointBeforeNode || checkpoint.Record.StepID != step.StepID || checkpoint.Record.TaskID != step.TaskID || checkpoint.Record.NodeID != step.NodeID {
		return RestoredCheckpoint{}, fmt.Errorf("%w: step %q before-node checkpoint identity mismatch", ErrRunControlNotAllowed, step.StepID)
	}
	schedule, _, err := LoadGraphSchedule(checkpoint.Business)
	if err != nil {
		return RestoredCheckpoint{}, err
	}
	if len(schedule.CurrentTasks) != 1 || schedule.CurrentTasks[0].TaskID != step.TaskID || schedule.CurrentTasks[0].ParallelWaveSize > 1 || checkpoint.Runtime.ParallelWaveID != "" || checkpoint.Runtime.WaveID != "" {
		return RestoredCheckpoint{}, fmt.Errorf("%w: effect resolution for parallel wave step %q is not supported", ErrRunControlNotAllowed, step.StepID)
	}
	return checkpoint, nil
}

func (r *GraphRunner) executeEffectCompensation(ctx context.Context, run RunRecord, step StepRecord, checkpoint RestoredCheckpoint, resolution EffectResolution) error {
	compensationGraph, ok := r.runnerGraph().(effectCompensationGraph)
	if !ok {
		return fmt.Errorf("node %q does not support effect compensation", step.NodeID)
	}
	operations, err := r.effectOperationsForStep(run.RunID, step)
	if err != nil {
		return err
	}
	operation := core.EffectOperation{
		Key: resolution.CompensationKey, ParentKey: step.OperationKey, Kind: "compensation", Name: step.NodeID,
		Class: core.EffectCompensatable, Status: core.EffectIntent, IdempotencyKey: resolution.CompensationKey,
	}
	compensationCtx := core.WithEffectOperation(ctx, operation)
	compensationCtx = WithRunnerMetadata(compensationCtx, RunnerMetadata{
		RunID: run.RunID, StepID: step.StepID, TaskID: step.TaskID, NodeID: step.NodeID,
		ParentRunID: run.ParentRunID, ParentStepID: run.ParentStepID, ParentTaskID: run.ParentTaskID,
		RootRunID: run.RootRunID, RunPath: append([]string(nil), run.RunPath...), Namespace: run.Namespace,
	})
	compensationCtx = core.WithEffectJournal(compensationCtx, core.EffectJournalFunc(func(effectCtx context.Context, child core.EffectOperation) error {
		return r.recordResolvedEffect(effectCtx, run, step, child, resolution.ID)
	}))
	if err := r.recordResolvedEffect(compensationCtx, run, step, operation, resolution.ID); err != nil {
		return err
	}
	request := core.EffectCompensationRequest{
		Operation: core.EffectOperation{
			Key: step.OperationKey, Kind: "node", Name: step.NodeID, Class: step.EffectClass,
			Status: core.EffectUnknown, Attempt: step.Attempt, IdempotencyKey: step.OperationKey,
		},
		Operations: operations,
	}
	if err := compensationGraph.CompensateEffect(compensationCtx, step.NodeID, request, checkpoint.Business.Clone()); err != nil {
		operation.Status = core.EffectUnknown
		operation.Error = err.Error()
		_ = r.recordResolvedEffect(context.WithoutCancel(compensationCtx), run, step, operation, resolution.ID)
		return err
	}
	operation.Status = core.EffectSucceeded
	return r.recordResolvedEffect(context.WithoutCancel(compensationCtx), run, step, operation, resolution.ID)
}

func (r *GraphRunner) recordResolvedEffect(ctx context.Context, run RunRecord, step StepRecord, operation core.EffectOperation, resolutionID string) error {
	if strings.TrimSpace(operation.Key) == "" {
		return errors.New("effect operation key is required")
	}
	eventType := EventEffectOutcome
	if operation.Status == core.EffectIntent {
		eventType = EventEffectIntent
	}
	event, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, eventType, operation)
	if err != nil {
		return err
	}
	event.ID = stableRuntimeID("effect-resolution-event", resolutionID, operation.Key, string(operation.Status), fmt.Sprintf("%d", operation.Attempt))
	event.OperationKey = operation.Key
	_, err = r.commitRuntime(ctx, Commit{
		TransactionID: stableRuntimeID("effect-resolution-operation", resolutionID, operation.Key, string(operation.Status), fmt.Sprintf("%d", operation.Attempt)),
		Run:           &RunWrite{Mode: RunWriteCheck, Run: run},
		Events:        []Event{event},
	})
	return err
}

func (r *GraphRunner) effectOperationsForStep(runID string, step StepRecord) ([]core.EffectOperation, error) {
	events, err := r.ListEvents(runID)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]core.EffectOperation)
	for _, event := range events {
		if event.StepID != step.StepID || event.Type != EventEffectIntent && event.Type != EventEffectOutcome {
			continue
		}
		var operation core.EffectOperation
		if err := json.Unmarshal(event.Payload, &operation); err != nil {
			return nil, fmt.Errorf("decode effect operation event %q: %w", event.ID, err)
		}
		if operation.Key == "" || operation.Key == step.OperationKey {
			continue
		}
		latest[operation.Key] = operation
	}
	operations := make([]core.EffectOperation, 0, len(latest))
	for _, operation := range latest {
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(leftIndex, rightIndex int) bool { return operations[leftIndex].Key < operations[rightIndex].Key })
	return operations, nil
}

func (r *GraphRunner) persistUnknownEffectResolution(ctx context.Context, run RunRecord, step StepRecord, resolution EffectResolution, cause error) (EffectResolutionResult, error) {
	now := r.currentTime()
	resolution.Status = EffectResolutionUnknown
	resolution.Error = cause.Error()
	resolution.ResolvedAt = &now
	step.EffectResolution = &resolution
	event, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventEffectResolutionOutcome, resolution)
	if err != nil {
		return EffectResolutionResult{}, errors.Join(cause, err)
	}
	event.OperationKey = step.OperationKey
	_, persistErr := r.commitRuntime(context.WithoutCancel(ctx), Commit{
		TransactionID: stableRuntimeID("effect-resolution", run.RunID, step.StepID, resolution.ID, "unknown"),
		Run:           &RunWrite{Mode: RunWriteCheck, Run: run},
		Steps:         []StepWrite{{Mode: StepWriteUpdate, Step: step}},
		Events:        []Event{event},
	})
	result := EffectResolutionResult{Run: run, Resolution: resolution}
	return result, fmt.Errorf("%w: compensation outcome is unknown: %v", ErrRunControlNotAllowed, errors.Join(cause, persistErr))
}

func (r *GraphRunner) completeEffectResolution(ctx context.Context, run RunRecord, step StepRecord, checkpoint RestoredCheckpoint, resolution EffectResolution, request EffectResolutionRequest, input *state.State) (EffectResolutionResult, error) {
	now := r.currentTime()
	resolution.Status = EffectResolutionSucceeded
	resolution.ResolvedAt = &now
	step.EffectResolution = &resolution
	if resolution.Action == EffectResolutionCompensate {
		step.EffectStatus = core.EffectCompensated
	} else {
		step.EffectStatus = core.EffectNotApplied
	}
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.FinishedAt = nil
	run.LastCheckpointID = checkpoint.Record.CheckpointID
	run.CurrentNodeID = step.NodeID
	run.CurrentNodeIDs = []string{step.NodeID}
	run.CurrentStepIDs = nil
	run.NextNodeIDs = nil
	run.ParallelWaveID = ""
	run.UpdatedAt = now
	event, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventEffectResolutionOutcome, resolution)
	if err != nil {
		return EffectResolutionResult{}, err
	}
	event.OperationKey = step.OperationKey
	pausedEvent, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventRunPaused, map[string]any{
		"checkpoint_id": checkpoint.Record.CheckpointID,
		"stage":         CheckpointBeforeNode,
		"node_id":       step.NodeID,
		"message":       "effect resolution completed; run is safe to resume",
	})
	if err != nil {
		return EffectResolutionResult{}, err
	}
	commitResult, err := r.commitRuntime(ctx, Commit{
		TransactionID: stableRuntimeID("effect-resolution", run.RunID, step.StepID, resolution.ID, "succeeded"),
		Run:           &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:         []StepWrite{{Mode: StepWriteUpdate, Step: step}},
		Events:        []Event{event, pausedEvent},
	})
	if err != nil {
		return EffectResolutionResult{}, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	result := EffectResolutionResult{Run: run, Resolution: resolution}
	if !request.Continue {
		return result, nil
	}
	resumedRun, finalState, err := r.Resume(ctx, run.RunID, input)
	result.Run = resumedRun
	result.State = finalState
	result.Continued = true
	return result, err
}

func (r *GraphRunner) replayEffectResolution(ctx context.Context, run RunRecord, step StepRecord, request EffectResolutionRequest, input *state.State) (EffectResolutionResult, error) {
	resolution := *step.EffectResolution
	if resolution.ID != request.ResolutionID || resolution.Action != request.Action || resolution.Actor != request.Actor || resolution.Reason != request.Reason {
		return EffectResolutionResult{}, fmt.Errorf("%w: step %q already has effect resolution %q", ErrRunControlNotAllowed, step.StepID, resolution.ID)
	}
	result := EffectResolutionResult{Run: run, Resolution: resolution}
	if resolution.Status == EffectResolutionIntent {
		if err := r.claimEffectResolution(run.RunID, step.StepID, resolution.ID); err != nil {
			return result, err
		}
		defer r.releaseEffectResolution(run.RunID, step.StepID, resolution.ID)
		checkpoint, err := r.loadEffectResolutionCheckpoint(ctx, run, step)
		if err != nil {
			return result, err
		}
		if resolution.Action == EffectResolutionConfirmNotApplied {
			return r.completeEffectResolution(ctx, run, step, checkpoint, resolution, request, input)
		}
		operation, found, err := r.effectOperationByKey(run.RunID, step, resolution.CompensationKey)
		if err != nil {
			return result, err
		}
		if !found {
			if err := r.executeEffectCompensation(ctx, run, step, checkpoint, resolution); err != nil {
				return r.persistUnknownEffectResolution(ctx, run, step, resolution, err)
			}
			return r.completeEffectResolution(ctx, run, step, checkpoint, resolution, request, input)
		}
		switch operation.Status {
		case core.EffectSucceeded:
			return r.completeEffectResolution(ctx, run, step, checkpoint, resolution, request, input)
		case core.EffectIntent, core.EffectUnknown:
			return r.persistUnknownEffectResolution(ctx, run, step, resolution, errors.New("compensation outcome is unknown after recovery"))
		default:
			return result, fmt.Errorf("%w: compensation operation %q has unsupported status %q", ErrRunControlNotAllowed, operation.Key, operation.Status)
		}
	}
	if resolution.Status != EffectResolutionSucceeded {
		return result, fmt.Errorf("%w: effect resolution %q status is %q and cannot be replayed", ErrRunControlNotAllowed, resolution.ID, resolution.Status)
	}
	if !request.Continue || run.Status != RunStatusPaused {
		return result, nil
	}
	resumedRun, finalState, err := r.Resume(ctx, run.RunID, input)
	result.Run = resumedRun
	result.State = finalState
	result.Continued = true
	return result, err
}

func (r *GraphRunner) effectOperationByKey(runID string, step StepRecord, operationKey string) (core.EffectOperation, bool, error) {
	operations, err := r.effectOperationsForStep(runID, step)
	if err != nil {
		return core.EffectOperation{}, false, err
	}
	for _, operation := range operations {
		if operation.Key == operationKey {
			return operation, true, nil
		}
	}
	return core.EffectOperation{}, false, nil
}

func (r *GraphRunner) claimEffectResolution(runID, stepID, resolutionID string) error {
	key := runID + "\x00" + stepID
	r.effectResolutionMu.Lock()
	defer r.effectResolutionMu.Unlock()
	if r.activeResolutions == nil {
		r.activeResolutions = make(map[string]string)
	}
	if activeID := r.activeResolutions[key]; activeID != "" {
		return fmt.Errorf("%w: effect resolution %q is already active for step %q", ErrRunControlNotAllowed, activeID, stepID)
	}
	r.activeResolutions[key] = resolutionID
	return nil
}

func (r *GraphRunner) releaseEffectResolution(runID, stepID, resolutionID string) {
	key := runID + "\x00" + stepID
	r.effectResolutionMu.Lock()
	defer r.effectResolutionMu.Unlock()
	if r.activeResolutions[key] == resolutionID {
		delete(r.activeResolutions, key)
	}
}

func (r *GraphRunner) ensureRunEffectsResolved(ctx context.Context, run RunRecord) error {
	if run.Status != RunStatusFailed {
		return nil
	}
	steps, err := r.executionStore.ListSteps(ctx, run.RunID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.EffectStatus == core.EffectUnknown {
			return fmt.Errorf("%w: run %q step %q has an unresolved effect", ErrRunControlNotAllowed, run.RunID, step.StepID)
		}
	}
	return nil
}
