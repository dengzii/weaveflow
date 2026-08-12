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
	"go.uber.org/zap"
)

type GraphRunner struct {
	graph              RunnerGraph
	executionStore     ExecutionStore
	checkpointStore    CheckpointStore
	artifactStore      ArtifactStore
	runDeleter         RunDeleter
	codec              state.StateCodec
	eventSink          EventSink
	graphID            string
	graphVersion       string
	graphHash          string
	graphSnapshotHash  string
	graphSessionID     string
	breakpoints        []Breakpoint
	contractValidation core.ContractValidationMode
	contractPolicy     ContractPolicy
	startupWarnings    []WarningRecord
	nodeContracts      map[string]state.Contract
	now                func() time.Time
	eventDiagnosticsMu sync.Mutex
	eventDiagnostics   EventPublicationDiagnostics
	activeMu           sync.Mutex
	activeExecutions   map[string]*graphRunnerExecution
	executionClaims    map[string]struct{}
}

func normalizeRunnerContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type GraphRunnerOption func(*graphRunnerConfig) error

type graphRunnerConfig struct {
	artifactStore      ArtifactStore
	runDeleter         RunDeleter
	graphID            string
	graphVersion       string
	graphHash          string
	graphSnapshotHash  string
	graphSessionID     string
	breakpoints        []Breakpoint
	contractValidation core.ContractValidationMode
	contractPolicy     ContractPolicy
	startupWarnings    []WarningRecord
	nodeContracts      map[string]state.Contract
	now                func() time.Time
}

func NewGraphRunner(graph RunnerGraph, executionStore ExecutionStore, checkpointStore CheckpointStore, codec state.StateCodec, eventSink EventSink, options ...GraphRunnerOption) (*GraphRunner, error) {
	if graph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	if executionStore == nil {
		return nil, fmt.Errorf("execution store is required")
	}
	if checkpointStore == nil {
		return nil, fmt.Errorf("checkpoint store is required")
	}
	if codec == nil {
		return nil, fmt.Errorf("state codec is required")
	}
	if eventSink == nil {
		return nil, fmt.Errorf("event sink is required")
	}
	cfg := graphRunnerConfig{
		contractValidation: core.ContractValidationStrict,
		artifactStore:      NewNoopArtifactStore(),
		now:                time.Now,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	if cfg.artifactStore == nil {
		return nil, fmt.Errorf("artifact store is required")
	}
	if cfg.now == nil {
		return nil, fmt.Errorf("now function is required")
	}
	return &GraphRunner{
		graph:              graph,
		executionStore:     executionStore,
		checkpointStore:    checkpointStore,
		artifactStore:      cfg.artifactStore,
		runDeleter:         cfg.runDeleter,
		codec:              codec,
		eventSink:          eventSink,
		graphID:            strings.TrimSpace(cfg.graphID),
		graphVersion:       strings.TrimSpace(cfg.graphVersion),
		graphHash:          strings.TrimSpace(cfg.graphHash),
		graphSnapshotHash:  strings.TrimSpace(cfg.graphSnapshotHash),
		graphSessionID:     strings.TrimSpace(cfg.graphSessionID),
		breakpoints:        cloneBreakpoints(cfg.breakpoints),
		contractValidation: cfg.contractValidation,
		contractPolicy:     cfg.contractPolicy,
		startupWarnings:    cloneWarnings(cfg.startupWarnings),
		nodeContracts:      cloneContracts(cfg.nodeContracts),
		now:                cfg.now,
		eventDiagnostics: EventPublicationDiagnostics{
			BestEffortFailures: map[EventType]EventPublicationFailure{},
		},
		activeExecutions: make(map[string]*graphRunnerExecution),
		executionClaims:  make(map[string]struct{}),
	}, nil
}

func WithArtifactStore(store ArtifactStore) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if store == nil {
			return fmt.Errorf("artifact store is required")
		}
		cfg.artifactStore = store
		return nil
	}
}

func WithRunDeleter(deleter RunDeleter) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.runDeleter = deleter; return nil }
}

func WithGraphMetadata(id, version, graphHash, snapshotHash, sessionID string) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if value := strings.TrimSpace(id); value != "" {
			cfg.graphID = value
		}
		if value := strings.TrimSpace(version); value != "" {
			cfg.graphVersion = value
		}
		if value := strings.TrimSpace(graphHash); value != "" {
			cfg.graphHash = value
		}
		if value := strings.TrimSpace(snapshotHash); value != "" {
			cfg.graphSnapshotHash = value
		}
		if value := strings.TrimSpace(sessionID); value != "" {
			cfg.graphSessionID = value
		}
		return nil
	}
}

func WithBreakpoints(breakpoints ...Breakpoint) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		cfg.breakpoints = append([]Breakpoint(nil), breakpoints...)
		return nil
	}
}

func WithContractValidation(mode core.ContractValidationMode) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.contractValidation = mode; return nil }
}

func WithContractPolicy(policy ContractPolicy) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.contractPolicy = policy; return nil }
}

func WithStartupWarnings(warnings []WarningRecord) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		cfg.startupWarnings = append([]WarningRecord(nil), warnings...)
		return nil
	}
}

func WithNodeContracts(contracts map[string]state.Contract) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.nodeContracts = cloneContracts(contracts); return nil }
}

func WithNow(now func() time.Time) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if now == nil {
			return fmt.Errorf("now function is required")
		}
		cfg.now = now
		return nil
	}
}

func cloneBreakpoints(items []Breakpoint) []Breakpoint { return append([]Breakpoint(nil), items...) }

func cloneWarnings(items []WarningRecord) []WarningRecord {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]WarningRecord, len(items))
	for index, item := range items {
		cloned[index] = item
		cloned[index].Sources = append([]string(nil), item.Sources...)
	}
	return cloned
}

func cloneContracts(items map[string]state.Contract) map[string]state.Contract {
	if len(items) == 0 {
		return nil
	}
	cloned := make(map[string]state.Contract, len(items))
	for key, item := range items {
		cloned[key] = item.Clone()
	}
	return cloned
}

func (r *GraphRunner) ExecutionStore() ExecutionStore {
	if r == nil {
		return nil
	}
	return r.executionStore
}
func (r *GraphRunner) CheckpointStore() CheckpointStore {
	if r == nil {
		return nil
	}
	return r.checkpointStore
}
func (r *GraphRunner) ArtifactStore() ArtifactStore {
	if r == nil {
		return nil
	}
	return r.artifactStore
}
func (r *GraphRunner) EventSink() EventSink {
	if r == nil {
		return nil
	}
	return r.eventSink
}
func (r *GraphRunner) GraphID() string {
	if r == nil {
		return ""
	}
	return r.graphID
}
func (r *GraphRunner) GraphVersion() string {
	if r == nil {
		return ""
	}
	return r.graphVersion
}
func (r *GraphRunner) GraphHash() string {
	if r == nil {
		return ""
	}
	return r.graphHash
}
func (r *GraphRunner) GraphSnapshotHash() string {
	if r == nil {
		return ""
	}
	return r.graphSnapshotHash
}
func (r *GraphRunner) GraphSessionID() string {
	if r == nil {
		return ""
	}
	return r.graphSessionID
}
func (r *GraphRunner) Breakpoints() []Breakpoint {
	if r == nil {
		return nil
	}
	return cloneBreakpoints(r.breakpoints)
}
func (r *GraphRunner) ContractValidation() core.ContractValidationMode {
	if r == nil {
		return core.ContractValidationOff
	}
	return r.contractValidation
}
func (r *GraphRunner) ContractPolicy() ContractPolicy {
	if r == nil {
		return ContractPolicy{}
	}
	return r.contractPolicy
}
func (r *GraphRunner) StartupWarnings() []WarningRecord {
	if r == nil {
		return nil
	}
	return cloneWarnings(r.startupWarnings)
}
func (r *GraphRunner) NodeContracts() map[string]state.Contract {
	if r == nil {
		return nil
	}
	return cloneContracts(r.nodeContracts)
}

func (r *GraphRunner) Start(ctx context.Context, initialState *state.State) (RunRecord, *state.State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
	if err := r.reserveExecution(run.RunID); err != nil {
		return RunRecord{}, nil, r.abortStartedRun(ctx, run, "async_execution_reservation_failed", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer r.releaseExecutionClaim(run.RunID)
		defer func() {
			if recovered := recover(); recovered != nil {
				r.failAsyncExecution(context.WithoutCancel(ctx), run, initialState, "async_execution_panic", fmt.Sprintf("panic: %v", recovered))
			}
		}()
		finishedRun, finalState, runErr := r.continueStartedRun(ctx, run, initialState)
		if runErr != nil && finishedRun.RunID == "" {
			if finalState == nil {
				finalState = initialState
			}
			r.failAsyncExecution(context.WithoutCancel(ctx), run, finalState, "async_execution_failed", runErr.Error())
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

	now := r.currentTime()
	entryPoint := r.entryPointID()
	run := RunRecord{
		RunID:             newRunnerID(),
		GraphID:           r.resolvedGraphID(),
		GraphVersion:      r.resolvedGraphVersion(),
		GraphHash:         r.resolvedGraphHash(),
		GraphSnapshotHash: r.resolvedGraphSnapshotHash(),
		GraphSessionID:    r.resolvedGraphSessionID(),
		Status:            RunStatusPending,
		EntryNodeID:       entryPoint,
		StartedAt:         now,
		UpdatedAt:         now,
	}
	if origin, ok := RunOriginFromContext(ctx); ok {
		originCopy := origin
		run.Origin = &originCopy
	}
	if err := r.executionStore.CreateRun(ctx, run); err != nil {
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
		return RunRecord{}, initialState, r.abortStartedRun(ctx, run, "run_created_event_failed", err)
	}

	run.Status = RunStatusRunning
	run.CurrentNodeID = run.EntryNodeID
	run.UpdatedAt = r.currentTime()
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, initialState, r.abortStartedRun(ctx, run, "run_start_persistence_failed", err)
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunStarted, nil); err != nil {
		return RunRecord{}, initialState, r.abortStartedRun(ctx, run, "run_started_event_failed", err)
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validate(); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.claimExecution(runID); err != nil {
		return RunRecord{}, nil, err
	}
	defer r.releaseExecutionClaim(runID)

	run, err := r.executionStore.GetRun(ctx, runID)
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
	if ctx == nil {
		ctx = context.Background()
	}
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
	if err := r.claimExecution(checkpoint.Record.RunID); err != nil {
		return RunRecord{}, nil, err
	}
	defer r.releaseExecutionClaim(checkpoint.Record.RunID)

	run, err := r.executionStore.GetRun(ctx, checkpoint.Record.RunID)
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
	runnable, err := r.runnerGraph().CompileForRunner(execution)
	if err != nil {
		return r.failRun(ctx, run, currentState, "compile_failed", err.Error())
	}

	afterNodes, err := execution.afterInterruptNodes()
	if err != nil {
		return r.failRun(ctx, run, currentState, "config_failed", err.Error())
	}

	config := SchedulerConfig{}
	config.StepObserver = func(ctx context.Context, stepNodeID string, currentState *state.State) error {
		err := execution.OnGraphStep(ctx, stepNodeID, currentState)
		if err != nil {
			execution.recordCallbackError(err)
		}
		return err
	}
	if len(startNodes) > 0 {
		config.StartNodeIDs = append([]string(nil), startNodes...)
	}
	if len(afterNodes) > 0 {
		config.InterruptAfterNodeIDs = afterNodes
	}
	fields := append(runLogFields(run),
		zap.Strings("start_nodes", startNodes),
		zap.Int("breakpoint_count", len(r.breakpoints)),
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
	if execution.currentRun().CancelRequested {
		if err := execution.finalizeCanceledSteps(ctx); err != nil {
			return r.failRun(ctx, execution.currentRun(), finalState, "cancel_finalize_failed", err.Error())
		}
		return r.cancelRun(ctx, execution.currentRun(), finalState)
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

	var interrupt *GraphInterrupt
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
			if err := execution.finalizeCanceledSteps(ctx); err != nil {
				run, finalState, failErr := r.failRun(ctx, execution.currentRun(), currentState, "cancel_finalize_failed", err.Error())
				return run, finalState, true, failErr
			}
			run, finalState, err := r.cancelRun(ctx, execution.currentRun(), currentState)
			return run, finalState, true, err
		}
		if control.checkpointID == "" {
			if control.kind == runnerControlPause && active != nil && pauseControlCanceledInvoke(control, invokeErr) {
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

func pauseControlCanceledInvoke(control *runnerPendingControl, invokeErr error) bool {
	if control == nil || control.kind != runnerControlPause {
		return false
	}
	if errors.Is(invokeErr, context.Canceled) {
		return true
	}
	return control.message == "pause requested"
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
	resolvedToEnd := len(startNodes) == 1 && startNodes[0] == EndNodeID
	run.CurrentNodeID = checkpoint.Runtime.CurrentNodeID
	if checkpoint.Record.Stage != CheckpointBeforeNode || run.CurrentNodeID == "" {
		if len(startNodes) > 0 {
			run.CurrentNodeID = startNodes[0]
		}
	}
	if resolvedToEnd {
		clearRunExecutionPointers(&run)
	}
	run.UpdatedAt = r.currentTime()
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.publishEvent(ctx, run, "", "", EventRunResumed, map[string]any{
		"checkpoint_id": checkpoint.Record.CheckpointID,
		"node_id":       run.CurrentNodeID,
		"node_ids":      startNodes,
	}); err != nil {
		return RunRecord{}, nil, err
	}
	if resolvedToEnd {
		logger.Info("resume resolved to completed run", append(runLogFields(run), state.SummaryFields(checkpoint.Business)...)...)
		return r.completeRun(ctx, run, checkpoint.Business)
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
	if r == nil || r.executionStore == nil {
		return RunRecord{}, errors.New("graph runner execution store is nil")
	}
	return r.executionStore.GetRun(normalizeRunnerContext(ctx), runID)
}

func (r *GraphRunner) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	if r == nil || r.executionStore == nil {
		return nil, errors.New("graph runner execution store is nil")
	}
	return r.executionStore.ListRuns(normalizeRunnerContext(ctx), filter)
}

func (r *GraphRunner) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	if r == nil || r.executionStore == nil {
		return nil, errors.New("graph runner execution store is nil")
	}
	return r.executionStore.ListSteps(normalizeRunnerContext(ctx), runID)
}

func (r *GraphRunner) ListCheckpoints(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	if r == nil || r.checkpointStore == nil {
		return nil, errors.New("graph runner checkpoint store is nil")
	}
	return r.checkpointStore.List(normalizeRunnerContext(ctx), runID)
}

func (r *GraphRunner) ListArtifacts(ctx context.Context, runID string) ([]state.ArtifactRef, error) {
	if r == nil || r.artifactStore == nil {
		return nil, errors.New("graph runner artifact store is nil")
	}
	return r.artifactStore.List(normalizeRunnerContext(ctx), runID)
}

func (r *GraphRunner) LoadArtifact(ctx context.Context, ref state.ArtifactRef) (Artifact, error) {
	if r == nil || r.artifactStore == nil {
		return Artifact{}, errors.New("graph runner artifact store is nil")
	}
	return r.artifactStore.Load(normalizeRunnerContext(ctx), ref)
}

func (r *GraphRunner) LoadCheckpointState(ctx context.Context, checkpointID string) (RestoredCheckpoint, error) {
	if r == nil {
		return RestoredCheckpoint{}, errors.New("graph runner is nil")
	}
	if r.checkpointStore == nil {
		return RestoredCheckpoint{}, errors.New("graph runner checkpoint store is nil")
	}
	if r.codec == nil {
		return RestoredCheckpoint{}, errors.New("graph runner state codec is nil")
	}

	record, payload, err := r.checkpointStore.Load(normalizeRunnerContext(ctx), checkpointID)
	if err != nil {
		return RestoredCheckpoint{}, err
	}

	snapshot, err := r.codec.Decode(payload)
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
	if r == nil || r.eventSink == nil {
		return nil, errors.New("graph runner event sink is nil")
	}
	reader, ok := r.eventSink.(EventReader)
	if !ok {
		return nil, errors.New("graph runner event sink does not support listing events")
	}
	return reader.ListEvents(runID)
}

func (r *GraphRunner) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	if r == nil || r.eventSink == nil {
		return EventPage{}, errors.New("graph runner event sink is nil")
	}
	if reader, ok := r.eventSink.(EventPageReader); ok {
		return reader.ListEventPage(runID, cursor, limit)
	}
	events, err := r.ListEvents(runID)
	if err != nil {
		return EventPage{}, err
	}
	return PaginateEventsNewestFirst(events, cursor, limit)
}

func (r *GraphRunner) Pause(ctx context.Context, runID string) error {
	if r == nil || r.executionStore == nil {
		return errors.New("graph runner execution store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := r.executionStore.GetRun(ctx, runID)
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
	execution := r.activeExecution(runID)
	if execution == nil {
		if r.hasExecutionClaim(runID) {
			run, err = r.persistReservedControlRequest(ctx, runID, runnerControlPause)
			if err != nil {
				return err
			}
			logger.Info("pause requested during execution startup", runLogFields(run)...)
			return r.publishEvent(ctx, run, "", "", EventRunPauseRequested, nil)
		}
		return fmt.Errorf("%w: run %q has no active execution", ErrRunControlNotAllowed, runID)
	}
	if run.PauseRequested {
		execution.requestPause()
		return nil
	}
	run, err = execution.persistControlRequest(ctx, runnerControlPause)
	if err != nil {
		return err
	}
	logger.Info("pause requested", runLogFields(run)...)
	if err := r.publishEvent(ctx, run, "", "", EventRunPauseRequested, nil); err != nil {
		return err
	}
	execution.requestPause()
	return nil
}

func (r *GraphRunner) Cancel(ctx context.Context, runID string) error {
	if r == nil || r.executionStore == nil {
		return errors.New("graph runner execution store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := r.executionStore.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	switch run.Status {
	case RunStatusCanceled:
		return nil
	case RunStatusPaused:
		run.PauseRequested = false
		run.CancelRequested = true
		run.UpdatedAt = r.currentTime()
		if err := r.executionStore.UpdateRun(ctx, run); err != nil {
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
	execution := r.activeExecution(runID)
	if execution == nil {
		if r.hasExecutionClaim(runID) {
			run, err = r.persistReservedControlRequest(ctx, runID, runnerControlCancel)
			if err != nil {
				return err
			}
			logger.Info("cancel requested during execution startup", runLogFields(run)...)
			return r.publishEvent(ctx, run, "", "", EventRunCancelRequested, nil)
		}
		return fmt.Errorf("%w: run %q has no active execution", ErrRunControlNotAllowed, runID)
	}
	if run.CancelRequested {
		execution.requestCancel()
		return nil
	}
	run, err = execution.persistControlRequest(ctx, runnerControlCancel)
	if err != nil {
		return err
	}
	logger.Info("cancel requested", runLogFields(run)...)
	if err := r.publishEvent(ctx, run, "", "", EventRunCancelRequested, nil); err != nil {
		return err
	}
	execution.requestCancel()
	return nil
}

func (r *GraphRunner) DeleteRun(ctx context.Context, runID string) (RunRecord, error) {
	ctx = normalizeRunnerContext(ctx)
	if err := r.validate(); err != nil {
		return RunRecord{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return RunRecord{}, ErrRunnerRecordNotFound
	}
	run, err := r.executionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if isActiveDeleteRunStatus(run.Status) || r.hasExecutionClaim(runID) {
		return RunRecord{}, fmt.Errorf("%w: run %q status %q must be stopped before deletion", ErrRunControlNotAllowed, runID, run.Status)
	}
	if r.runDeleter == nil {
		return RunRecord{}, fmt.Errorf("run deletion is not configured")
	}
	if err := r.runDeleter.DeleteRun(ctx, runID); err != nil {
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

func (r *GraphRunner) reserveExecution(runID string) error {
	if r == nil || strings.TrimSpace(runID) == "" {
		return ErrRunControlNotAllowed
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.activeExecutions != nil && r.activeExecutions[runID] != nil {
		return fmt.Errorf("%w: run %q already has an active execution", ErrRunControlNotAllowed, runID)
	}
	if r.executionClaims == nil {
		r.executionClaims = map[string]struct{}{}
	}
	if _, exists := r.executionClaims[runID]; exists {
		return fmt.Errorf("%w: run %q already has an execution reservation", ErrRunControlNotAllowed, runID)
	}
	r.executionClaims[runID] = struct{}{}
	return nil
}

func (r *GraphRunner) claimExecution(runID string) error {
	if r == nil || strings.TrimSpace(runID) == "" {
		return ErrRunControlNotAllowed
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.activeExecutions != nil && r.activeExecutions[runID] != nil {
		return fmt.Errorf("%w: run %q already has an active execution", ErrRunControlNotAllowed, runID)
	}
	if r.executionClaims == nil {
		r.executionClaims = map[string]struct{}{}
	}
	if _, exists := r.executionClaims[runID]; exists {
		return fmt.Errorf("%w: run %q is already being resumed", ErrRunControlNotAllowed, runID)
	}
	r.executionClaims[runID] = struct{}{}
	return nil
}

func (r *GraphRunner) persistReservedControlRequest(ctx context.Context, runID string, kind runnerControlKind) (RunRecord, error) {
	ctx = normalizeRunnerContext(ctx)
	run, err := r.executionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if !isActiveDeleteRunStatus(run.Status) {
		return run, fmt.Errorf("%w: run %q status %q cannot be controlled", ErrRunControlNotAllowed, runID, run.Status)
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
	run.UpdatedAt = r.currentTime()
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	return run, nil
}

func (r *GraphRunner) releaseExecutionClaim(runID string) {
	if r == nil {
		return
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	delete(r.executionClaims, runID)
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

func (r *GraphRunner) activeExecution(runID string) *graphRunnerExecution {
	if r == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	return r.activeExecutions[runID]
}

func (r *GraphRunner) hasActiveExecution(runID string) bool {
	return r.activeExecution(runID) != nil
}

func (r *GraphRunner) hasExecutionClaim(runID string) bool {
	if r == nil || strings.TrimSpace(runID) == "" {
		return false
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.activeExecutions != nil && r.activeExecutions[runID] != nil {
		return true
	}
	_, claimed := r.executionClaims[runID]
	return claimed
}

func (r *GraphRunner) IsRunActive(runID string) bool {
	return r.hasActiveExecution(runID)
}

func (r *GraphRunner) ActiveRunCount() int {
	if r == nil {
		return 0
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	return len(r.activeExecutions)
}

func (r *GraphRunner) MarkRunExecutionLost(ctx context.Context, runID string) (RunRecord, error) {
	if r == nil || r.executionStore == nil {
		return RunRecord{}, errors.New("graph runner execution store is nil")
	}
	ctx = normalizeRunnerContext(ctx)
	run, err := r.executionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if run.Status != RunStatusPending && run.Status != RunStatusRunning {
		return run, nil
	}
	if r.hasExecutionClaim(runID) {
		return run, fmt.Errorf("%w: run %q still has an active execution", ErrRunControlNotAllowed, runID)
	}
	return r.persistRunFailure(ctx, run, nil, "run_execution_lost", "run execution is no longer active in this server process")
}

func isActiveDeleteRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusPending, RunStatusRunning:
		return true
	default:
		return false
	}
}

func (r *GraphRunner) handleInterrupt(ctx context.Context, execution *graphRunnerExecution, currentState *state.State, interrupt *GraphInterrupt) (RunRecord, *state.State, error) {
	run := execution.currentRun()
	fields := append(runLogFields(run),
		zap.String("interrupt_node_id", interrupt.NodeID),
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

	if hit := r.matchBreakpoint(interrupt.NodeID, string(CheckpointAfterNode), nil); hit != nil {
		completed := execution.consumeLastCompleted(interrupt.NodeID)
		if completed == nil {
			return r.failRun(ctx, run, currentState, "interrupt_failed", fmt.Sprintf("after-node interrupt missing completed step for %q", interrupt.NodeID))
		}
		return r.pauseRun(ctx, run, currentState, completed.step, completed.afterCheckpointID, hit, "")
	}

	if completed := execution.consumeLastCompleted(interrupt.NodeID); completed != nil {
		return r.pauseRun(ctx, run, currentState, completed.step, completed.afterCheckpointID, nil, interrupt.Error())
	}

	return r.failRun(ctx, run, currentState, "interrupt_failed", interrupt.Error())
}

func (r *GraphRunner) completeRun(ctx context.Context, run RunRecord, finalState *state.State) (RunRecord, *state.State, error) {
	now := r.currentTime()
	run.Status = RunStatusCompleted
	run.PauseRequested = false
	run.CancelRequested = false
	clearRunExecutionPointers(&run)
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, finalState, err
	}
	logger.Info("run completed", append(runLogFields(run), state.SummaryFields(finalState)...)...)
	if err := r.publishEvent(ctx, run, run.LastStepID, "", EventRunFinished, nil); err != nil {
		return run, finalState, err
	}
	return run, finalState, nil
}

func clearRunExecutionPointers(run *RunRecord) {
	run.CurrentNodeID = ""
	run.CurrentNodeIDs = nil
	run.CurrentStepIDs = nil
	run.NextNodeIDs = nil
	run.ParallelWaveID = ""
}

func (r *GraphRunner) cancelRun(ctx context.Context, run RunRecord, currentState *state.State) (RunRecord, *state.State, error) {
	now := r.currentTime()
	run.Status = RunStatusCanceled
	run.PauseRequested = false
	run.CancelRequested = false
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
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
	snapshot.Version = r.codec.Version()

	payload, err := r.codec.Encode(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode checkpoint state: %w", err)
	}

	record := CheckpointRecord{
		CheckpointID: newRunnerID(),
		RunID:        run.RunID,
		StepID:       step.StepID,
		NodeID:       nodeID,
		Stage:        stage,
		StateCodec:   r.codec.Name(),
		StateVersion: r.codec.Version(),
		CreatedAt:    r.currentTime(),
	}
	if err := r.checkpointStore.Save(ctx, record, payload); err != nil {
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
	return r.codec.Diff(beforeSnapshot, afterSnapshot)
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
	now := r.currentTime()
	originalStep := step
	stage := pauseCheckpointStage(step, checkpointID)
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	stepUpdated := stage != CheckpointAfterNode
	if stepUpdated {
		step.Status = StepStatusPaused
		step.UpdatedAt = now
		if err := r.executionStore.UpdateStep(ctx, step); err != nil {
			return RunRecord{}, currentState, err
		}
	}
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
		var rollbackErr error
		if stepUpdated {
			rollbackErr = r.executionStore.UpdateStep(context.WithoutCancel(normalizeRunnerContext(ctx)), originalStep)
		}
		return RunRecord{}, currentState, errors.Join(err, rollbackErr)
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
	if err := r.publishEvent(ctx, run, step.StepID, step.NodeID, EventRunPaused, pauseEventPayload(checkpointID, stage, step.NodeID, message, hit)); err != nil {
		return RunRecord{}, currentState, err
	}
	return run, currentState, nil
}

func (r *GraphRunner) pauseRunAtCheckpoint(ctx context.Context, run RunRecord, currentState *state.State, checkpointID string, hit *state.BreakpointHit, message string) (RunRecord, *state.State, error) {
	now := r.currentTime()
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
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
	failedRun, err := r.persistRunFailure(ctx, run, currentState, code, message)
	if err != nil {
		return RunRecord{}, currentState, err
	}
	return failedRun, currentState, errors.New(message)
}

func (r *GraphRunner) persistRunFailure(ctx context.Context, run RunRecord, currentState *state.State, code string, message string) (RunRecord, error) {
	now := r.currentTime()
	run.Status = RunStatusFailed
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = code
	run.ErrorMessage = message
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := r.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	logger.Error("run failed", append(runLogFields(run), state.SummaryFields(currentState)...)...)
	if err := r.publishEvent(ctx, run, "", run.CurrentNodeID, EventRunFailed, map[string]any{
		"error_code":    code,
		"error_message": message,
	}); err != nil {
		return run, err
	}
	return run, nil
}

func (r *GraphRunner) failAsyncExecution(ctx context.Context, run RunRecord, currentState *state.State, code, message string) {
	if r == nil || r.executionStore == nil {
		return
	}
	latest, err := r.executionStore.GetRun(ctx, run.RunID)
	if err != nil || !isActiveDeleteRunStatus(latest.Status) {
		return
	}
	_, _, _ = r.failRun(ctx, latest, currentState, code, message)
}

func (r *GraphRunner) abortStartedRun(ctx context.Context, run RunRecord, code string, cause error) error {
	if r == nil || r.executionStore == nil {
		return cause
	}
	failureCtx := context.WithoutCancel(normalizeRunnerContext(ctx))
	now := r.currentTime()
	run.Status = RunStatusFailed
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = code
	run.ErrorMessage = cause.Error()
	run.UpdatedAt = now
	run.FinishedAt = &now
	updateErr := r.executionStore.UpdateRun(failureCtx, run)
	publishErr := r.publishEvent(failureCtx, run, "", run.CurrentNodeID, EventRunFailed, map[string]any{
		"error_code":    code,
		"error_message": cause.Error(),
	})
	return errors.Join(cause, updateErr, publishErr)
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
	for _, breakpoint := range r.breakpoints {
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
			HitAt:        r.currentTime(),
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
		ID:             newRunnerID(),
		GraphID:        firstNonEmpty(run.GraphID, r.resolvedGraphID()),
		GraphSessionID: firstNonEmpty(run.GraphSessionID, r.resolvedGraphSessionID()),
		RunID:          run.RunID,
		StepID:         stepID,
		NodeID:         nodeID,
		Type:           eventType,
		Timestamp:      r.currentTime(),
		Payload:        raw,
	}
	if r.eventSink != nil {
		if err := r.eventSink.Publish(ctx, event); err != nil {
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
	if r.executionStore == nil {
		return errors.New("graph runner execution store is nil")
	}
	if r.checkpointStore == nil {
		return errors.New("graph runner checkpoint store is nil")
	}
	if r.codec == nil {
		return errors.New("graph runner state codec is nil")
	}
	if r.eventSink == nil {
		return errors.New("graph runner event sink is nil")
	}
	return nil
}

func (r *GraphRunner) publishStartupWarnings(ctx context.Context, run RunRecord) error {
	if r == nil || len(r.startupWarnings) == 0 {
		return nil
	}
	for _, warning := range r.startupWarnings {
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
	if r == nil || r.artifactStore == nil {
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
		artifact.CreatedAt = r.currentTime()
	}
	if artifact.ID == "" {
		artifact.ID = newRunnerID()
	}

	ref, err := r.artifactStore.Save(ctx, artifact)
	if err != nil {
		return state.ArtifactRef{}, err
	}
	fields := append(artifactLogFields(ref), zap.Int("bytes", len(artifact.Data)))
	logger.Debug("artifact recorded", fields...)
	if artifact.RunID != "" {
		r.publishBestEffortEvent(ctx, RunRecord{RunID: artifact.RunID}, artifact.StepID, artifact.NodeID, EventArtifactCreated, map[string]any{
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
	if codecName != r.codec.Name() {
		return fmt.Errorf("checkpoint %q uses state codec %q, runner configured for %q", record.CheckpointID, codecName, r.codec.Name())
	}
	version := strings.TrimSpace(record.StateVersion)
	if version == "" {
		return fmt.Errorf("checkpoint %q state version is required", record.CheckpointID)
	}
	if version != r.codec.Version() {
		return fmt.Errorf("checkpoint %q uses state version %q, runner configured for %q", record.CheckpointID, version, r.codec.Version())
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

func (r *GraphRunner) resolvedGraphID() string {
	if text := strings.TrimSpace(r.graphID); text != "" {
		return text
	}
	return "graph"
}

func (r *GraphRunner) resolvedGraphVersion() string {
	if text := strings.TrimSpace(r.graphVersion); text != "" {
		return text
	}
	return DefaultGraphVersion
}

func (r *GraphRunner) resolvedGraphHash() string {
	return strings.TrimSpace(r.graphHash)
}

func (r *GraphRunner) validateRunGraphHash(run RunRecord) error {
	expected := r.resolvedGraphHash()
	if expected == "" {
		return nil
	}
	actual := strings.TrimSpace(run.GraphHash)
	if actual == expected {
		return nil
	}
	return fmt.Errorf("resume run %q: graph hash mismatch: run uses %q, runner uses %q", run.RunID, actual, expected)
}

func (r *GraphRunner) resolvedGraphSnapshotHash() string {
	return strings.TrimSpace(r.graphSnapshotHash)
}

func (r *GraphRunner) resolvedGraphSessionID() string {
	return strings.TrimSpace(r.graphSessionID)
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

func (r *GraphRunner) effectiveContractPolicy() ContractPolicy {
	if r == nil {
		return ContractPolicyForMode(core.ContractValidationOff)
	}
	return r.contractPolicy.Effective(r.contractValidation)
}

func (r *GraphRunner) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now()
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
