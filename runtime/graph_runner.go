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
	retentionPolicy    RunRetentionPolicy
	retentionAudit     RetentionAuditSink
	retentionMu        sync.Mutex
	codec              state.StateCodec
	eventSink          EventSink
	transactionStore   RuntimeTransactionStore
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
	stateSchemas       map[string]state.JSONSchema
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
	retentionPolicy    RunRetentionPolicy
	retentionAudit     RetentionAuditSink
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
	stateSchemas       map[string]state.JSONSchema
	transactionStore   RuntimeTransactionStore
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
	transactionStore := cfg.transactionStore
	if transactionStore == nil {
		var err error
		transactionStore, err = resolveRuntimeTransactionStore(executionStore, checkpointStore, eventSink)
		if err != nil {
			return nil, fmt.Errorf("initialize runtime transaction store: %w", err)
		}
	}
	if err := validateRunRetentionPolicy(cfg.retentionPolicy); err != nil {
		return nil, err
	}
	if (cfg.retentionPolicy.MaxRuns > 0 || cfg.retentionPolicy.MaxAge > 0) && (cfg.runDeleter == nil || cfg.retentionAudit == nil) {
		return nil, fmt.Errorf("run retention requires a run deleter and audit sink")
	}
	return &GraphRunner{
		graph:              graph,
		executionStore:     executionStore,
		checkpointStore:    checkpointStore,
		artifactStore:      cfg.artifactStore,
		runDeleter:         cfg.runDeleter,
		retentionPolicy:    cfg.retentionPolicy,
		retentionAudit:     cfg.retentionAudit,
		codec:              codec,
		eventSink:          eventSink,
		transactionStore:   transactionStore,
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
		stateSchemas:       cloneSchemas(cfg.stateSchemas),
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

func WithRunRetention(policy RunRetentionPolicy, audit RetentionAuditSink) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if err := validateRunRetentionPolicy(policy); err != nil {
			return err
		}
		cfg.retentionPolicy = policy
		cfg.retentionAudit = audit
		return nil
	}
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

func WithStateSchemas(schemas map[string]state.JSONSchema) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.stateSchemas = cloneSchemas(schemas); return nil }
}

func WithRuntimeTransactionStore(store RuntimeTransactionStore) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if store == nil {
			return fmt.Errorf("runtime transaction store is required")
		}
		cfg.transactionStore = store
		return nil
	}
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

func cloneSchemas(items map[string]state.JSONSchema) map[string]state.JSONSchema {
	if len(items) == 0 {
		return nil
	}
	cloned := make(map[string]state.JSONSchema, len(items))
	for path, schema := range items {
		cloned[path] = schema.Clone()
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

func (r *GraphRunner) TransactionStore() RuntimeTransactionStore {
	if r == nil {
		return nil
	}
	return r.transactionStore
}

func (r *GraphRunner) StateCodec() state.StateCodec {
	if r == nil {
		return nil
	}
	return r.codec
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

func (r *GraphRunner) StateSchemas() map[string]state.JSONSchema {
	if r == nil {
		return nil
	}
	return cloneSchemas(r.stateSchemas)
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

func (r *GraphRunner) RunChild(ctx context.Context, request ChildRunRequest, input *state.State) (ChildRunResult, error) {
	ctx = normalizeRunnerContext(ctx)
	request.ParentRunID = strings.TrimSpace(request.ParentRunID)
	request.ParentStepID = strings.TrimSpace(request.ParentStepID)
	request.ParentTaskID = strings.TrimSpace(request.ParentTaskID)
	request.GraphRef = strings.TrimSpace(request.GraphRef)
	request.Namespace = strings.Trim(strings.TrimSpace(request.Namespace), "/")
	if request.ParentRunID == "" || request.ParentStepID == "" || request.ParentTaskID == "" || request.GraphRef == "" {
		return ChildRunResult{}, fmt.Errorf("child run requires parent_run_id, parent_step_id, parent_task_id, and graph_ref")
	}
	parentRun, err := r.executionStore.GetRun(ctx, request.ParentRunID)
	if err != nil {
		return ChildRunResult{}, fmt.Errorf("load parent run %q: %w", request.ParentRunID, err)
	}
	if request.Namespace == "" {
		request.Namespace = strings.Trim(parentRun.Namespace+"/"+request.GraphRef+":"+request.ParentTaskID, "/")
	}
	lineage := ChildRunLineage{
		ParentRunID: request.ParentRunID, ParentStepID: request.ParentStepID, ParentTaskID: request.ParentTaskID,
		RootRunID: parentRun.RootRunID, ParentRunPath: append([]string(nil), parentRun.RunPath...), Namespace: request.Namespace,
	}
	childCtx := WithGraphRunner(ctx, r)
	childCtx = WithChildRunLineage(childCtx, lineage)

	existing, err := r.findChildRun(childCtx, request)
	if err != nil {
		return ChildRunResult{}, err
	}
	if existing != nil {
		return r.continueChildRun(childCtx, request, *existing, input, true)
	}

	run, initialState, err := r.startRun(childCtx, input)
	if err != nil {
		return ChildRunResult{}, err
	}
	if err := r.linkChildRun(childCtx, parentRun.RunID, run.RunID); err != nil {
		_, _, _ = r.failRun(context.WithoutCancel(childCtx), run, initialState, "parent_link_failed", err.Error())
		return ChildRunResult{Run: run, State: initialState}, err
	}
	return r.continueChildRun(childCtx, request, run, initialState, false)
}

func (r *GraphRunner) findChildRun(ctx context.Context, request ChildRunRequest) (*RunRecord, error) {
	runs, err := r.executionStore.ListRuns(ctx, RunFilter{ParentRunID: request.ParentRunID, ParentTaskID: request.ParentTaskID})
	if err != nil {
		return nil, err
	}
	matches := make([]RunRecord, 0, 1)
	for _, run := range runs {
		if run.GraphID != r.resolvedGraphID() || run.Namespace != request.Namespace {
			continue
		}
		matches = append(matches, run)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("child task %q in parent run %q has %d persisted runs", request.ParentTaskID, request.ParentRunID, len(matches))
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return &matches[0], nil
}

func (r *GraphRunner) continueChildRun(ctx context.Context, request ChildRunRequest, run RunRecord, input *state.State, resumed bool) (ChildRunResult, error) {
	controller, _ := ChildRunControllerFromContext(ctx)
	if controller != nil {
		controller.RegisterChildRun(request.ParentTaskID, r, run.RunID)
		defer controller.UnregisterChildRun(request.ParentTaskID, run.RunID)
	}

	var finalState *state.State
	var err error
	switch run.Status {
	case RunStatusCompleted:
		if run.LastCheckpointID == "" {
			return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, fmt.Errorf("completed child run %q has no checkpoint", run.RunID)
		}
		checkpoint, loadErr := r.LoadCheckpointState(ctx, run.LastCheckpointID)
		if loadErr != nil {
			return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, loadErr
		}
		finalState = checkpoint.Business
	case RunStatusFailed:
		return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, fmt.Errorf("child run %q failed: %s", run.RunID, run.ErrorMessage)
	case RunStatusCanceled:
		return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, fmt.Errorf("child run %q was canceled", run.RunID)
	case RunStatusPending, RunStatusRunning:
		if run.LastCheckpointID == "" {
			if input == nil {
				return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, fmt.Errorf("child run %q has no checkpoint or input state", run.RunID)
			}
			run, finalState, err = r.continueStartedRun(ctx, run, input.Clone())
		} else {
			run, finalState, err = r.Resume(ctx, run.RunID, nil)
		}
	case RunStatusPaused:
		run, finalState, err = r.Resume(ctx, run.RunID, nil)
	default:
		err = fmt.Errorf("child run %q has unsupported status %q", run.RunID, run.Status)
	}
	result := ChildRunResult{
		Run: run, State: finalState, ReturnValue: run.ReturnValue,
		Interrupted: run.Status == RunStatusPaused, Resumed: resumed,
	}
	return result, err
}

func (r *GraphRunner) linkChildRun(ctx context.Context, parentRunID, childRunID string) error {
	if controller, ok := ChildRunControllerFromContext(ctx); ok {
		return controller.LinkChildRun(ctx, parentRunID, childRunID)
	}
	for {
		parentRun, err := r.executionStore.GetRun(ctx, parentRunID)
		if err != nil {
			return err
		}
		for _, existingID := range parentRun.ChildRunIDs {
			if existingID == childRunID {
				return nil
			}
		}
		parentRun.ChildRunIDs = append(parentRun.ChildRunIDs, childRunID)
		parentRun.UpdatedAt = r.currentTime()
		if _, err := compareAndSwapRun(ctx, r.executionStore, parentRun); errors.Is(err, ErrRunRevisionConflict) {
			continue
		} else if err != nil {
			return err
		}
		return nil
	}
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
		defer r.releaseExecutionClaim(run.RunID)
		defer close(done)
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
	if issues := state.ValidateStateBySchemas(initialState, r.stateSchemas); len(issues) > 0 {
		return RunRecord{}, initialState, state.NewValidationError("entry", issues)
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
	if lineage, ok := ChildRunLineageFromContext(ctx); ok {
		run.ParentRunID = strings.TrimSpace(lineage.ParentRunID)
		run.ParentStepID = strings.TrimSpace(lineage.ParentStepID)
		run.ParentTaskID = strings.TrimSpace(lineage.ParentTaskID)
		run.RootRunID = strings.TrimSpace(lineage.RootRunID)
		run.RunPath = append([]string(nil), lineage.ParentRunPath...)
		run.RunPath = append(run.RunPath, run.RunID)
		run.Namespace = strings.Trim(strings.TrimSpace(lineage.Namespace), "/")
	}
	if run.RootRunID == "" {
		run.RootRunID = run.RunID
	}
	if len(run.RunPath) == 0 {
		run.RunPath = []string{run.RunID}
	}
	if run.Namespace == "" {
		run.Namespace = run.RunID
	}
	if origin, ok := RunOriginFromContext(ctx); ok {
		originCopy := origin
		run.Origin = &originCopy
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
	createdEvent, err := r.buildEvent(run, "", "", "", EventRunCreated, payload)
	if err != nil {
		return RunRecord{}, initialState, err
	}

	run.Status = RunStatusRunning
	run.CurrentNodeID = run.EntryNodeID
	run.UpdatedAt = r.currentTime()
	startedEvent, err := r.buildEvent(run, "", "", "", EventRunStarted, nil)
	if err != nil {
		return RunRecord{}, initialState, err
	}
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{
		Run:    &RunWrite{Mode: RunWriteCreate, Run: run},
		Events: []Event{createdEvent, startedEvent},
	})
	if err != nil {
		return RunRecord{}, initialState, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	logger.Info("run started", append(runLogFields(run), state.SummaryFields(initialState)...)...)
	return run, initialState, nil
}

func (r *GraphRunner) continueStartedRun(ctx context.Context, run RunRecord, initialState *state.State) (RunRecord, *state.State, error) {
	if err := r.publishStartupWarnings(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	return r.execute(ctx, run, initialState.Clone(), []GraphTask{NewStaticGraphTask(run.EntryNodeID, 0)}, nil, nil)
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
	if checkpoint.Record.Stage == CheckpointFinal {
		return RunRecord{}, nil, fmt.Errorf("final checkpoint %q is not resumable", checkpoint.Record.CheckpointID)
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
	case checkpoint.Record.Stage == CheckpointAfterWave:
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
	if checkpoint.Record.Stage == CheckpointFinal {
		return RunRecord{}, nil, fmt.Errorf("final checkpoint %q is not resumable", checkpoint.Record.CheckpointID)
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
	case checkpoint.Record.Stage == CheckpointAfterWave:
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
		if sessionID := r.resolvedGraphSessionID(); sessionID != "" && strings.TrimSpace(run.GraphSessionID) != sessionID {
			continue
		}
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

func (r *GraphRunner) execute(ctx context.Context, run RunRecord, currentState *state.State, startTasks []GraphTask, skip *breakpointSkip, artifacts []state.ArtifactRef) (RunRecord, *state.State, error) {
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
	config.StepObserver = func(ctx context.Context, completedTasks []GraphTask, currentState *state.State) error {
		err := execution.OnGraphStep(ctx, completedTasks, currentState)
		if err != nil {
			execution.recordCallbackError(err)
		}
		return err
	}
	config.EventObserver = execution.OnSchedulerEvent
	if len(startTasks) > 0 {
		config.StartTasks = CloneGraphTasks(startTasks)
	}
	if len(afterNodes) > 0 {
		config.InterruptAfterNodeIDs = afterNodes
	}
	fields := append(runLogFields(run),
		zap.Strings("start_nodes", GraphTaskNodeIDs(startTasks)),
		zap.Int("breakpoint_count", len(r.breakpoints)),
		zap.Int("interrupt_after_count", len(afterNodes)),
		zap.Int("artifact_count", len(artifacts)),
	)
	fields = append(fields, state.SummaryFields(currentState)...)
	logger.Info("run executing", fields...)

	finalState, invokeErr := runnable.InvokeWithConfig(invokeCtx, currentState.Clone(), config)
	finalState = execution.stateOrFallback(finalState)
	controlCtx := execution.controlPersistenceContext(ctx)
	if run, pausedState, handled, err := r.resolvePendingControl(controlCtx, execution, finalState, invokeErr); handled || err != nil {
		return run, pausedState, err
	}
	if execution.currentRun().CancelRequested {
		transition, err := execution.prepareCanceledSteps()
		if err != nil {
			return r.failRun(ctx, execution.currentRun(), finalState, "cancel_finalize_failed", err.Error())
		}
		return r.cancelRunWithTransition(ctx, execution.currentRun(), finalState, transition)
	}
	if callbackErr := execution.callbackError(); callbackErr != nil {
		transition, err := execution.prepareFailedSteps(callbackErr)
		if err != nil {
			return RunRecord{}, finalState, err
		}
		return r.failRunWithTransition(ctx, execution.currentRun(), finalState, "callback_failed", callbackErr.Error(), transition)
	}
	if invokeErr == nil {
		return r.completeRun(ctx, execution.currentRun(), finalState, execution.snapshotArtifacts())
	}

	var interrupt *GraphInterrupt
	if errors.As(invokeErr, &interrupt) {
		return r.handleInterrupt(ctx, execution, finalState, interrupt)
	}

	transition, err := execution.prepareFailedSteps(invokeErr)
	if err != nil {
		return RunRecord{}, finalState, err
	}
	errorCode := string(core.ClassifyError(invokeErr))
	if errorCode == string(core.ErrorUnknown) {
		errorCode = "node_failed"
	}
	return r.failRunWithTransition(ctx, execution.currentRun(), finalState, errorCode, invokeErr.Error(), transition)
}

func (r *GraphRunner) resolvePendingControl(ctx context.Context, execution *graphRunnerExecution, currentState *state.State, invokeErr error) (RunRecord, *state.State, bool, error) {
	if control, active := execution.consumePendingControl(); control != nil {
		if control.kind == runnerControlCancel {
			transition, err := execution.prepareCanceledSteps()
			if err != nil {
				run, finalState, failErr := r.failRun(ctx, execution.currentRun(), currentState, "cancel_finalize_failed", err.Error())
				return run, finalState, true, failErr
			}
			run, finalState, err := r.cancelRunWithTransition(ctx, execution.currentRun(), currentState, transition)
			return run, finalState, true, err
		}
		if control.checkpointID == "" {
			if control.kind == runnerControlPause && active != nil && pauseControlCanceledInvoke(control, invokeErr) {
				if active.beforeCheckpointID == "" {
					run, finalState, err := r.failRun(ctx, execution.currentRun(), currentState, "interrupt_failed", fmt.Sprintf("pause interrupt missing before checkpoint for %q", active.step.NodeID))
					return run, finalState, true, err
				}
				checkpointID, checkpointErr := r.saveCheckpoint(ctx, execution.currentRun(), active.step, active.step.NodeID, CheckpointBeforeNode, currentState, active.attempts, control.hit, execution.snapshotArtifacts())
				if checkpointErr != nil {
					run, finalState, err := r.failRun(ctx, execution.currentRun(), currentState, "interrupt_failed", fmt.Sprintf("refresh pause checkpoint for %q: %v", active.step.NodeID, checkpointErr))
					return run, finalState, true, err
				}
				active.step.CheckpointBeforeID = checkpointID
				run, finalState, err := r.pauseRun(ctx, execution.currentRun(), currentState, active.step, checkpointID, control.hit, control.message)
				return run, finalState, true, err
			}
			execution.restorePendingControl(control)
			return RunRecord{}, currentState, false, nil
		}
		switch control.kind {
		case runnerControlPause:
			if control.nodeID != waveCheckpointNodeID {
				identifier := control.taskID
				if identifier == "" {
					identifier = control.nodeID
				}
				completed := execution.consumeLastCompleted(identifier)
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
	if issues := state.ValidateStateBySchemas(checkpoint.Business, r.stateSchemas); len(issues) > 0 {
		return RunRecord{}, checkpoint.Business, state.NewValidationError("resume input", issues)
	}

	startTasks, skip, err := r.resumeTarget(ctx, checkpoint.Record, checkpoint.Runtime, checkpoint.Business)
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
	resolvedToEnd := len(startTasks) == 0 || len(startTasks) == 1 && startTasks[0].NodeID == EndNodeID
	run.CurrentNodeID = checkpoint.Runtime.CurrentNodeID
	if checkpoint.Record.Stage != CheckpointBeforeNode || run.CurrentNodeID == "" {
		if len(startTasks) > 0 {
			run.CurrentNodeID = startTasks[0].NodeID
		}
	}
	if resolvedToEnd {
		clearRunExecutionPointers(&run)
	}
	run.UpdatedAt = r.currentTime()
	resumedEvent, err := r.buildEvent(run, "", "", "", EventRunResumed, map[string]any{
		"checkpoint_id": checkpoint.Record.CheckpointID,
		"node_id":       run.CurrentNodeID,
		"node_ids":      GraphTaskNodeIDs(startTasks),
		"tasks":         startTasks,
	})
	if err != nil {
		return RunRecord{}, nil, err
	}
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Events: []Event{resumedEvent},
	})
	if err != nil {
		return RunRecord{}, nil, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	if resolvedToEnd {
		logger.Info("resume resolved to completed run", append(runLogFields(run), state.SummaryFields(checkpoint.Business)...)...)
		return r.completeRun(ctx, run, checkpoint.Business, checkpoint.Artifacts)
	}

	fields := append(runLogFields(run),
		zap.Strings("start_nodes", GraphTaskNodeIDs(startTasks)),
		zap.String("resume_checkpoint_id", checkpoint.Record.CheckpointID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	)
	fields = append(fields, state.SummaryFields(checkpoint.Business)...)
	logger.Info("resuming run", fields...)
	return r.execute(ctx, run, checkpoint.Business, startTasks, skip, checkpoint.Artifacts)
}

func (r *GraphRunner) continueRun(ctx context.Context, run RunRecord, checkpoint RestoredCheckpoint, input *state.State) (RunRecord, *state.State, error) {
	continuedState, err := state.PrepareContinuationState(checkpoint.Business, input)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if issues := state.ValidateStateBySchemas(continuedState, r.stateSchemas); len(issues) > 0 {
		return RunRecord{}, continuedState, state.NewValidationError("resume input", issues)
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
	if issues := state.ValidateStateBySchemas(result.Business, r.stateSchemas); len(issues) > 0 {
		return RestoredCheckpoint{}, state.NewValidationError("restore", issues)
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
			return nil
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
		control, controlErr := NewRunControlService(r.executionStore, r.transactionStore, r.eventSink, nil)
		if controlErr != nil {
			return controlErr
		}
		control, controlErr = control.WithNow(r.currentTime)
		if controlErr != nil {
			return controlErr
		}
		canceledRun, controlErr := control.CancelPausedRun(ctx, runID)
		if controlErr != nil {
			return controlErr
		}
		logger.Info("run canceled", runLogFields(canceledRun)...)
		return r.applyRunRetention(context.WithoutCancel(normalizeRunnerContext(ctx)), canceledRun.RunID)
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
			return nil
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
	eventType, err := controlRequestEventType(kind)
	if err != nil {
		return RunRecord{}, err
	}
	event, err := r.buildEvent(run, "", "", "", eventType, nil)
	if err != nil {
		return RunRecord{}, err
	}
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Events: []Event{event},
	})
	if err != nil {
		return RunRecord{}, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
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
	return r.hasExecutionClaim(runID)
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
			transition, err := execution.prepareCanceledSteps()
			if err != nil {
				return r.failRun(ctx, execution.currentRun(), currentState, "cancel_finalize_failed", err.Error())
			}
			return r.cancelRunWithTransition(ctx, execution.currentRun(), currentState, transition)
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
				checkpointState := currentState.Clone()
				schedule, _ := LoadGraphSchedule(checkpointState)
				if err := StoreGraphSchedule(checkpointState, GraphSchedule{
					CurrentTasks:      []GraphTask{active.task},
					PendingFanInNodes: schedule.PendingFanInNodes,
				}); err != nil {
					return r.failRun(ctx, run, currentState, "checkpoint_failed", err.Error())
				}
				savedID, err := r.saveCheckpoint(ctx, run, step, step.NodeID, CheckpointBeforeNode, checkpointState, active.attempts, control.hit, execution.snapshotArtifacts())
				if err != nil {
					return r.failRun(ctx, run, currentState, "checkpoint_failed", err.Error())
				}
				checkpointID = savedID
				step.CheckpointBeforeID = savedID
			}
			return r.pauseRun(ctx, run, currentState, step, checkpointID, control.hit, control.message)
		}
	}
	if interrupt.CheckpointStage == CheckpointAfterWave {
		if run.LastCheckpointID == "" {
			return r.failRun(ctx, run, currentState, "interrupt_failed", "after-wave interrupt missing checkpoint")
		}
		return r.pauseRunAtCheckpoint(ctx, run, currentState, run.LastCheckpointID, nil, interrupt.Error())
	}

	identifier := interrupt.TaskID
	if identifier == "" {
		identifier = interrupt.NodeID
	}
	if hit := r.matchBreakpoint(interrupt.NodeID, string(CheckpointAfterNode), nil); hit != nil {
		completed := execution.consumeLastCompleted(identifier)
		if completed == nil {
			return r.failRun(ctx, run, currentState, "interrupt_failed", fmt.Sprintf("after-node interrupt missing completed step for %q", interrupt.NodeID))
		}
		return r.pauseRun(ctx, run, currentState, completed.step, completed.afterCheckpointID, hit, "")
	}

	if completed := execution.consumeLastCompleted(identifier); completed != nil {
		return r.pauseRun(ctx, run, currentState, completed.step, completed.afterCheckpointID, nil, interrupt.Error())
	}

	return r.failRun(ctx, run, currentState, "interrupt_failed", interrupt.Error())
}

func (r *GraphRunner) completeRun(ctx context.Context, run RunRecord, finalState *state.State, artifacts []state.ArtifactRef) (RunRecord, *state.State, error) {
	if returnValue, ok := LoadGraphReturnValue(finalState); ok {
		run.ReturnValue = returnValue
		if err := ClearGraphReturnValue(finalState); err != nil {
			return r.failRun(ctx, run, finalState, "return_value_cleanup_failed", err.Error())
		}
	}
	if issues := state.ValidateStateBySchemas(finalState, r.stateSchemas); len(issues) > 0 {
		return r.failRun(ctx, run, finalState, "output_schema_validation_failed", state.NewValidationError("output", issues).Error())
	}
	now := r.currentTime()
	run.Status = RunStatusCompleted
	run.PauseRequested = false
	run.CancelRequested = false
	clearRunExecutionPointers(&run)
	run.UpdatedAt = now
	run.FinishedAt = &now
	finalStep := StepRecord{StepID: run.LastStepID}
	checkpointWrite, checkpointEvent, err := r.buildCheckpointWrite(ctx, run, finalStep, "__final__", CheckpointFinal, finalState, 0, nil, artifacts)
	if err != nil {
		return RunRecord{}, finalState, err
	}
	run.LastCheckpointID = checkpointWrite.Record.CheckpointID
	payload := any(nil)
	if run.ReturnValue != nil {
		payload = map[string]any{"return_value": run.ReturnValue}
	}
	finishedEvent, err := r.buildEvent(run, run.LastStepID, "", "", EventRunFinished, payload)
	if err != nil {
		return RunRecord{}, finalState, err
	}
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{
		Run: &RunWrite{Mode: RunWriteUpdate, Run: run}, Checkpoints: []CheckpointWrite{checkpointWrite}, Events: []Event{checkpointEvent, finishedEvent},
	})
	if err != nil {
		return RunRecord{}, finalState, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	logger.Info("run completed", append(runLogFields(run), state.SummaryFields(finalState)...)...)
	if err := r.applyRunRetention(context.WithoutCancel(normalizeRunnerContext(ctx)), run.RunID); err != nil {
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
	return r.cancelRunWithTransition(ctx, run, currentState, runnerStepTransition{})
}

func (r *GraphRunner) cancelRunWithTransition(ctx context.Context, run RunRecord, currentState *state.State, transition runnerStepTransition) (RunRecord, *state.State, error) {
	now := r.currentTime()
	run.Status = RunStatusCanceled
	run.PauseRequested = false
	run.CancelRequested = false
	run.UpdatedAt = now
	run.FinishedAt = &now
	canceledEvent, err := r.buildEvent(run, "", "", run.CurrentNodeID, EventRunCanceled, nil)
	if err != nil {
		return RunRecord{}, currentState, err
	}
	events := append([]Event(nil), transition.events...)
	events = append(events, canceledEvent)
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:  transition.writes,
		Events: events,
	})
	if err != nil {
		return RunRecord{}, currentState, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	logger.Info("run canceled", append(runLogFields(run), state.SummaryFields(currentState)...)...)
	if err := r.applyRunRetention(context.WithoutCancel(normalizeRunnerContext(ctx)), run.RunID); err != nil {
		return run, currentState, err
	}
	return run, currentState, nil
}

func (r *GraphRunner) saveCheckpoint(ctx context.Context, run RunRecord, step StepRecord, nodeID string, stage CheckpointStage, currentState *state.State, attempts int, hit *state.BreakpointHit, artifacts []state.ArtifactRef) (string, error) {
	write, event, err := r.buildCheckpointWrite(ctx, run, step, nodeID, stage, currentState, attempts, hit, artifacts)
	if err != nil {
		return "", err
	}
	if _, err := r.commitRuntime(ctx, RuntimeCommit{
		Checkpoints: []CheckpointWrite{write},
		Events:      []Event{event},
	}); err != nil {
		return "", err
	}
	return write.Record.CheckpointID, nil
}

func (r *GraphRunner) buildCheckpointWrite(ctx context.Context, run RunRecord, step StepRecord, nodeID string, stage CheckpointStage, currentState *state.State, attempts int, hit *state.BreakpointHit, artifacts []state.ArtifactRef) (CheckpointWrite, Event, error) {
	checkpointState := currentState
	if budget, ok := GraphExecutionBudgetFromContext(ctx); ok {
		checkpointState = currentState.Clone()
		if err := StoreGraphExecutionBudget(checkpointState, budget); err != nil {
			return CheckpointWrite{}, Event{}, fmt.Errorf("store graph execution budget: %w", err)
		}
	}
	snapshot, err := state.SnapshotFromStateWithRuntime(checkpointState, state.RuntimeState{
		RunID:           run.RunID,
		CurrentStepID:   step.StepID,
		CurrentTaskID:   step.TaskID,
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
		return CheckpointWrite{}, Event{}, fmt.Errorf("encode checkpoint state: %w", err)
	}
	snapshot.Version = r.codec.Version()

	payload, err := r.codec.Encode(snapshot)
	if err != nil {
		return CheckpointWrite{}, Event{}, fmt.Errorf("encode checkpoint state: %w", err)
	}

	record := CheckpointRecord{
		CheckpointID: newRunnerID(),
		RunID:        run.RunID,
		StepID:       step.StepID,
		TaskID:       step.TaskID,
		ParentRunID:  run.ParentRunID,
		ParentStepID: run.ParentStepID,
		ParentTaskID: run.ParentTaskID,
		RootRunID:    run.RootRunID,
		RunPath:      append([]string(nil), run.RunPath...),
		Namespace:    run.Namespace,
		NodeID:       nodeID,
		Stage:        stage,
		StateCodec:   r.codec.Name(),
		StateVersion: r.codec.Version(),
		CreatedAt:    r.currentTime(),
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
	logger.Debug("checkpoint prepared", fields...)
	event, err := r.buildEvent(run, step.StepID, step.TaskID, record.NodeID, EventCheckpointCreated, map[string]any{
		"checkpoint_id": record.CheckpointID,
		"stage":         stage,
	})
	if err != nil {
		return CheckpointWrite{}, Event{}, err
	}
	return CheckpointWrite{Record: record, Payload: payload}, event, nil
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

func (r *GraphRunner) pauseRun(ctx context.Context, run RunRecord, currentState *state.State, step StepRecord, checkpointID string, hit *state.BreakpointHit, message string) (RunRecord, *state.State, error) {
	now := r.currentTime()
	stage := pauseCheckpointStage(step, checkpointID)
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	stepUpdated := stage != CheckpointAfterNode
	stepWrites := []StepWrite(nil)
	if stepUpdated {
		step.Status = StepStatusPaused
		step.UpdatedAt = now
		stepWrites = append(stepWrites, StepWrite{Mode: StepWriteUpdate, Step: step})
	}
	events := make([]Event, 0, 2)
	if hit != nil {
		event, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventBreakpointHit, hit)
		if err != nil {
			return RunRecord{}, currentState, err
		}
		events = append(events, event)
	}
	pausedEvent, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventRunPaused, pauseEventPayload(checkpointID, stage, step.NodeID, message, hit))
	if err != nil {
		return RunRecord{}, currentState, err
	}
	events = append(events, pausedEvent)
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{Run: &RunWrite{Mode: RunWriteUpdate, Run: run}, Steps: stepWrites, Events: events})
	if err != nil {
		return RunRecord{}, currentState, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
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
	return run, currentState, nil
}

func (r *GraphRunner) pauseRunAtCheckpoint(ctx context.Context, run RunRecord, currentState *state.State, checkpointID string, hit *state.BreakpointHit, message string) (RunRecord, *state.State, error) {
	now := r.currentTime()
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.LastCheckpointID = checkpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	events := make([]Event, 0, 2)
	if hit != nil {
		event, err := r.buildEvent(run, "", "", waveCheckpointNodeID, EventBreakpointHit, hit)
		if err != nil {
			return RunRecord{}, currentState, err
		}
		events = append(events, event)
	}
	pausedEvent, err := r.buildEvent(run, "", "", waveCheckpointNodeID, EventRunPaused, pauseEventPayload(checkpointID, CheckpointAfterWave, waveCheckpointNodeID, message, hit))
	if err != nil {
		return RunRecord{}, currentState, err
	}
	events = append(events, pausedEvent)
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{Run: &RunWrite{Mode: RunWriteUpdate, Run: run}, Events: events})
	if err != nil {
		return RunRecord{}, currentState, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	fields := append(runLogFields(run), state.SummaryFields(currentState)...)
	if hit != nil {
		fields = append(fields,
			zap.String("breakpoint_id", hit.BreakpointID),
			zap.String("breakpoint_stage", hit.Stage),
		)
	}
	logger.Info("run paused", fields...)
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
	return r.failRunWithTransition(ctx, run, currentState, code, message, runnerStepTransition{})
}

func (r *GraphRunner) failRunWithTransition(ctx context.Context, run RunRecord, currentState *state.State, code string, message string, transition runnerStepTransition) (RunRecord, *state.State, error) {
	failedRun, err := r.persistRunFailureWithTransition(ctx, run, currentState, code, message, transition)
	if err != nil {
		return RunRecord{}, currentState, err
	}
	return failedRun, currentState, errors.New(message)
}

func (r *GraphRunner) persistRunFailure(ctx context.Context, run RunRecord, currentState *state.State, code string, message string) (RunRecord, error) {
	return r.persistRunFailureWithTransition(ctx, run, currentState, code, message, runnerStepTransition{})
}

func (r *GraphRunner) persistRunFailureWithTransition(ctx context.Context, run RunRecord, currentState *state.State, code string, message string, transition runnerStepTransition) (RunRecord, error) {
	now := r.currentTime()
	run.Status = RunStatusFailed
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = code
	run.ErrorMessage = message
	run.UpdatedAt = now
	run.FinishedAt = &now
	failedEvent, err := r.buildEvent(run, "", "", run.CurrentNodeID, EventRunFailed, map[string]any{
		"error_code":    code,
		"error_message": message,
	})
	if err != nil {
		return RunRecord{}, err
	}
	events := append([]Event(nil), transition.events...)
	events = append(events, failedEvent)
	commitResult, err := r.commitRuntime(ctx, RuntimeCommit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:  transition.writes,
		Events: events,
	})
	if err != nil {
		return RunRecord{}, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	for _, step := range transition.steps {
		logger.Error("nodes failed", append(stepLogFields(step), zap.String("error", message))...)
	}
	logger.Error("run failed", append(runLogFields(run), state.SummaryFields(currentState)...)...)
	if err := r.applyRunRetention(context.WithoutCancel(normalizeRunnerContext(ctx)), run.RunID); err != nil {
		return run, err
	}
	return run, nil
}

func (r *GraphRunner) applyRunRetention(ctx context.Context, protectedRunID string) error {
	if r == nil || r.runDeleter == nil || r.retentionAudit == nil || (r.retentionPolicy.MaxRuns <= 0 && r.retentionPolicy.MaxAge <= 0) {
		return nil
	}
	r.retentionMu.Lock()
	defer r.retentionMu.Unlock()
	runs, err := r.executionStore.ListRuns(ctx, RunFilter{})
	if err != nil {
		return fmt.Errorf("list runs for retention: %w", err)
	}
	byID := make(map[string]RunRecord, len(runs))
	for _, run := range runs {
		byID[run.RunID] = run
	}
	for runID, reason := range retentionCandidates(runs, r.retentionPolicy, r.currentTime()) {
		if runID == protectedRunID || r.IsRunActive(runID) {
			continue
		}
		run := byID[runID]
		if err := r.retentionAudit.RecordRetention(ctx, RetentionAuditRecord{
			RunID:          runID,
			GraphID:        run.GraphID,
			GraphSessionID: run.GraphSessionID,
			Action:         "delete_intent",
			Reason:         reason,
			Policy:         r.retentionPolicy,
			RecordedAt:     r.currentTime(),
		}); err != nil {
			return fmt.Errorf("audit retained run %q: %w", runID, err)
		}
		if err := r.runDeleter.DeleteRun(ctx, runID); err != nil {
			return fmt.Errorf("retain run %q: %w", runID, err)
		}
	}
	return nil
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
	failedEvent, buildErr := r.buildEvent(run, "", "", run.CurrentNodeID, EventRunFailed, map[string]any{
		"error_code":    code,
		"error_message": cause.Error(),
	})
	if buildErr != nil {
		return errors.Join(cause, buildErr)
	}
	_, commitErr := r.commitRuntime(failureCtx, RuntimeCommit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Events: []Event{failedEvent},
	})
	return errors.Join(cause, commitErr)
}

func (r *GraphRunner) resumeTarget(ctx context.Context, checkpoint CheckpointRecord, runtime state.RuntimeState, currentState *state.State) ([]GraphTask, *breakpointSkip, error) {
	schedule, _ := LoadGraphSchedule(currentState)
	switch checkpoint.Stage {
	case CheckpointBeforeNode:
		nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
		if err != nil {
			return nil, nil, err
		}
		if len(schedule.CurrentTasks) != 1 || schedule.CurrentTasks[0].NodeID != nodeID {
			return nil, nil, fmt.Errorf("before-node checkpoint %q has invalid current task schedule", checkpoint.CheckpointID)
		}
		return CloneGraphTasks(schedule.CurrentTasks), &breakpointSkip{NodeID: checkpoint.NodeID, Stage: string(CheckpointBeforeNode)}, nil
	case CheckpointAfterNode:
		nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
		if err != nil {
			return nil, nil, err
		}
		if r.runnerGraph().IsParallelBranchTarget(nodeID) || runtime.ParallelWaveID != "" || runtime.WaveID != "" {
			return nil, nil, fmt.Errorf("resume from wave task %q checkpoint %q is not supported without after-wave context", checkpoint.Stage, checkpoint.CheckpointID)
		}
		nextNodeID, err := r.runnerGraph().ResolveNextNode(ctx, nodeID, currentState)
		if err != nil {
			return nil, nil, err
		}
		return []GraphTask{NewStaticGraphTask(nextNodeID, 0)}, nil, nil
	case CheckpointAfterWave:
		return CloneGraphTasks(schedule.NextTasks), nil, nil
	case CheckpointFinal:
		return nil, nil, fmt.Errorf("final checkpoint %q is not resumable", checkpoint.CheckpointID)
	default:
		return nil, nil, fmt.Errorf("unsupported checkpoint stage %q", checkpoint.Stage)
	}
}

func (r *GraphRunner) matchBreakpoint(nodeID string, stage string, skip *breakpointSkip) *state.BreakpointHit {
	hit, skipped := r.previewBreakpoint(nodeID, stage, skip)
	if skipped && skip != nil {
		skip.Consumed = true
	}
	return hit
}

func (r *GraphRunner) previewBreakpoint(nodeID string, stage string, skip *breakpointSkip) (*state.BreakpointHit, bool) {
	if skip != nil && !skip.Consumed && skip.NodeID == nodeID && skip.Stage == stage {
		return nil, true
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
		}, false
	}
	return nil, false
}

func (r *GraphRunner) publishEvent(ctx context.Context, run RunRecord, stepID string, nodeID string, eventType EventType, payload any) error {
	return r.publishEventWithTask(ctx, run, stepID, "", nodeID, eventType, payload)
}

func (r *GraphRunner) publishEventWithTask(ctx context.Context, run RunRecord, stepID, taskID, nodeID string, eventType EventType, payload any) error {
	event, err := r.buildEvent(run, stepID, taskID, nodeID, eventType, payload)
	if err != nil {
		return err
	}
	return r.publishPreparedEvent(ctx, event)
}

func (r *GraphRunner) buildEvent(run RunRecord, stepID, taskID, nodeID string, eventType EventType, payload any) (Event, error) {
	var raw json.RawMessage
	if payload != nil {
		bytes, err := json.Marshal(payload)
		if err != nil {
			return Event{}, err
		}
		raw = bytes
	}
	event := Event{
		ID:             newRunnerID(),
		GraphID:        firstNonEmpty(run.GraphID, r.resolvedGraphID()),
		GraphSessionID: firstNonEmpty(run.GraphSessionID, r.resolvedGraphSessionID()),
		RunID:          run.RunID,
		ParentRunID:    run.ParentRunID,
		ParentStepID:   run.ParentStepID,
		ParentTaskID:   run.ParentTaskID,
		RootRunID:      run.RootRunID,
		RunPath:        append([]string(nil), run.RunPath...),
		Namespace:      run.Namespace,
		StepID:         stepID,
		TaskID:         taskID,
		NodeID:         nodeID,
		Type:           eventType,
		Timestamp:      r.currentTime(),
		Payload:        raw,
	}
	return event, nil
}

func (r *GraphRunner) publishPreparedEvent(ctx context.Context, event Event) error {
	if r.eventSink != nil {
		if err := r.eventSink.Publish(ctx, event); err != nil {
			return err
		}
	}
	return observeRunnerContextEvent(ctx, event)
}

func (r *GraphRunner) commitRuntime(ctx context.Context, commit RuntimeCommit) (RuntimeCommitResult, error) {
	if r == nil || r.transactionStore == nil {
		return RuntimeCommitResult{}, errors.New("runtime transaction store is nil")
	}
	result, err := r.transactionStore.Commit(ctx, commit)
	if err != nil {
		return RuntimeCommitResult{}, err
	}
	if err := publishCommittedEventObservers(ctx, r.eventSink, r.transactionStore, commit.Events); err != nil {
		return result, err
	}
	for _, event := range commit.Events {
		if err := observeRunnerContextEvent(ctx, event); err != nil {
			return result, err
		}
	}
	return result, nil
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
	if artifact.ParentRunID == "" {
		artifact.ParentRunID = metadata.ParentRunID
	}
	if artifact.ParentStepID == "" {
		artifact.ParentStepID = metadata.ParentStepID
	}
	if artifact.ParentTaskID == "" {
		artifact.ParentTaskID = metadata.ParentTaskID
	}
	if artifact.RootRunID == "" {
		artifact.RootRunID = metadata.RootRunID
	}
	if len(artifact.RunPath) == 0 {
		artifact.RunPath = append([]string(nil), metadata.RunPath...)
	}
	if artifact.Namespace == "" {
		artifact.Namespace = metadata.Namespace
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
		r.publishBestEffortEvent(ctx, RunRecord{
			RunID: artifact.RunID, ParentRunID: artifact.ParentRunID,
			ParentStepID: artifact.ParentStepID, ParentTaskID: artifact.ParentTaskID, RootRunID: artifact.RootRunID,
			RunPath: append([]string(nil), artifact.RunPath...), Namespace: artifact.Namespace,
		}, artifact.StepID, artifact.NodeID, EventArtifactCreated, map[string]any{
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
	if record.TaskID != "" && checkpoint.Runtime.CurrentTaskID != "" && record.TaskID != checkpoint.Runtime.CurrentTaskID {
		return fmt.Errorf("checkpoint %q task mismatch: record=%q snapshot=%q", record.CheckpointID, record.TaskID, checkpoint.Runtime.CurrentTaskID)
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
	expectedSessionID := r.resolvedGraphSessionID()
	actualSessionID := strings.TrimSpace(run.GraphSessionID)
	if expectedSessionID != "" && actualSessionID != expectedSessionID {
		return fmt.Errorf("resume run %q: graph session mismatch: run uses %q, runner uses %q", run.RunID, actualSessionID, expectedSessionID)
	}
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
