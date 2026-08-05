package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"

	"github.com/google/uuid"
	langgraph "github.com/smallnest/langgraphgo/graph"
	"go.uber.org/zap"
)

type GraphRunner struct {
	graph              RunnerGraph
	ExecutionStore     ExecutionStore
	CheckpointStore    CheckpointStore
	ArtifactStore      ArtifactStore
	RunDeleter         RunDeleter
	Codec              state.StateCodec
	EventSink          EventSink
	GraphID            string
	GraphVersion       string
	GraphHash          string
	GraphSnapshotHash  string
	GraphSessionID     string
	Breakpoints        []Breakpoint
	ContractValidation core.ContractValidationMode
	ContractPolicy     ContractPolicy
	StartupWarnings    []WarningRecord
	NodeContracts      map[string]state.Contract
	Now                func() time.Time
	activeMu           sync.Mutex
	activeExecutions   map[string]*graphRunnerExecution
}

func NewGraphRunner(graph RunnerGraph, executionStore ExecutionStore, checkpointStore CheckpointStore, codec state.StateCodec, eventSink EventSink) *GraphRunner {
	if codec == nil {
		codec = state.NewJSONStateCodec("")
	}
	if eventSink == nil {
		eventSink = NoopEventSink{}
	}
	return &GraphRunner{
		graph:           graph,
		ExecutionStore:  executionStore,
		CheckpointStore: checkpointStore,
		ArtifactStore:   NewNoopArtifactStore(),
		Codec:           codec,
		EventSink:       eventSink,
		Now:             time.Now,
	}
}

func (r *GraphRunner) Start(ctx context.Context, initialState *state.State) (RunRecord, *state.State, error) {
	run, initialState, err := r.startRun(ctx, initialState)
	if err != nil {
		return RunRecord{}, initialState, err
	}
	return r.continueStartedRun(ctx, run, initialState)
}

// StartAsync creates and starts a run, then executes it in the background. The
// returned run has already been persisted as running and EventRunStarted has
// already been published. done is closed when execution stops.
func (r *GraphRunner) StartAsync(ctx context.Context, initialState *state.State) (RunRecord, <-chan struct{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if initialState != nil {
		initialState = initialState.Clone()
	}
	run, initialState, err := r.startRun(ctx, initialState)
	if err != nil {
		return RunRecord{}, nil, err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if recovered := recover(); recovered != nil {
				_, _, _ = r.failRun(
					context.WithoutCancel(ctx),
					run,
					initialState,
					"async_execution_panic",
					fmt.Sprintf("panic: %v", recovered),
				)
			}
		}()
		finishedRun, finalState, runErr := r.continueStartedRun(ctx, run, initialState)
		if runErr != nil && finishedRun.RunID == "" {
			if finalState == nil {
				finalState = initialState
			}
			_, _, _ = r.failRun(
				context.WithoutCancel(ctx),
				run,
				finalState,
				"async_execution_failed",
				runErr.Error(),
			)
		}
	}()
	return run, done, nil
}

func (r *GraphRunner) startRun(ctx context.Context, initialState *state.State) (RunRecord, *state.State, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, initialState, err
	}

	if initialState == nil {
		initialState = state.NewState()
	}

	now := r.now()
	entryPoint := r.entryPointID()
	run := RunRecord{
		RunID:             newRunnerID(),
		GraphID:           r.graphID(),
		GraphVersion:      r.graphVersion(),
		GraphHash:         r.graphHash(),
		GraphSnapshotHash: r.graphSnapshotHash(),
		GraphSessionID:    r.graphSessionID(),
		Status:            RunStatusPending,
		EntryNodeID:       entryPoint,
		StartedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.ExecutionStore.CreateRun(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	payload := map[string]any{
		"entry_node_id": run.EntryNodeID,
	}
	if run.GraphHash != "" {
		payload["graph_hash"] = run.GraphHash
	}
	if run.GraphSnapshotHash != "" {
		payload["graph_snapshot_hash"] = run.GraphSnapshotHash
	}
	if run.GraphSessionID != "" {
		payload["graph_session_id"] = run.GraphSessionID
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunCreated, payload); err != nil {
		return RunRecord{}, initialState, err
	}

	run.Status = RunStatusRunning
	run.CurrentNodeID = run.EntryNodeID
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunStarted, nil); err != nil {
		return RunRecord{}, initialState, err
	}
	logger.Info("run started", append(runLogFields(run), state.SummaryFields(initialState)...)...)
	return run, initialState, nil
}

func (r *GraphRunner) continueStartedRun(ctx context.Context, run RunRecord, initialState *state.State) (RunRecord, *state.State, error) {
	if err := r.publishStartupWarnings(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	return r.execute(ctx, run, initialState.Clone(), []string{run.EntryNodeID}, nil, nil)
}

func (r *GraphRunner) Resume(ctx context.Context, runID string, input *state.State) (RunRecord, *state.State, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, nil, err
	}

	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.validateRunGraphHash(run); err != nil {
		return RunRecord{}, nil, err
	}
	if strings.TrimSpace(run.LastCheckpointID) == "" {
		return RunRecord{}, nil, fmt.Errorf("resume run %q: no checkpoint available", runID)
	}

	checkpoint, err := r.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	logger.Info("checkpoint loaded",
		zap.String("run_id", run.RunID),
		zap.String("status", string(run.Status)),
		zap.String("checkpoint_id", run.LastCheckpointID),
		zap.String("current_node_id", checkpoint.Runtime.CurrentNodeID),
		zap.String("current_step_id", checkpoint.Runtime.CurrentStepID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	)

	switch {
	case isResumableRunStatus(run.Status):
		return r.resumeExistingRun(ctx, run, checkpoint, input)
	case checkpoint.Record.Stage == CheckpointAfterParallelWave:
		return r.resumeExistingRun(ctx, run, checkpoint, input)
	case isContinuableRunStatus(run.Status):
		return r.continueRun(ctx, run, checkpoint, input)
	default:
		return RunRecord{}, nil, fmt.Errorf("run %q status %q is not resumable", runID, run.Status)
	}
}

func (r *GraphRunner) ResumeFromCheckpoint(ctx context.Context, checkpointID string, input *state.State) (RunRecord, *state.State, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, nil, err
	}
	if strings.TrimSpace(checkpointID) == "" {
		return RunRecord{}, nil, fmt.Errorf("checkpoint id is required")
	}

	checkpoint, err := r.LoadCheckpointState(ctx, checkpointID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if strings.TrimSpace(checkpoint.Record.RunID) == "" {
		return RunRecord{}, nil, fmt.Errorf("checkpoint %q has no run id", checkpointID)
	}

	run, err := r.ExecutionStore.GetRun(ctx, checkpoint.Record.RunID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.validateRunGraphHash(run); err != nil {
		return RunRecord{}, nil, err
	}

	switch {
	case isResumableRunStatus(run.Status):
		return r.resumeExistingRun(ctx, run, checkpoint, input)
	case checkpoint.Record.Stage == CheckpointAfterParallelWave:
		return r.resumeExistingRun(ctx, run, checkpoint, input)
	case isContinuableRunStatus(run.Status):
		return r.continueRun(ctx, run, checkpoint, input)
	default:
		return RunRecord{}, nil, fmt.Errorf("run %q status %q is not resumable", run.RunID, run.Status)
	}
}

func (r *GraphRunner) GetResumableRun(ctx context.Context) (*RunRecord, error) {
	return r.latestCheckpointedRun(ctx, isResumableRunStatus)
}

func (r *GraphRunner) GetContinuableRun(ctx context.Context) (*RunRecord, error) {
	return r.latestCheckpointedRun(ctx, isContinuableRunStatus)
}

func (r *GraphRunner) latestCheckpointedRun(ctx context.Context, predicate func(RunStatus) bool) (*RunRecord, error) {
	runs, err := r.ListRuns(ctx, RunFilter{})
	if err != nil {
		return nil, err
	}
	var candidate *RunRecord
	for i := range runs {
		run := runs[i]
		if run.LastCheckpointID == "" {
			continue
		}
		if predicate != nil && !predicate(run.Status) {
			continue
		}
		if candidate == nil || candidate.UpdatedAt.Before(run.UpdatedAt) {
			candidate = &run
		}
	}
	return candidate, nil
}

func isResumableRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusPaused, RunStatusRunning, RunStatusPending:
		return true
	default:
		return false
	}
}

func isContinuableRunStatus(status RunStatus) bool {
	if isResumableRunStatus(status) {
		return true
	}
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusCanceled:
		return true
	default:
		return false
	}
}

func (r *GraphRunner) execute(ctx context.Context, run RunRecord, currentState *state.State, startNodes []string, skip *breakpointSkip, artifacts []state.ArtifactRef) (RunRecord, *state.State, error) {
	invokeCtx, cancelInvoke := context.WithCancel(ctx)
	defer cancelInvoke()
	execution := newGraphRunnerExecution(r, run, currentState, artifacts, skip, cancelInvoke)
	r.registerActiveExecution(run.RunID, execution)
	defer r.unregisterActiveExecution(run.RunID, execution)
	var runnable RunnerRunnable
	var err error
	if compiler, ok := r.runnerGraph().(RunnerRunnableCompiler); ok {
		runnable, err = compiler.CompileRunnableForRunner(execution)
	} else {
		runnable, err = r.runnerGraph().CompileForRunner(execution)
	}
	if err != nil {
		return r.failRun(ctx, run, currentState, "compile_failed", err.Error())
	}

	afterNodes, err := execution.afterInterruptNodes()
	if err != nil {
		return r.failRun(ctx, run, currentState, "config_failed", err.Error())
	}

	config := &langgraph.Config{
		Callbacks: []langgraph.CallbackHandler{
			&runnerGraphCallbacks{execution: execution},
		},
	}
	if len(startNodes) > 0 {
		config.ResumeFrom = append([]string(nil), startNodes...)
	}
	if len(afterNodes) > 0 {
		config.InterruptAfter = afterNodes
	}
	fields := append(runLogFields(run),
		zap.Strings("start_nodes", startNodes),
		zap.Int("breakpoint_count", len(r.Breakpoints)),
		zap.Int("interrupt_after_count", len(afterNodes)),
		zap.Int("artifact_count", len(artifacts)),
	)
	fields = append(fields, state.SummaryFields(currentState)...)
	logger.Info("run executing", fields...)

	finalState, invokeErr := runnable.InvokeWithConfig(invokeCtx, currentState.Clone(), config)
	finalState = execution.stateOrFallback(finalState)
	if run, pausedState, handled, err := r.resolvePendingControl(ctx, execution, finalState, invokeErr); handled || err != nil {
		return run, pausedState, err
	}
	if callbackErr := execution.callbackError(); callbackErr != nil {
		if err := execution.finalizeFailure(ctx, callbackErr); err != nil {
			return RunRecord{}, finalState, err
		}
		return r.failRun(ctx, execution.currentRun(), finalState, "callback_failed", callbackErr.Error())
	}
	if invokeErr == nil {
		return r.completeRun(ctx, execution.currentRun(), finalState)
	}

	var interrupt *langgraph.GraphInterrupt
	if errors.As(invokeErr, &interrupt) {
		return r.handleInterrupt(ctx, execution, finalState, interrupt)
	}

	if err := execution.finalizeFailure(ctx, invokeErr); err != nil {
		return RunRecord{}, finalState, err
	}
	return r.failRun(ctx, execution.currentRun(), finalState, "node_failed", invokeErr.Error())
}

func (r *GraphRunner) resolvePendingControl(ctx context.Context, execution *graphRunnerExecution, currentState *state.State, invokeErr error) (RunRecord, *state.State, bool, error) {
	if control, active := execution.consumePendingControl(); control != nil {
		if control.kind == runnerControlCancel {
			run, finalState, err := r.cancelRun(ctx, execution.currentRun(), currentState)
			return run, finalState, true, err
		}
		if control.checkpointID == "" {
			if control.kind == runnerControlPause && active != nil && errors.Is(invokeErr, context.Canceled) {
				if active.beforeCheckpointID == "" {
					run, finalState, err := r.failRun(ctx, execution.currentRun(), currentState, "interrupt_failed", fmt.Sprintf("pause interrupt missing before checkpoint for %q", active.step.NodeID))
					return run, finalState, true, err
				}
				run, finalState, err := r.pauseRun(ctx, execution.currentRun(), currentState, active.step, active.beforeCheckpointID, control.hit, control.message)
				return run, finalState, true, err
			}
			execution.restorePendingControl(control)
			return RunRecord{}, currentState, false, nil
		}
		switch control.kind {
		case runnerControlPause:
			if control.nodeID != parallelBarrierNodeID {
				completed := execution.consumeLastCompleted(control.nodeID)
				if completed == nil {
					run, finalState, err := r.failRun(ctx, execution.currentRun(), currentState, "interrupt_failed", fmt.Sprintf("pause interrupt missing completed step for %q", control.nodeID))
					return run, finalState, true, err
				}
				run, finalState, err := r.pauseRun(ctx, execution.currentRun(), currentState, completed.step, control.checkpointID, control.hit, control.message)
				return run, finalState, true, err
			}
			run, finalState, err := r.pauseRunAtCheckpoint(ctx, execution.currentRun(), currentState, control.checkpointID, control.hit, control.message)
			return run, finalState, true, err
		}
	}
	return RunRecord{}, currentState, false, nil
}

func (r *GraphRunner) resumeExistingRun(ctx context.Context, run RunRecord, checkpoint RestoredCheckpoint, input *state.State) (RunRecord, *state.State, error) {
	var err error
	if checkpoint.Business, err = state.MergeResumeInput(checkpoint.Business, input); err != nil {
		return RunRecord{}, nil, err
	}

	startNodes, skip, err := r.resumeTarget(ctx, checkpoint.Record, checkpoint.Runtime, checkpoint.Business)
	if err != nil {
		return RunRecord{}, nil, err
	}

	run.Status = RunStatusRunning
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.FinishedAt = nil
	if checkpoint.Runtime.CurrentStepID != "" {
		run.LastStepID = checkpoint.Runtime.CurrentStepID
	}
	if len(startNodes) == 1 && startNodes[0] == langgraph.END {
		now := r.now()
		run.Status = RunStatusCompleted
		run.UpdatedAt = now
		run.FinishedAt = &now
		if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
			return RunRecord{}, nil, err
		}
		logger.Info("resume resolved to completed run", append(runLogFields(run), state.SummaryFields(checkpoint.Business)...)...)
		return run, checkpoint.Business, nil
	}
	run.CurrentNodeID = checkpoint.Runtime.CurrentNodeID
	if checkpoint.Record.Stage != CheckpointBeforeNode || run.CurrentNodeID == "" {
		if len(startNodes) > 0 {
			run.CurrentNodeID = startNodes[0]
		}
	}
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunResumed, map[string]any{
		"checkpoint_id": checkpoint.Record.CheckpointID,
		"node_id":       run.CurrentNodeID,
		"node_ids":      startNodes,
	}); err != nil {
		return RunRecord{}, nil, err
	}

	fields := append(runLogFields(run),
		zap.Strings("start_nodes", startNodes),
		zap.String("resume_checkpoint_id", checkpoint.Record.CheckpointID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	)
	fields = append(fields, state.SummaryFields(checkpoint.Business)...)
	logger.Info("resuming run", fields...)
	return r.execute(ctx, run, checkpoint.Business, startNodes, skip, checkpoint.Artifacts)
}

func (r *GraphRunner) continueRun(ctx context.Context, run RunRecord, checkpoint RestoredCheckpoint, input *state.State) (RunRecord, *state.State, error) {
	continuedState, err := state.PrepareContinuationState(checkpoint.Business, input)
	if err != nil {
		return RunRecord{}, nil, err
	}

	fields := []zap.Field{
		zap.String("run_id", run.RunID),
		zap.String("status", string(run.Status)),
		zap.String("checkpoint_id", checkpoint.Record.CheckpointID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	}
	fields = append(fields, state.SummaryFields(continuedState)...)
	logger.Info("continuing run as new execution", fields...)
	return r.Start(ctx, continuedState)
}

func (r *GraphRunner) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	if r == nil || r.ExecutionStore == nil {
		return RunRecord{}, errors.New("graph runner execution store is nil")
	}
	return r.ExecutionStore.GetRun(ctx, runID)
}

func (r *GraphRunner) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	if r == nil || r.ExecutionStore == nil {
		return nil, errors.New("graph runner execution store is nil")
	}
	return r.ExecutionStore.ListRuns(ctx, filter)
}

func (r *GraphRunner) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	if r == nil || r.ExecutionStore == nil {
		return nil, errors.New("graph runner execution store is nil")
	}
	return r.ExecutionStore.ListSteps(ctx, runID)
}

func (r *GraphRunner) ListCheckpoints(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	if r == nil || r.CheckpointStore == nil {
		return nil, errors.New("graph runner checkpoint store is nil")
	}
	return r.CheckpointStore.List(ctx, runID)
}

func (r *GraphRunner) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	if r == nil || r.ArtifactStore == nil {
		return nil, errors.New("graph runner artifact store is nil")
	}
	return r.ArtifactStore.List(ctx, runID)
}

func (r *GraphRunner) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (Artifact, error) {
	if r == nil || r.ArtifactStore == nil {
		return Artifact{}, errors.New("graph runner artifact store is nil")
	}
	return r.ArtifactStore.Load(ctx, ref)
}

func (r *GraphRunner) LoadCheckpointState(ctx context.Context, checkpointID string) (RestoredCheckpoint, error) {
	if r == nil {
		return RestoredCheckpoint{}, errors.New("graph runner is nil")
	}
	if r.CheckpointStore == nil {
		return RestoredCheckpoint{}, errors.New("graph runner checkpoint store is nil")
	}
	if r.Codec == nil {
		return RestoredCheckpoint{}, errors.New("graph runner state codec is nil")
	}

	record, payload, err := r.CheckpointStore.Load(ctx, checkpointID)
	if err != nil {
		return RestoredCheckpoint{}, err
	}

	snapshot, err := r.Codec.Decode(payload)
	if err != nil {
		return RestoredCheckpoint{}, err
	}
	restored, err := state.RestoreStateSnapshot(snapshot)
	if err != nil {
		return RestoredCheckpoint{}, err
	}

	result := RestoredCheckpoint{
		Record:    record,
		Snapshot:  restored.Snapshot,
		Business:  restored.Business,
		Runtime:   restored.Runtime,
		Artifacts: restored.Artifacts,
	}
	if err := r.validateRestoredCheckpoint(result); err != nil {
		return RestoredCheckpoint{}, err
	}
	return result, nil
}

func (r *GraphRunner) ListEvents(runID string) ([]Event, error) {
	if r == nil || r.EventSink == nil {
		return nil, errors.New("graph runner event sink is nil")
	}
	reader, ok := r.EventSink.(EventReader)
	if !ok {
		return nil, errors.New("graph runner event sink does not support listing events")
	}
	return reader.ListEvents(runID)
}

func (r *GraphRunner) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	if r == nil || r.EventSink == nil {
		return EventPage{}, errors.New("graph runner event sink is nil")
	}
	if reader, ok := r.EventSink.(EventPageReader); ok {
		return reader.ListEventPage(runID, cursor, limit)
	}
	events, err := r.ListEvents(runID)
	if err != nil {
		return EventPage{}, err
	}
	return PaginateEventsNewestFirst(events, cursor, limit)
}

func (r *GraphRunner) Pause(ctx context.Context, runID string) error {
	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	switch run.Status {
	case RunStatusPaused:
		return nil
	case RunStatusPending, RunStatusRunning:
	default:
		return fmt.Errorf("%w: run %q status %q cannot be paused", ErrRunControlNotAllowed, runID, run.Status)
	}
	if run.PauseRequested {
		return nil
	}
	run.PauseRequested = true
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return err
	}
	logger.Info("pause requested", runLogFields(run)...)
	if err := r.publishEvent(ctx, run, "", "", EventRunPauseRequested, nil); err != nil {
		return err
	}
	r.pauseActiveExecution(runID)
	return nil
}

func (r *GraphRunner) Cancel(ctx context.Context, runID string) error {
	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	switch run.Status {
	case RunStatusCanceled:
		return nil
	case RunStatusPaused:
		run.PauseRequested = false
		run.CancelRequested = true
		run.UpdatedAt = r.now()
		if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
			return err
		}
		logger.Info("cancel requested", runLogFields(run)...)
		if err := r.publishEvent(ctx, run, "", "", EventRunCancelRequested, nil); err != nil {
			return err
		}
		_, _, err := r.cancelRun(ctx, run, nil)
		return err
	case RunStatusPending, RunStatusRunning:
	default:
		return fmt.Errorf("%w: run %q status %q cannot be canceled", ErrRunControlNotAllowed, runID, run.Status)
	}
	if run.CancelRequested {
		r.cancelActiveExecution(runID)
		return nil
	}
	run.PauseRequested = false
	run.CancelRequested = true
	run.UpdatedAt = r.now()
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return err
	}
	logger.Info("cancel requested", runLogFields(run)...)
	if err := r.publishEvent(ctx, run, "", "", EventRunCancelRequested, nil); err != nil {
		return err
	}
	r.cancelActiveExecution(runID)
	return nil
}

func (r *GraphRunner) DeleteRun(ctx context.Context, runID string) (RunRecord, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return RunRecord{}, ErrRunnerRecordNotFound
	}
	run, err := r.ExecutionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if isActiveDeleteRunStatus(run.Status) || r.hasActiveExecution(runID) {
		return RunRecord{}, fmt.Errorf("%w: run %q status %q must be stopped before deletion", ErrRunControlNotAllowed, runID, run.Status)
	}
	if r.RunDeleter == nil {
		return RunRecord{}, fmt.Errorf("run deletion is not configured")
	}
	if err := r.RunDeleter.DeleteRun(ctx, runID); err != nil {
		return RunRecord{}, err
	}
	return run, nil
}

func (r *GraphRunner) registerActiveExecution(runID string, execution *graphRunnerExecution) {
	if r == nil || execution == nil || strings.TrimSpace(runID) == "" {
		return
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.activeExecutions == nil {
		r.activeExecutions = map[string]*graphRunnerExecution{}
	}
	r.activeExecutions[runID] = execution
}

func (r *GraphRunner) unregisterActiveExecution(runID string, execution *graphRunnerExecution) {
	if r == nil || strings.TrimSpace(runID) == "" {
		return
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.activeExecutions == nil {
		return
	}
	if execution != nil && r.activeExecutions[runID] != execution {
		return
	}
	delete(r.activeExecutions, runID)
}

func (r *GraphRunner) cancelActiveExecution(runID string) {
	if r == nil || strings.TrimSpace(runID) == "" {
		return
	}
	r.activeMu.Lock()
	execution := r.activeExecutions[runID]
	r.activeMu.Unlock()
	if execution == nil {
		return
	}
	execution.requestCancel()
}

func (r *GraphRunner) hasActiveExecution(runID string) bool {
	if r == nil || strings.TrimSpace(runID) == "" {
		return false
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	_, ok := r.activeExecutions[runID]
	return ok
}

func (r *GraphRunner) pauseActiveExecution(runID string) {
	if r == nil || strings.TrimSpace(runID) == "" {
		return
	}
	r.activeMu.Lock()
	execution := r.activeExecutions[runID]
	r.activeMu.Unlock()
	if execution == nil {
		return
	}
	execution.requestPause()
}

func isActiveDeleteRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusPending, RunStatusRunning:
		return true
	default:
		return false
	}
}

func (r *GraphRunner) handleInterrupt(ctx context.Context, execution *graphRunnerExecution, currentState *state.State, interrupt *langgraph.GraphInterrupt) (RunRecord, *state.State, error) {
	run := execution.currentRun()
	fields := append(runLogFields(run),
		zap.String("interrupt_node_id", interrupt.Node),
		zap.String("interrupt_reason", interrupt.Error()),
	)
	fields = append(fields, state.SummaryFields(currentState)...)
	logger.Info("run interrupt", fields...)

	if control, active := execution.consumePendingControl(); control != nil {
		switch control.kind {
		case runnerControlCancel:
			return r.cancelRun(ctx, run, currentState)
		case runnerControlPause:
			if control.checkpointID != "" {
				return r.pauseRunAtCheckpoint(ctx, run, currentState, control.checkpointID, control.hit, control.message)
			}
			if active == nil {
				return r.failRun(ctx, run, currentState, "interrupt_failed", "pause interrupt missing active step")
			}
			checkpointID := active.beforeCheckpointID
			step := active.step
			if active.beforeInterrupted {
				savedID, err := r.saveCheckpoint(ctx, run, step, step.NodeID, CheckpointBeforeNode, currentState, active.attempts, control.hit, execution.snapshotArtifacts())
				if err != nil {
					return r.failRun(ctx, run, currentState, "checkpoint_failed", err.Error())
				}
				checkpointID = savedID
				step.CheckpointBeforeID = savedID
			}
			return r.pauseRun(ctx, run, currentState, step, checkpointID, control.hit, control.message)
		}
	}

	if hit := r.matchBreakpoint(interrupt.Node, string(CheckpointAfterNode), nil); hit != nil {
		completed := execution.consumeLastCompleted(interrupt.Node)
		if completed == nil {
			return r.failRun(ctx, run, currentState, "interrupt_failed", fmt.Sprintf("after-nodes interrupt missing completed step for %q", interrupt.Node))
		}
		return r.pauseRun(ctx, run, currentState, completed.step, completed.afterCheckpointID, hit, "")
	}

	if completed := execution.consumeLastCompleted(interrupt.Node); completed != nil {
		return r.pauseRun(ctx, run, currentState, completed.step, completed.afterCheckpointID, nil, interrupt.Error())
	}

	return r.failRun(ctx, run, currentState, "interrupt_failed", interrupt.Error())
}

func (r *GraphRunner) completeRun(ctx context.Context, run RunRecord, finalState *state.State) (RunRecord, *state.State, error) {
	now := r.now()
	run.Status = RunStatusCompleted
	run.CurrentNodeID = ""
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, finalState, err
	}
	logger.Info("run completed", append(runLogFields(run), state.SummaryFields(finalState)...)...)
	if err := r.publishEvent(ctx, run, run.LastStepID, "", EventRunFinished, nil); err != nil {
		return RunRecord{}, finalState, err
	}
	return run, finalState, nil
}

func (r *GraphRunner) cancelRun(ctx context.Context, run RunRecord, currentState *state.State) (RunRecord, *state.State, error) {
	now := r.now()
	run.Status = RunStatusCanceled
	run.PauseRequested = false
	run.CancelRequested = false
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, currentState, err
	}
	logger.Info("run canceled", append(runLogFields(run), state.SummaryFields(currentState)...)...)
	if err := r.publishEvent(ctx, run, "", run.CurrentNodeID, EventRunCanceled, nil); err != nil {
		return RunRecord{}, currentState, err
	}
	return run, currentState, nil
}

func (r *GraphRunner) saveCheckpoint(ctx context.Context, run RunRecord, step StepRecord, nodeID string, stage CheckpointStage, currentState *state.State, attempts int, hit *state.BreakpointHit, artifacts []state.ArtifactRef) (string, error) {
	snapshot, err := state.SnapshotFromStateWithRuntime(currentState, state.RuntimeState{
		RunID:           run.RunID,
		CurrentStepID:   step.StepID,
		CurrentNodeID:   nodeID,
		CurrentNodeIDs:  append([]string(nil), run.CurrentNodeIDs...),
		CurrentStepIDs:  append([]string(nil), run.CurrentStepIDs...),
		NextNodeIDs:     append([]string(nil), run.NextNodeIDs...),
		ParallelWaveID:  run.ParallelWaveID,
		WaveID:          step.WaveID,
		Status:          string(run.Status),
		RetryCount:      attempts,
		PauseRequested:  run.PauseRequested,
		CancelRequested: run.CancelRequested,
		BreakpointHit:   hit,
	}, artifacts)
	if err != nil {
		return "", fmt.Errorf("encode checkpoint state: %w", err)
	}
	snapshot.Version = r.Codec.Version()

	payload, err := r.Codec.Encode(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode checkpoint state: %w", err)
	}

	record := CheckpointRecord{
		CheckpointID: newRunnerID(),
		RunID:        run.RunID,
		StepID:       step.StepID,
		NodeID:       nodeID,
		Stage:        stage,
		StateCodec:   r.Codec.Name(),
		StateVersion: r.Codec.Version(),
		CreatedAt:    r.now(),
	}
	if err := r.CheckpointStore.Save(ctx, record, payload); err != nil {
		return "", err
	}
	fields := append(checkpointLogFields(record),
		zap.Int("payload_bytes", len(payload)),
		zap.Int("attempt", attempts),
		zap.Int("artifact_count", len(artifacts)),
	)
	if hit != nil {
		fields = append(fields,
			zap.String("breakpoint_id", hit.BreakpointID),
			zap.String("breakpoint_stage", hit.Stage),
		)
	}
	logger.Debug("checkpoint saved", fields...)
	if err := r.publishEvent(ctx, run, step.StepID, record.NodeID, EventCheckpointCreated, map[string]any{
		"checkpoint_id": record.CheckpointID,
		"stage":         stage,
	}); err != nil {
		return "", err
	}
	return record.CheckpointID, nil
}

func (r *GraphRunner) computeStateDiff(before, after *state.State) ([]state.StateChange, error) {
	beforeSnapshot, err := state.SnapshotFromState(before)
	if err != nil {
		return nil, err
	}
	afterSnapshot, err := state.SnapshotFromState(after)
	if err != nil {
		return nil, err
	}
	return r.Codec.Diff(beforeSnapshot, afterSnapshot)
}

func (r *GraphRunner) publishStateDiffChanges(ctx context.Context, run RunRecord, step StepRecord, changes []state.StateChange) error {
	if len(changes) == 0 {
		return nil
	}
	logger.Debug("state diff computed",
		zap.String("run_id", run.RunID),
		zap.String("step_id", step.StepID),
		zap.String("node_id", step.NodeID),
		zap.Int("change_count", len(changes)),
	)
	return r.publishEvent(ctx, run, step.StepID, step.NodeID, EventStateChanged, map[string]any{
		"changes": changes,
	})
}

func (r *GraphRunner) pauseRun(ctx context.Context, run RunRecord, currentState *state.State, step StepRecord, checkpointID string, hit *state.BreakpointHit, message string) (RunRecord, *state.State, error) {
	now := r.now()
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	step.Status = StepStatusPaused
	step.UpdatedAt = now
	if err := r.ExecutionStore.UpdateStep(ctx, step); err != nil {
		return RunRecord{}, currentState, err
	}
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, currentState, err
	}
	fields := append(runLogFields(run), stepLogFields(step)...)
	fields = append(fields, state.SummaryFields(currentState)...)
	if hit != nil {
		fields = append(fields,
			zap.String("breakpoint_id", hit.BreakpointID),
			zap.String("breakpoint_stage", hit.Stage),
		)
	}
	logger.Info("run paused", fields...)
	if hit != nil {
		if err := r.publishEvent(ctx, run, step.StepID, step.NodeID, EventBreakpointHit, hit); err != nil {
			return RunRecord{}, currentState, err
		}
	}
	if err := r.publishEvent(ctx, run, step.StepID, step.NodeID, EventRunPaused, pauseEventPayload(checkpointID, pauseCheckpointStage(step, checkpointID), step.NodeID, message, hit)); err != nil {
		return RunRecord{}, currentState, err
	}
	return run, currentState, nil
}

func (r *GraphRunner) pauseRunAtCheckpoint(ctx context.Context, run RunRecord, currentState *state.State, checkpointID string, hit *state.BreakpointHit, message string) (RunRecord, *state.State, error) {
	now := r.now()
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, currentState, err
	}
	fields := append(runLogFields(run), state.SummaryFields(currentState)...)
	if hit != nil {
		fields = append(fields,
			zap.String("breakpoint_id", hit.BreakpointID),
			zap.String("breakpoint_stage", hit.Stage),
		)
	}
	logger.Info("run paused", fields...)
	if hit != nil {
		if err := r.publishEvent(ctx, run, "", parallelBarrierNodeID, EventBreakpointHit, hit); err != nil {
			return RunRecord{}, currentState, err
		}
	}
	if err := r.publishEvent(ctx, run, "", parallelBarrierNodeID, EventRunPaused, pauseEventPayload(checkpointID, CheckpointAfterParallelWave, parallelBarrierNodeID, message, hit)); err != nil {
		return RunRecord{}, currentState, err
	}
	return run, currentState, nil
}

func pauseCheckpointStage(step StepRecord, checkpointID string) CheckpointStage {
	if checkpointID != "" && checkpointID == step.CheckpointAfterID {
		return CheckpointAfterNode
	}
	return CheckpointBeforeNode
}

func pauseEventPayload(checkpointID string, stage CheckpointStage, nodeID string, message string, hit *state.BreakpointHit) map[string]any {
	payload := map[string]any{
		"checkpoint_id": checkpointID,
		"stage":         stage,
		"node_id":       nodeID,
	}
	if strings.TrimSpace(message) != "" {
		payload["message"] = strings.TrimSpace(message)
	}
	if hit != nil {
		payload["breakpoint_hit"] = hit
	}
	return payload
}

func (r *GraphRunner) failRun(ctx context.Context, run RunRecord, currentState *state.State, code string, message string) (RunRecord, *state.State, error) {
	now := r.now()
	run.Status = RunStatusFailed
	run.ErrorCode = code
	run.ErrorMessage = message
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.ExecutionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, currentState, err
	}
	logger.Error("run failed", append(runLogFields(run), state.SummaryFields(currentState)...)...)
	if err := r.publishEvent(ctx, run, "", run.CurrentNodeID, EventRunFailed, map[string]any{
		"error_code":    code,
		"error_message": message,
	}); err != nil {
		return RunRecord{}, currentState, err
	}
	return run, currentState, errors.New(message)
}

func (r *GraphRunner) resumeTarget(ctx context.Context, checkpoint CheckpointRecord, runtime state.RuntimeState, currentState *state.State) ([]string, *breakpointSkip, error) {
	switch checkpoint.Stage {
	case CheckpointBeforeNode:
		nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
		if err != nil {
			return nil, nil, err
		}
		return []string{nodeID}, &breakpointSkip{NodeID: checkpoint.NodeID, Stage: string(CheckpointBeforeNode)}, nil
	case CheckpointAfterNode:
		nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
		if err != nil {
			return nil, nil, err
		}
		if r.runnerGraph().IsParallelBranchTarget(nodeID) || runtime.ParallelWaveID != "" || runtime.WaveID != "" {
			return nil, nil, fmt.Errorf("resume from parallel branch %q checkpoint %q is not supported without parallel wave context; resume from after_parallel_wave instead", checkpoint.Stage, checkpoint.CheckpointID)
		}
		nextNodeID, err := r.runnerGraph().ResolveNextNode(ctx, nodeID, currentState)
		if err != nil {
			return nil, nil, err
		}
		return []string{nextNodeID}, nil, nil
	case CheckpointAfterParallelWave:
		if len(runtime.NextNodeIDs) == 0 {
			return nil, nil, fmt.Errorf("parallel barrier checkpoint %q has no next nodes", checkpoint.CheckpointID)
		}
		return append([]string(nil), runtime.NextNodeIDs...), nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported checkpoint stage %q", checkpoint.Stage)
	}
}

func (r *GraphRunner) matchBreakpoint(nodeID string, stage string, skip *breakpointSkip) *state.BreakpointHit {
	if skip != nil && !skip.Consumed && skip.NodeID == nodeID && skip.Stage == stage {
		skip.Consumed = true
		return nil
	}
	for _, breakpoint := range r.Breakpoints {
		if !breakpoint.Enabled {
			continue
		}
		if breakpoint.NodeID != nodeID || breakpoint.Stage != stage {
			continue
		}
		return &state.BreakpointHit{
			BreakpointID: breakpoint.ID,
			NodeID:       breakpoint.NodeID,
			Stage:        breakpoint.Stage,
			HitAt:        r.now(),
		}
	}
	return nil
}

func (r *GraphRunner) publishEvent(ctx context.Context, run RunRecord, stepID string, nodeID string, eventType EventType, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		bytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = bytes
	}
	event := Event{
		ID:        newRunnerID(),
		RunID:     run.RunID,
		StepID:    stepID,
		NodeID:    nodeID,
		Type:      eventType,
		Timestamp: r.now(),
		Payload:   raw,
	}
	if r.EventSink != nil {
		if err := r.EventSink.Publish(ctx, event); err != nil {
			return err
		}
	}
	return observeRunnerContextEvent(ctx, event)
}

func (r *GraphRunner) validate() error {
	if r == nil {
		return errors.New("graph runner is nil")
	}
	graph := r.runnerGraph()
	if graph == nil {
		return errors.New("graph runner graph is nil")
	}
	if err := graph.Validate(); err != nil {
		return err
	}
	if r.ExecutionStore == nil {
		return errors.New("graph runner execution store is nil")
	}
	if r.CheckpointStore == nil {
		return errors.New("graph runner checkpoint store is nil")
	}
	if r.Codec == nil {
		return errors.New("graph runner state codec is nil")
	}
	if r.EventSink == nil {
		return errors.New("graph runner event sink is nil")
	}
	return nil
}

func (r *GraphRunner) publishStartupWarnings(ctx context.Context, run RunRecord) error {
	if r == nil || len(r.StartupWarnings) == 0 {
		return nil
	}
	for _, warning := range r.StartupWarnings {
		if strings.TrimSpace(warning.Message) == "" {
			continue
		}
		fields := append(runLogFields(run),
			zap.String("warning_code", warning.Code),
			zap.String("warning_message", warning.Message),
		)
		if warning.NodeID != "" {
			fields = append(fields, zap.String("node_id", warning.NodeID))
		}
		if warning.OtherNodeID != "" {
			fields = append(fields, zap.String("other_node_id", warning.OtherNodeID))
		}
		if warning.Path != "" {
			fields = append(fields, zap.String("path", warning.Path))
		}
		if len(warning.Sources) > 0 {
			fields = append(fields, zap.Strings("sources", warning.Sources))
		}
		logger.Warn("runner startup warning", fields...)
		if err := r.publishEvent(ctx, run, "", warning.NodeID, EventWarning, warning); err != nil {
			return err
		}
	}
	return nil
}

func (r *GraphRunner) recordArtifact(ctx context.Context, artifact Artifact) (state.ArtifactRef, error) {
	if r == nil || r.ArtifactStore == nil {
		return state.ArtifactRef{}, ErrArtifactRecorderUnavailable
	}

	metadata, _ := RunnerMetadataFromContext(ctx)
	if artifact.RunID == "" {
		artifact.RunID = metadata.RunID
	}
	if artifact.StepID == "" {
		artifact.StepID = metadata.StepID
	}
	if artifact.NodeID == "" {
		artifact.NodeID = metadata.NodeID
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = r.now()
	}
	if artifact.ID == "" {
		artifact.ID = newRunnerID()
	}

	ref, err := r.ArtifactStore.Save(ctx, artifact)
	if err != nil {
		return state.ArtifactRef{}, err
	}
	fields := append(artifactLogFields(ref), zap.Int("bytes", len(artifact.Data)))
	logger.Debug("artifact recorded", fields...)
	if artifact.RunID != "" {
		_ = r.publishEvent(ctx, RunRecord{RunID: artifact.RunID}, artifact.StepID, artifact.NodeID, EventArtifactCreated, map[string]any{
			"artifact_id": ref.ID,
			"type":        ref.Type,
			"mime_type":   ref.MIMEType,
			"location":    ref.Location,
		})
	}
	return ref, nil
}

func (r *GraphRunner) validateRestoredCheckpoint(checkpoint RestoredCheckpoint) error {
	record := checkpoint.Record
	codecName := strings.TrimSpace(record.StateCodec)
	if codecName == "" {
		return fmt.Errorf("checkpoint %q state codec is required", record.CheckpointID)
	}
	if codecName != r.Codec.Name() {
		return fmt.Errorf("checkpoint %q uses state codec %q, runner configured for %q", record.CheckpointID, codecName, r.Codec.Name())
	}
	version := strings.TrimSpace(record.StateVersion)
	if version == "" {
		return fmt.Errorf("checkpoint %q state version is required", record.CheckpointID)
	}
	if version != r.Codec.Version() {
		return fmt.Errorf("checkpoint %q uses state version %q, runner configured for %q", record.CheckpointID, version, r.Codec.Version())
	}
	if checkpoint.Snapshot.Version == "" {
		return fmt.Errorf("checkpoint %q snapshot version is required", record.CheckpointID)
	}
	if version != checkpoint.Snapshot.Version {
		return fmt.Errorf("checkpoint %q state version mismatch: record=%q snapshot=%q", record.CheckpointID, version, checkpoint.Snapshot.Version)
	}
	if record.RunID != "" && checkpoint.Runtime.RunID != "" && record.RunID != checkpoint.Runtime.RunID {
		return fmt.Errorf("checkpoint %q run mismatch: record=%q snapshot=%q", record.CheckpointID, record.RunID, checkpoint.Runtime.RunID)
	}
	if record.StepID != "" && checkpoint.Runtime.CurrentStepID != "" && record.StepID != checkpoint.Runtime.CurrentStepID {
		return fmt.Errorf("checkpoint %q step mismatch: record=%q snapshot=%q", record.CheckpointID, record.StepID, checkpoint.Runtime.CurrentStepID)
	}
	if record.NodeID != "" && checkpoint.Runtime.CurrentNodeID != "" && record.NodeID != checkpoint.Runtime.CurrentNodeID {
		return fmt.Errorf("checkpoint %q nodes mismatch: record=%q snapshot=%q", record.CheckpointID, record.NodeID, checkpoint.Runtime.CurrentNodeID)
	}
	return nil
}

func (r *GraphRunner) graphID() string {
	if text := strings.TrimSpace(r.GraphID); text != "" {
		return text
	}
	return "graph"
}

func (r *GraphRunner) graphVersion() string {
	if text := strings.TrimSpace(r.GraphVersion); text != "" {
		return text
	}
	return DefaultGraphVersion
}

func (r *GraphRunner) graphHash() string {
	return strings.TrimSpace(r.GraphHash)
}

func (r *GraphRunner) validateRunGraphHash(run RunRecord) error {
	expected := r.graphHash()
	if expected == "" {
		return nil
	}
	actual := strings.TrimSpace(run.GraphHash)
	if actual == expected {
		return nil
	}
	return fmt.Errorf("resume run %q: graph hash mismatch: run uses %q, runner uses %q", run.RunID, actual, expected)
}

func (r *GraphRunner) graphSnapshotHash() string {
	return strings.TrimSpace(r.GraphSnapshotHash)
}

func (r *GraphRunner) graphSessionID() string {
	return strings.TrimSpace(r.GraphSessionID)
}

func (r *GraphRunner) nodeName(nodeID string) string {
	graph := r.runnerGraph()
	if graph == nil {
		return nodeID
	}
	return graph.NodeName(nodeID)
}

func (r *GraphRunner) entryPointID() string {
	graph := r.runnerGraph()
	if graph == nil {
		return ""
	}
	return graph.EntryPointID()
}

func (r *GraphRunner) runnerGraph() RunnerGraph {
	if r == nil {
		return nil
	}
	return r.graph
}

func (r *GraphRunner) contractPolicy() ContractPolicy {
	if r == nil {
		return ContractPolicyForMode(core.ContractValidationOff)
	}
	return r.ContractPolicy.Effective(r.ContractValidation)
}

func (r *GraphRunner) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func newRunnerID() string {
	return uuid.NewString()
}

type breakpointSkip struct {
	NodeID   string
	Stage    string
	Consumed bool
}
