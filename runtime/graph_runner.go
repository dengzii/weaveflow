package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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
	codec              state.Codec
	eventSink          EventSink
	transactionStore   TransactionStore
	taskQueue          TaskQueue
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
	reducers           map[string]state.Reducer
	now                func() time.Time
	leaseOwnerID       string
	leaseTTL           time.Duration
	leaseHeartbeat     time.Duration
	eventDiagnosticsMu sync.Mutex
	eventDiagnostics   EventPublicationDiagnostics
	activeMu           sync.Mutex
	activeExecutions   map[string]*graphRunnerExecution
	effectResolutionMu sync.Mutex
	activeResolutions  map[string]string
	childRunMu         sync.Mutex
	closer             io.Closer
	closeOnce          sync.Once
	closeErr           error
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
	reducers           map[string]state.Reducer
	transactionStore   TransactionStore
	taskQueue          TaskQueue
	closer             io.Closer
	now                func() time.Time
	leaseOwnerID       string
	leaseTTL           time.Duration
	leaseHeartbeat     time.Duration
}

func NewGraphRunner(graph RunnerGraph, executionStore ExecutionStore, checkpointStore CheckpointStore, codec state.Codec, eventSink EventSink, options ...GraphRunnerOption) (*GraphRunner, error) {
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
		leaseOwnerID:       newRunnerID(),
		leaseTTL:           30 * time.Second,
		leaseHeartbeat:     10 * time.Second,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	if err := validateNodeContractsWithReducers(cfg.nodeContracts, cfg.reducers); err != nil {
		return nil, err
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
	if cfg.runDeleter != nil {
		if _, ok := transactionStore.(RunDeleter); !ok {
			return nil, fmt.Errorf("run deletion requires a transaction store with deletion support")
		}
		if _, ok := transactionStore.(RunDeletionFencer); !ok {
			return nil, fmt.Errorf("run deletion requires a transaction store with deletion fencing")
		}
		if _, ok := cfg.artifactStore.(RunDeleter); !ok {
			return nil, fmt.Errorf("run deletion requires an artifact store with deletion support")
		}
		if _, ok := cfg.artifactStore.(RunDeletionFencer); !ok {
			return nil, fmt.Errorf("run deletion requires an artifact store with deletion fencing")
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
		taskQueue:          cfg.taskQueue,
		closer:             cfg.closer,
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
		reducers:           cloneReducers(cfg.reducers),
		now:                cfg.now,
		leaseOwnerID:       cfg.leaseOwnerID,
		leaseTTL:           cfg.leaseTTL,
		leaseHeartbeat:     cfg.leaseHeartbeat,
		eventDiagnostics: EventPublicationDiagnostics{
			BestEffortFailures: map[EventType]EventPublicationFailure{},
		},
		activeExecutions:  make(map[string]*graphRunnerExecution),
		activeResolutions: make(map[string]string),
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
	return func(cfg *graphRunnerConfig) error {
		if err := validateContractValidationMode(mode); err != nil {
			return err
		}
		cfg.contractValidation = mode
		return nil
	}
}

func WithContractPolicy(policy ContractPolicy) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if policy.ModeSet || policy.Mode != core.ContractValidationOff {
			if err := validateContractValidationMode(policy.Mode); err != nil {
				return err
			}
		}
		cfg.contractPolicy = policy
		return nil
	}
}

func WithStartupWarnings(warnings []WarningRecord) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		cfg.startupWarnings = append([]WarningRecord(nil), warnings...)
		return nil
	}
}

func WithNodeContracts(contracts map[string]state.Contract) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if err := validateNodeContracts(contracts); err != nil {
			return err
		}
		cfg.nodeContracts = cloneContracts(contracts)
		return nil
	}
}

func WithStateSchemas(schemas map[string]state.JSONSchema) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.stateSchemas = cloneSchemas(schemas); return nil }
}

func WithStateReducers(reducers map[string]state.Reducer) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error { cfg.reducers = cloneReducers(reducers); return nil }
}

func cloneReducers(reducers map[string]state.Reducer) map[string]state.Reducer {
	if len(reducers) == 0 {
		return nil
	}
	cloned := make(map[string]state.Reducer, len(reducers))
	for identifier, reducer := range reducers {
		cloned[identifier] = reducer
	}
	return cloned
}

func WithRuntimeTransactionStore(store TransactionStore) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if store == nil {
			return fmt.Errorf("runtime transaction store is required")
		}
		cfg.transactionStore = store
		return nil
	}
}

func WithTaskQueue(queue TaskQueue) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		cfg.taskQueue = queue
		return nil
	}
}

func WithStoreCloser(closer io.Closer) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		if closer == nil {
			return fmt.Errorf("store closer is required")
		}
		cfg.closer = closer
		return nil
	}
}

func (r *GraphRunner) Close() error {
	if r == nil || r.closer == nil {
		return nil
	}
	r.activeMu.Lock()
	active := len(r.activeExecutions)
	r.activeMu.Unlock()
	if active > 0 {
		return fmt.Errorf("cannot close graph runner with %d active executions", active)
	}
	r.closeOnce.Do(func() {
		r.closeErr = r.closer.Close()
	})
	return r.closeErr
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

func WithExecutionLeasePolicy(ownerID string, ttl, heartbeatInterval time.Duration) GraphRunnerOption {
	return func(cfg *graphRunnerConfig) error {
		ownerID = strings.TrimSpace(ownerID)
		if err := validateRunnerStorageID("execution lease owner ID", ownerID); err != nil {
			return err
		}
		if ttl <= 0 {
			return errors.New("execution lease TTL must be greater than zero")
		}
		if heartbeatInterval <= 0 || heartbeatInterval >= ttl {
			return errors.New("execution lease heartbeat interval must be greater than zero and less than TTL")
		}
		cfg.leaseOwnerID = ownerID
		cfg.leaseTTL = ttl
		cfg.leaseHeartbeat = heartbeatInterval
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

func validateContractValidationMode(mode core.ContractValidationMode) error {
	switch mode {
	case core.ContractValidationOff, core.ContractValidationWarn, core.ContractValidationStrict:
		return nil
	default:
		return fmt.Errorf("unsupported contract validation mode %q", mode)
	}
}

func validateNodeContracts(contracts map[string]state.Contract) error {
	for nodeID, contract := range contracts {
		if strings.TrimSpace(nodeID) == "" {
			return fmt.Errorf("node contract ID is required")
		}
		if strings.TrimSpace(nodeID) != nodeID {
			return fmt.Errorf("node contract ID %q must not have surrounding whitespace", nodeID)
		}
		if issues := state.ValidateContract(contract); len(issues) > 0 {
			return fmt.Errorf("node %q contract: %w", nodeID, state.NewValidationError("contract", issues))
		}
	}
	return nil
}

func validateNodeContractsWithReducers(contracts map[string]state.Contract, reducers map[string]state.Reducer) error {
	if err := validateNodeContracts(contracts); err != nil {
		return err
	}
	for nodeID, contract := range contracts {
		for _, field := range contract.Fields {
			reducerID := strings.TrimSpace(field.Reducer)
			if reducerID == "" {
				continue
			}
			if field.Mode != state.AccessWrite && field.Mode != state.AccessReadWrite {
				return fmt.Errorf("node %q state path %q reducer requires write access", nodeID, field.Path.String())
			}
			if state.IsNilReducer(reducers[reducerID]) {
				return fmt.Errorf("node %q state path %q reducer %q is not registered", nodeID, field.Path.String(), reducerID)
			}
		}
	}
	return nil
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

func (r *GraphRunner) TransactionStore() TransactionStore {
	if r == nil {
		return nil
	}
	return r.transactionStore
}

func (r *GraphRunner) StateCodec() state.Codec {
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
	run, initialState, err := r.startRun(ctx, initialState, nil)
	if err != nil {
		return RunRecord{}, initialState, err
	}
	guard, _ := executionLeaseGuard(run)
	executionCtx, heartbeat := r.startLeaseHeartbeat(ctx, guard)
	finishedRun, finalState, runErr := r.continueStartedRun(executionCtx, run, initialState)
	leaseErr := r.finishExecutionLease(executionCtx, guard, heartbeat)
	finishedRun, leaseErr = r.refreshRunAfterLease(executionCtx, finishedRun, leaseErr)
	return finishedRun, finalState, errors.Join(runErr, leaseErr)
}

func (r *GraphRunner) RunChild(ctx context.Context, request ChildRunRequest, input *state.State) (ChildRunResult, error) {
	ctx = normalizeRunnerContext(ctx)
	var err error
	input, err = normalizeExternalState(input)
	if err != nil {
		return ChildRunResult{}, fmt.Errorf("child run input: %w", err)
	}
	request.ParentRunID = strings.TrimSpace(request.ParentRunID)
	request.ParentStepID = strings.TrimSpace(request.ParentStepID)
	request.ParentTaskID = strings.TrimSpace(request.ParentTaskID)
	request.GraphRef = strings.TrimSpace(request.GraphRef)
	request.Namespace = strings.Trim(strings.TrimSpace(request.Namespace), "/")
	if request.ParentRunID == "" || request.ParentStepID == "" || request.ParentTaskID == "" || request.GraphRef == "" {
		return ChildRunResult{}, fmt.Errorf("child run requires parent_run_id, parent_step_id, parent_task_id, and graph_ref")
	}
	metadata, metadataOK := RunnerMetadataFromContext(ctx)
	if !metadataOK || strings.TrimSpace(metadata.RunID) == "" {
		return ChildRunResult{}, errors.New("child run requires runner metadata")
	}
	if metadata.RunID != request.ParentRunID || metadata.StepID != request.ParentStepID || metadata.TaskID != request.ParentTaskID {
		return ChildRunResult{}, fmt.Errorf("child run parent identity does not match runner metadata")
	}
	parentRun, err := r.executionStore.GetRun(ctx, request.ParentRunID)
	if err != nil {
		return ChildRunResult{}, fmt.Errorf("load parent run %q: %w", request.ParentRunID, err)
	}
	if parentRunner, ok := GraphRunnerFromContext(ctx); ok && parentRunner != nil {
		if err := parentRunner.validateRunGraphHash(parentRun); err != nil {
			return ChildRunResult{}, fmt.Errorf("validate parent run %q: %w", parentRun.RunID, err)
		}
	}
	if !isActiveDeleteRunStatus(parentRun.Status) && parentRun.Status != RunStatusPaused {
		return ChildRunResult{}, fmt.Errorf("parent run %q status %q cannot create or resume a child", parentRun.RunID, parentRun.Status)
	}
	if request.Namespace == "" {
		request.Namespace = strings.Trim(parentRun.Namespace+"/"+request.GraphRef+":"+request.ParentTaskID, "/")
	}
	keyFields := []struct {
		name  string
		value string
	}{
		{name: "parent run ID", value: request.ParentRunID},
		{name: "parent task ID", value: request.ParentTaskID},
		{name: "graph ref", value: request.GraphRef},
		{name: "namespace", value: request.Namespace},
	}
	for _, field := range keyFields {
		if strings.ContainsRune(field.value, '\x00') {
			return ChildRunResult{}, fmt.Errorf("child run %s must not contain NUL", field.name)
		}
	}
	if input == nil {
		input = state.NewState()
	}
	inputHash, err := childRunInputHash(input)
	if err != nil {
		return ChildRunResult{}, err
	}
	requestKey := childRunRequestKey(request)
	childRunID := childRunIDForRequestKey(requestKey)
	childCtx := WithGraphRunner(ctx, r)
	r.childRunMu.Lock()
	existing, err := r.findChildRun(childCtx, request, requestKey)
	if err != nil {
		r.childRunMu.Unlock()
		return ChildRunResult{}, err
	}
	proposed := PendingChildRun{
		RequestKey: requestKey, ChildRunID: childRunID,
		ParentRunID: request.ParentRunID, ParentStepID: request.ParentStepID, ParentTaskID: request.ParentTaskID,
		GraphRef: request.GraphRef, GraphID: r.resolvedGraphID(), GraphVersion: r.resolvedGraphVersion(),
		GraphHash: r.resolvedGraphHash(), GraphSnapshotHash: r.resolvedGraphSnapshotHash(), GraphSessionID: r.resolvedGraphSessionID(),
		Namespace: request.Namespace, InputHash: inputHash, ReservedAt: r.currentTime(),
	}
	if existing != nil {
		if strings.TrimSpace(existing.ParentStepID) == "" {
			r.childRunMu.Unlock()
			return ChildRunResult{}, fmt.Errorf("child run %q parent step ID is empty", existing.RunID)
		}
		proposed.ParentStepID = existing.ParentStepID
		if err := validateReservedChildRun(*existing, proposed); err != nil {
			r.childRunMu.Unlock()
			return ChildRunResult{}, err
		}
		lineage := ChildRunLineage{
			ParentRunID: proposed.ParentRunID, ParentStepID: proposed.ParentStepID, ParentTaskID: proposed.ParentTaskID,
			RootRunID: parentRun.RootRunID, ParentRunPath: append([]string(nil), parentRun.RunPath...), Namespace: proposed.Namespace,
		}
		childCtx = WithChildRunLineage(childCtx, lineage)
		if err := r.finalizeChildRun(childCtx, parentRun.RunID, requestKey, existing.RunID); err != nil {
			r.childRunMu.Unlock()
			return ChildRunResult{Run: *existing, State: input, Resumed: true}, err
		}
		r.childRunMu.Unlock()
		return r.continueChildRun(childCtx, request, *existing, input, true)
	}

	reservation, err := r.reserveChildRun(childCtx, parentRun.RunID, proposed)
	if err != nil {
		r.childRunMu.Unlock()
		return ChildRunResult{}, err
	}
	lineage := ChildRunLineage{
		ParentRunID: reservation.ParentRunID, ParentStepID: reservation.ParentStepID, ParentTaskID: reservation.ParentTaskID,
		RootRunID: parentRun.RootRunID, ParentRunPath: append([]string(nil), parentRun.RunPath...), Namespace: reservation.Namespace,
	}
	childCtx = WithChildRunLineage(childCtx, lineage)

	run, initialState, err := r.startRun(childCtx, input, &reservation)
	if err != nil {
		persisted, loadErr := r.executionStore.GetRun(childCtx, reservation.ChildRunID)
		if loadErr != nil {
			r.childRunMu.Unlock()
			return ChildRunResult{}, err
		}
		run = persisted
		initialState = input
	}
	if err := validateReservedChildRun(run, reservation); err != nil {
		r.childRunMu.Unlock()
		return ChildRunResult{}, err
	}
	if err := r.finalizeChildRun(childCtx, parentRun.RunID, requestKey, run.RunID); err != nil {
		r.childRunMu.Unlock()
		return ChildRunResult{Run: run, State: initialState}, err
	}
	r.childRunMu.Unlock()
	guard, _ := executionLeaseGuard(run)
	return r.continueChildRun(withExecutionLeaseGuard(childCtx, guard), request, run, initialState, false)
}

func (r *GraphRunner) findChildRun(ctx context.Context, request ChildRunRequest, requestKey string) (*RunRecord, error) {
	runID := childRunIDForRequestKey(requestKey)
	run, err := r.executionStore.GetRun(ctx, runID)
	if errors.Is(err, ErrRunnerRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validateChildRunRecordIdentity(run); err != nil {
		return nil, err
	}
	if run.ChildRequestKey != requestKey || run.ParentRunID != request.ParentRunID || run.ParentTaskID != request.ParentTaskID {
		return nil, fmt.Errorf("child run %q identity does not match request key %q", run.RunID, requestKey)
	}
	return &run, nil
}

func childRunRequestKey(request ChildRunRequest) string {
	payload, _ := json.Marshal([]string{
		request.ParentRunID,
		request.ParentTaskID,
		request.GraphRef,
		request.Namespace,
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:])
}

func childRunIDForRequestKey(requestKey string) string {
	return "child-" + requestKey
}

func childRunInputHash(input *state.State) (string, error) {
	if input == nil {
		input = state.NewState()
	}
	payload, err := json.Marshal(input.Export())
	if err != nil {
		return "", fmt.Errorf("hash child run input: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

func (r *GraphRunner) reserveChildRun(ctx context.Context, parentRunID string, proposed PendingChildRun) (PendingChildRun, error) {
	if controller, ok := ChildRunControllerFromContext(ctx); ok {
		return controller.ReserveChildRun(ctx, parentRunID, proposed)
	}
	revisionConflicts := 0
	for {
		parentRun, err := r.executionStore.GetRun(ctx, parentRunID)
		if err != nil {
			return PendingChildRun{}, err
		}
		reservation, changed, err := reservePendingChildRun(&parentRun, proposed, r.currentTime())
		if err != nil {
			return PendingChildRun{}, err
		}
		if !changed {
			return reservation, nil
		}
		if _, err := compareAndSwapRun(ctx, r.executionStore, parentRun); errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return PendingChildRun{}, runRevisionRetriesExceeded("reserve child run")
			}
			continue
		} else if err != nil {
			return PendingChildRun{}, err
		}
		return reservation, nil
	}
}

func (r *GraphRunner) finalizeChildRun(ctx context.Context, parentRunID, requestKey, childRunID string) error {
	if controller, ok := ChildRunControllerFromContext(ctx); ok {
		return controller.FinalizeChildRun(ctx, parentRunID, requestKey, childRunID)
	}
	revisionConflicts := 0
	for {
		parentRun, err := r.executionStore.GetRun(ctx, parentRunID)
		if err != nil {
			return err
		}
		changed, err := finalizePendingChildRun(&parentRun, requestKey, childRunID, r.currentTime())
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if _, err := compareAndSwapRun(ctx, r.executionStore, parentRun); errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("finalize child run")
			}
			continue
		} else if err != nil {
			return err
		}
		return nil
	}
}

func reservePendingChildRun(parentRun *RunRecord, proposed PendingChildRun, now time.Time) (PendingChildRun, bool, error) {
	if parentRun == nil {
		return PendingChildRun{}, false, errors.New("parent run is nil")
	}
	if err := validatePendingChildRun(proposed); err != nil {
		return PendingChildRun{}, false, err
	}
	if parentRun.RunID != proposed.ParentRunID {
		return PendingChildRun{}, false, fmt.Errorf("child reservation parent mismatch: run=%q request=%q", parentRun.RunID, proposed.ParentRunID)
	}
	if !isActiveDeleteRunStatus(parentRun.Status) && parentRun.Status != RunStatusPaused {
		return PendingChildRun{}, false, fmt.Errorf("parent run %q status %q cannot reserve a child", parentRun.RunID, parentRun.Status)
	}
	if parentRun.Deletion != nil {
		return PendingChildRun{}, false, fmt.Errorf("parent run %q is reserved for deletion", parentRun.RunID)
	}
	seenChildRunIDs := make(map[string]struct{}, len(parentRun.ChildRunIDs))
	for _, existingID := range parentRun.ChildRunIDs {
		if _, exists := seenChildRunIDs[existingID]; exists {
			return PendingChildRun{}, false, fmt.Errorf("parent run %q has duplicate child run ID %q", parentRun.RunID, existingID)
		}
		seenChildRunIDs[existingID] = struct{}{}
	}
	_, finalized := seenChildRunIDs[proposed.ChildRunID]
	existing, found, err := pendingChildRunByKey(parentRun.PendingChildRuns, proposed.RequestKey)
	if err != nil {
		return PendingChildRun{}, false, err
	}
	if found {
		if err := validateMatchingPendingChildRun(existing, proposed); err != nil {
			return PendingChildRun{}, false, err
		}
		if finalized {
			return PendingChildRun{}, false, fmt.Errorf("parent run %q child %q is both pending and finalized", parentRun.RunID, proposed.ChildRunID)
		}
		return existing, false, nil
	}
	if finalized {
		return proposed, false, nil
	}
	parentRun.PendingChildRuns = append(append([]PendingChildRun(nil), parentRun.PendingChildRuns...), proposed)
	parentRun.UpdatedAt = now
	return proposed, true, nil
}

func finalizePendingChildRun(parentRun *RunRecord, requestKey, childRunID string, now time.Time) (bool, error) {
	if parentRun == nil {
		return false, errors.New("parent run is nil")
	}
	requestKey = strings.TrimSpace(requestKey)
	childRunID = strings.TrimSpace(childRunID)
	if requestKey == "" {
		return false, errors.New("child request key is required")
	}
	if err := validateRunnerStorageID("child run ID", childRunID); err != nil {
		return false, err
	}
	pendingRuns := make([]PendingChildRun, 0, len(parentRun.PendingChildRuns))
	pendingKeys := make(map[string]struct{}, len(parentRun.PendingChildRuns))
	var matchedPending *PendingChildRun
	for _, pending := range parentRun.PendingChildRuns {
		if _, exists := pendingKeys[pending.RequestKey]; exists {
			return false, fmt.Errorf("parent run %q has duplicate pending child request key %q", parentRun.RunID, pending.RequestKey)
		}
		pendingKeys[pending.RequestKey] = struct{}{}
		if pending.RequestKey == requestKey {
			pendingCopy := pending
			matchedPending = &pendingCopy
			continue
		}
		pendingRuns = append(pendingRuns, pending)
	}
	if matchedPending != nil && matchedPending.ChildRunID != childRunID {
		return false, fmt.Errorf("pending child request %q reserves run %q, not %q", requestKey, matchedPending.ChildRunID, childRunID)
	}
	if matchedPending != nil {
		if err := validatePendingChildRun(*matchedPending); err != nil {
			return false, fmt.Errorf("parent run %q has invalid pending child request %q: %w", parentRun.RunID, requestKey, err)
		}
		if matchedPending.ParentRunID != parentRun.RunID {
			return false, fmt.Errorf("pending child request %q parent is %q, not %q", requestKey, matchedPending.ParentRunID, parentRun.RunID)
		}
	}
	seen := make(map[string]struct{}, len(parentRun.ChildRunIDs))
	linked := false
	for _, existingID := range parentRun.ChildRunIDs {
		if _, exists := seen[existingID]; exists {
			return false, fmt.Errorf("parent run %q has duplicate child run ID %q", parentRun.RunID, existingID)
		}
		seen[existingID] = struct{}{}
		linked = linked || existingID == childRunID
	}
	if matchedPending == nil {
		if !linked {
			return false, fmt.Errorf("child run %q has no pending reservation or finalized link", childRunID)
		}
		return false, nil
	}
	parentRun.PendingChildRuns = pendingRuns
	if !linked {
		parentRun.ChildRunIDs = append(append([]string(nil), parentRun.ChildRunIDs...), childRunID)
	}
	parentRun.UpdatedAt = now
	return true, nil
}

func pendingChildRunByKey(pendingRuns []PendingChildRun, requestKey string) (PendingChildRun, bool, error) {
	var matched PendingChildRun
	count := 0
	for _, pending := range pendingRuns {
		if pending.RequestKey != requestKey {
			continue
		}
		matched = pending
		count++
	}
	if count > 1 {
		return PendingChildRun{}, false, fmt.Errorf("request key %q has %d pending child reservations", requestKey, count)
	}
	return matched, count == 1, nil
}

func validatePendingChildRun(pending PendingChildRun) error {
	requestKey := strings.TrimSpace(pending.RequestKey)
	if requestKey == "" {
		return errors.New("child request key is required")
	}
	if requestKey != pending.RequestKey {
		return errors.New("child request key cannot contain surrounding whitespace")
	}
	if err := validateRunnerStorageID("child run ID", pending.ChildRunID); err != nil {
		return err
	}
	expectedChildRunID := childRunIDForRequestKey(requestKey)
	if pending.ChildRunID != expectedChildRunID {
		return fmt.Errorf("child reservation run ID %q does not match request key %q", pending.ChildRunID, pending.RequestKey)
	}
	required := []struct {
		name  string
		value string
	}{
		{name: "parent run ID", value: pending.ParentRunID},
		{name: "parent step ID", value: pending.ParentStepID},
		{name: "parent task ID", value: pending.ParentTaskID},
		{name: "graph ref", value: pending.GraphRef},
		{name: "graph ID", value: pending.GraphID},
		{name: "graph version", value: pending.GraphVersion},
		{name: "namespace", value: pending.Namespace},
		{name: "input hash", value: pending.InputHash},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("child reservation %s is required", field.name)
		}
	}
	if pending.ReservedAt.IsZero() {
		return errors.New("child reservation time is required")
	}
	return nil
}

func validateMatchingPendingChildRun(existing, proposed PendingChildRun) error {
	fields := []struct {
		name     string
		existing string
		proposed string
	}{
		{name: "child run ID", existing: existing.ChildRunID, proposed: proposed.ChildRunID},
		{name: "parent run ID", existing: existing.ParentRunID, proposed: proposed.ParentRunID},
		{name: "parent task ID", existing: existing.ParentTaskID, proposed: proposed.ParentTaskID},
		{name: "graph ref", existing: existing.GraphRef, proposed: proposed.GraphRef},
		{name: "graph ID", existing: existing.GraphID, proposed: proposed.GraphID},
		{name: "graph version", existing: existing.GraphVersion, proposed: proposed.GraphVersion},
		{name: "graph hash", existing: existing.GraphHash, proposed: proposed.GraphHash},
		{name: "graph snapshot hash", existing: existing.GraphSnapshotHash, proposed: proposed.GraphSnapshotHash},
		{name: "graph session ID", existing: existing.GraphSessionID, proposed: proposed.GraphSessionID},
		{name: "namespace", existing: existing.Namespace, proposed: proposed.Namespace},
		{name: "input hash", existing: existing.InputHash, proposed: proposed.InputHash},
	}
	for _, field := range fields {
		if field.existing != field.proposed {
			return fmt.Errorf("pending child request %q %s changed from %q to %q", existing.RequestKey, field.name, field.existing, field.proposed)
		}
	}
	return validatePendingChildRun(existing)
}

func validateReservedChildRun(run RunRecord, reservation PendingChildRun) error {
	if err := validateChildRunRecordIdentity(run); err != nil {
		return err
	}
	fields := []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "run ID", actual: run.RunID, expected: reservation.ChildRunID},
		{name: "request key", actual: run.ChildRequestKey, expected: reservation.RequestKey},
		{name: "input hash", actual: run.ChildInputHash, expected: reservation.InputHash},
		{name: "parent run ID", actual: run.ParentRunID, expected: reservation.ParentRunID},
		{name: "parent step ID", actual: run.ParentStepID, expected: reservation.ParentStepID},
		{name: "parent task ID", actual: run.ParentTaskID, expected: reservation.ParentTaskID},
		{name: "graph ID", actual: run.GraphID, expected: reservation.GraphID},
		{name: "graph version", actual: run.GraphVersion, expected: reservation.GraphVersion},
		{name: "graph hash", actual: run.GraphHash, expected: reservation.GraphHash},
		{name: "graph snapshot hash", actual: run.GraphSnapshotHash, expected: reservation.GraphSnapshotHash},
		{name: "graph session ID", actual: run.GraphSessionID, expected: reservation.GraphSessionID},
		{name: "namespace", actual: run.Namespace, expected: reservation.Namespace},
	}
	for _, field := range fields {
		if field.actual != field.expected {
			return fmt.Errorf("child run %q %s is %q, want %q", run.RunID, field.name, field.actual, field.expected)
		}
	}
	return nil
}

func validateChildRunRecordIdentity(run RunRecord) error {
	requestKey := strings.TrimSpace(run.ChildRequestKey)
	if requestKey == "" {
		return nil
	}
	expectedRunID := childRunIDForRequestKey(requestKey)
	if run.RunID != expectedRunID {
		return fmt.Errorf("child run %q does not match request key %q", run.RunID, requestKey)
	}
	return nil
}

func validateRunExecutionOwner(ctx context.Context, run RunRecord) error {
	if err := validateChildRunRecordIdentity(run); err != nil {
		return err
	}
	guard, ok := executionLeaseGuardFromContext(ctx)
	if ok {
		return validateExecutionLeaseGuard(run, guard)
	}
	if run.ExecutionLease != nil && run.ExecutionLease.Status == ExecutionLeaseActive {
		return fmt.Errorf("%w: run %q has no execution lease guard", ErrExecutionLeaseLost, run.RunID)
	}
	return nil
}

func (r *GraphRunner) continueChildRun(ctx context.Context, request ChildRunRequest, run RunRecord, input *state.State, resumed bool) (result ChildRunResult, resultErr error) {
	controller, _ := ChildRunControllerFromContext(ctx)
	if controller != nil {
		controller.RegisterChildRun(request.ParentTaskID, r, run.RunID)
		defer controller.UnregisterChildRun(request.ParentTaskID, run.RunID)
	}
	claimedRun, guard, _, err := r.ensureExecutionLease(ctx, run.RunID)
	if err != nil {
		return ChildRunResult{Run: claimedRun, State: input, Resumed: resumed}, err
	}
	run = claimedRun
	if guard.RunID != "" {
		var heartbeat *leaseHeartbeat
		ctx, heartbeat = r.startLeaseHeartbeat(ctx, guard)
		defer func() {
			leaseErr := r.finishExecutionLease(ctx, guard, heartbeat)
			result.Run, leaseErr = r.refreshRunAfterLease(ctx, result.Run, leaseErr)
			resultErr = errors.Join(resultErr, leaseErr)
		}()
	}

	var finalState *state.State
	switch run.Status {
	case RunStatusCompleted:
		if run.LastCheckpointID == "" {
			return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, fmt.Errorf("completed child run %q has no checkpoint", run.RunID)
		}
		checkpoint, loadErr := r.LoadCheckpointState(ctx, run.LastCheckpointID)
		if loadErr != nil {
			return ChildRunResult{Run: run, ReturnValue: run.ReturnValue, Resumed: resumed}, loadErr
		}
		if loadErr := validateCheckpointRun(run, checkpoint); loadErr != nil {
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
	result = ChildRunResult{
		Run: run, State: finalState, ReturnValue: run.ReturnValue,
		Interrupted: run.Status == RunStatusPaused, Resumed: resumed,
	}
	return result, err
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
	run, initialState, err := r.startRun(ctx, initialState, nil)
	if err != nil {
		return RunRecord{}, nil, err
	}
	guard, _ := executionLeaseGuard(run)

	done := make(chan struct{})
	go func() {
		defer close(done)
		executionCtx, heartbeat := r.startLeaseHeartbeat(ctx, guard)
		defer func() {
			if finishErr := r.finishExecutionLease(executionCtx, guard, heartbeat); finishErr != nil {
				logger.Error("finish async execution lease", append(runLogFields(executionCtx, run), zap.Error(finishErr))...)
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				r.failAsyncExecution(context.WithoutCancel(executionCtx), run, initialState, "async_execution_panic", fmt.Sprintf("panic: %v", recovered))
			}
		}()
		finishedRun, finalState, runErr := r.continueStartedRun(executionCtx, run, initialState)
		if runErr != nil && finishedRun.RunID == "" {
			if finalState == nil {
				finalState = initialState
			}
			r.failAsyncExecution(context.WithoutCancel(executionCtx), run, finalState, "async_execution_failed", runErr.Error())
		}
	}()
	return run, done, nil
}

func (r *GraphRunner) EnqueueStart(ctx context.Context, initialState *state.State, queue AtomicTaskQueue) (RunRecord, Task, error) {
	if queue == nil {
		return RunRecord{}, Task{}, errors.New("atomic task queue is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if initialState != nil {
		initialState = initialState.Clone()
	}
	run, _, commit, err := r.prepareRun(ctx, initialState, nil, false)
	if err != nil {
		return RunRecord{}, Task{}, err
	}
	now := r.currentTime()
	task := Task{
		ID: run.RunID, Kind: TaskKindGraphRun, RunID: run.RunID, GraphTaskID: run.EntryNodeID,
		GraphID: run.GraphID, GraphSessionID: run.GraphSessionID, CheckpointID: run.LastCheckpointID,
		Status: TaskStatusQueued, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	queued, result, err := queue.EnqueueWithCommit(ctx, task, commit)
	if err != nil {
		return RunRecord{}, Task{}, err
	}
	if result.Run != nil {
		run = *result.Run
	}
	logger.Info("run queued", append(runLogFields(ctx, run), state.SummaryFields(initialState)...)...)
	return run, queued, nil
}

func (r *GraphRunner) startRun(ctx context.Context, initialState *state.State, reservation *PendingChildRun) (RunRecord, *state.State, error) {
	run, initialState, commit, err := r.prepareRun(ctx, initialState, reservation, true)
	if err != nil {
		return RunRecord{}, initialState, err
	}
	commitResult, err := r.commitRuntime(ctx, commit)
	if err != nil {
		return RunRecord{}, initialState, err
	}
	if commitResult.Run != nil {
		run = *commitResult.Run
	}
	logger.Info("run started", append(runLogFields(ctx, run), state.SummaryFields(initialState)...)...)
	return run, initialState, nil
}

func (r *GraphRunner) prepareRun(ctx context.Context, initialState *state.State, reservation *PendingChildRun, active bool) (RunRecord, *state.State, Commit, error) {
	if err := r.validate(); err != nil {
		return RunRecord{}, initialState, Commit{}, err
	}
	if reservation != nil {
		if err := validatePendingChildRun(*reservation); err != nil {
			return RunRecord{}, initialState, Commit{}, err
		}
	}

	var err error
	initialState, err = normalizeExternalState(initialState)
	if err != nil {
		return RunRecord{}, initialState, Commit{}, fmt.Errorf("entry state: %w", err)
	}
	if initialState == nil {
		initialState = state.NewState()
	}
	if issues := state.ValidateStateBySchemas(initialState, r.stateSchemas); len(issues) > 0 {
		return RunRecord{}, initialState, Commit{}, state.NewValidationError("entry", issues)
	}
	if validator, ok := r.graph.(interface{ ValidateInitialState(*state.State) error }); ok {
		if err := validator.ValidateInitialState(initialState); err != nil {
			return RunRecord{}, initialState, Commit{}, fmt.Errorf("graph initial state: %w", err)
		}
	}

	now := r.currentTime()
	entryPoint := r.entryPointID()
	runID := newRunnerID()
	if reservation != nil {
		runID = reservation.ChildRunID
	}
	run := RunRecord{
		RunID:             runID,
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
	if active {
		run.ExecutionLease = r.newExecutionLease(nil, now)
	}
	if reservation != nil {
		run.ChildRequestKey = reservation.RequestKey
		run.ChildInputHash = reservation.InputHash
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
	if reservation != nil {
		if err := validateReservedChildRun(run, *reservation); err != nil {
			return RunRecord{}, initialState, Commit{}, err
		}
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
		return RunRecord{}, initialState, Commit{}, err
	}

	if active {
		run.Status = RunStatusRunning
	}
	run.CurrentNodeID = run.EntryNodeID
	run.UpdatedAt = r.currentTime()
	events := []Event{createdEvent}
	if active {
		startedEvent, err := r.buildEvent(run, "", "", "", EventRunStarted, nil)
		if err != nil {
			return RunRecord{}, initialState, Commit{}, err
		}
		events = append(events, startedEvent)
	}
	checkpointState := initialState.Clone()
	if err := StoreGraphSchedule(checkpointState, GraphSchedule{CurrentTasks: []GraphTask{NewStaticGraphTask(run.EntryNodeID, 0)}}); err != nil {
		return RunRecord{}, initialState, Commit{}, fmt.Errorf("store initial graph schedule: %w", err)
	}
	checkpointWrite, checkpointEvent, err := r.buildCheckpointWrite(ctx, run, StepRecord{TaskID: run.EntryNodeID}, run.EntryNodeID, CheckpointBeforeNode, checkpointState, 0, nil, nil)
	if err != nil {
		return RunRecord{}, initialState, Commit{}, err
	}
	run.LastCheckpointID = checkpointWrite.Record.CheckpointID
	events = append(events, checkpointEvent)
	commit := Commit{
		TransactionID: newRunnerID(),
		Run:           &RunWrite{Mode: RunWriteCreate, Run: run},
		Checkpoints:   []CheckpointWrite{checkpointWrite},
		Events:        events,
	}
	return run, initialState, commit, nil
}

func (r *GraphRunner) continueStartedRun(ctx context.Context, run RunRecord, initialState *state.State) (RunRecord, *state.State, error) {
	if err := r.publishStartupWarnings(ctx, run); err != nil {
		return RunRecord{}, initialState, err
	}
	return r.execute(ctx, run, initialState.Clone(), []GraphTask{NewStaticGraphTask(run.EntryNodeID, 0)}, nil, nil)
}

func (r *GraphRunner) Resume(ctx context.Context, runID string, input *state.State) (result RunRecord, finalState *state.State, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validate(); err != nil {
		return RunRecord{}, nil, err
	}
	run, guard, acquired, err := r.ensureExecutionLease(ctx, runID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if acquired {
		var heartbeat *leaseHeartbeat
		ctx, heartbeat = r.startLeaseHeartbeat(ctx, guard)
		defer func() {
			leaseErr := r.finishExecutionLease(ctx, guard, heartbeat)
			result, leaseErr = r.refreshRunAfterLease(ctx, result, leaseErr)
			resultErr = errors.Join(resultErr, leaseErr)
		}()
	} else if guard.RunID != "" {
		ctx = withExecutionLeaseGuard(ctx, guard)
	}
	if err := r.validateRunGraphHash(run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := validateRunExecutionOwner(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.ensureRunEffectsResolved(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if strings.TrimSpace(run.LastCheckpointID) == "" {
		return RunRecord{}, nil, fmt.Errorf("resume run %q: no checkpoint available", runID)
	}

	checkpoint, err := r.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if err := validateCheckpointRun(run, checkpoint); err != nil {
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

func (r *GraphRunner) ResumeFromCheckpoint(ctx context.Context, checkpointID string, input *state.State) (result RunRecord, finalState *state.State, resultErr error) {
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
	run, guard, acquired, err := r.ensureExecutionLease(ctx, checkpoint.Record.RunID)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if acquired {
		var heartbeat *leaseHeartbeat
		ctx, heartbeat = r.startLeaseHeartbeat(ctx, guard)
		defer func() {
			leaseErr := r.finishExecutionLease(ctx, guard, heartbeat)
			result, leaseErr = r.refreshRunAfterLease(ctx, result, leaseErr)
			resultErr = errors.Join(resultErr, leaseErr)
		}()
	} else if guard.RunID != "" {
		ctx = withExecutionLeaseGuard(ctx, guard)
	}
	if err := r.validateRunGraphHash(run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := validateRunExecutionOwner(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := r.ensureRunEffectsResolved(ctx, run); err != nil {
		return RunRecord{}, nil, err
	}
	if err := validateCheckpointRun(run, checkpoint); err != nil {
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
		if !r.runBelongsToRunner(run) {
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
	if err := validateRunExecutionOwner(ctx, run); err != nil {
		return RunRecord{}, currentState, err
	}
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
	fields := append(runLogFields(ctx, run),
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
		failedRun, failedState, failErr := r.failRunWithTransition(ctx, execution.currentRun(), finalState, "callback_failed", callbackErr.Error(), transition)
		if failedRun.Status == RunStatusFailed {
			execution.deleteNodeTaskFailureLeases(transition.taskFailures)
		}
		return failedRun, failedState, failErr
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
	failedRun, persistErr := r.persistRunFailureWithTransition(ctx, execution.currentRun(), finalState, errorCode, invokeErr.Error(), transition)
	if persistErr != nil {
		return failedRun, finalState, persistErr
	}
	execution.deleteNodeTaskFailureLeases(transition.taskFailures)
	if failedRun.Status != RunStatusFailed {
		return failedRun, finalState, nil
	}
	return failedRun, finalState, invokeErr
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
				checkpointID, checkpointRun, checkpointErr := r.saveCheckpoint(ctx, execution.currentRun(), active.step, active.step.NodeID, CheckpointBeforeNode, currentState, active.attempts, control.hit, execution.snapshotArtifacts())
				if checkpointErr != nil {
					run, finalState, err := r.failRun(ctx, checkpointRun, currentState, "interrupt_failed", fmt.Sprintf("refresh pause checkpoint for %q: %v", active.step.NodeID, checkpointErr))
					return run, finalState, true, err
				}
				active.step.CheckpointBeforeID = checkpointID
				run, finalState, err := r.pauseRun(ctx, checkpointRun, currentState, active.step, checkpointID, control.hit, control.message)
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
	input, err = normalizeExternalState(input)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("resume input: %w", err)
	}
	if checkpoint.Business, err = state.MergeResumeInput(checkpoint.Business, input); err != nil {
		return RunRecord{}, nil, err
	}
	if issues := state.ValidateStateBySchemas(checkpoint.Business, r.stateSchemas); len(issues) > 0 {
		return RunRecord{}, checkpoint.Business, state.NewValidationError("resume input", issues)
	}

	startTasks, skip, suspend, err := r.resumeTarget(ctx, checkpoint.Record, checkpoint.Runtime, checkpoint.Business)
	if err != nil {
		return RunRecord{}, nil, err
	}
	if phase, found, phaseErr := LoadAgentResumeState(checkpoint.Business); phaseErr != nil {
		return RunRecord{}, nil, phaseErr
	} else if found {
		ctx = WithAgentResumePhase(ctx, phase)
		if err := ClearAgentResumeState(checkpoint.Business); err != nil {
			return RunRecord{}, nil, err
		}
	}

	expectedRun := run
	desiredRun := run
	desiredRun.Status = RunStatusRunning
	desiredRun.PauseRequested = false
	desiredRun.CancelRequested = false
	desiredRun.ErrorCode = ""
	desiredRun.ErrorMessage = ""
	desiredRun.FinishedAt = nil
	if checkpoint.Runtime.CurrentStepID != "" {
		desiredRun.LastStepID = checkpoint.Runtime.CurrentStepID
	}
	resolvedToEnd := suspend == nil && (len(startTasks) == 0 || len(startTasks) == 1 && startTasks[0].NodeID == EndNodeID)
	desiredRun.CurrentNodeID = checkpoint.Runtime.CurrentNodeID
	if suspend == nil && (checkpoint.Record.Stage != CheckpointBeforeNode || desiredRun.CurrentNodeID == "") {
		if len(startTasks) > 0 {
			desiredRun.CurrentNodeID = startTasks[0].NodeID
		}
	}
	if suspend != nil {
		desiredRun.NextNodeIDs = GraphTaskNodeIDs(startTasks)
	}
	if resolvedToEnd {
		clearRunExecutionPointers(&desiredRun)
	}
	updatedRun, err := r.commitRunUpdateWithRetry(ctx, expectedRun, "resume run", func(latestRun RunRecord) (runUpdatePreparation, error) {
		if latestRun.CancelRequested {
			return r.prepareCanceledRunUpdate(latestRun, runnerStepTransition{})
		}
		if latestRun.Status != expectedRun.Status && !isActiveDeleteRunStatus(latestRun.Status) {
			return runUpdatePreparation{run: latestRun}, nil
		}
		latestRun.Status = RunStatusRunning
		latestRun.CancelRequested = false
		latestRun.ErrorCode = ""
		latestRun.ErrorMessage = ""
		latestRun.FinishedAt = nil
		latestRun.LastStepID = desiredRun.LastStepID
		latestRun.CurrentNodeID = desiredRun.CurrentNodeID
		latestRun.CurrentNodeIDs = append([]string(nil), desiredRun.CurrentNodeIDs...)
		latestRun.CurrentStepIDs = append([]string(nil), desiredRun.CurrentStepIDs...)
		latestRun.NextNodeIDs = append([]string(nil), desiredRun.NextNodeIDs...)
		latestRun.ParallelWaveID = desiredRun.ParallelWaveID
		latestRun.UpdatedAt = r.currentTime()
		resumedEvent, buildErr := r.buildEvent(latestRun, "", "", "", EventRunResumed, map[string]any{
			"checkpoint_id": checkpoint.Record.CheckpointID,
			"node_id":       latestRun.CurrentNodeID,
			"node_ids":      GraphTaskNodeIDs(startTasks),
			"tasks":         startTasks,
		})
		if buildErr != nil {
			return runUpdatePreparation{}, buildErr
		}
		commit := Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: latestRun},
			Events: []Event{resumedEvent},
		}
		return runUpdatePreparation{run: latestRun, commit: &commit}, nil
	})
	if err != nil {
		return updatedRun, checkpoint.Business, err
	}
	run = updatedRun
	if run.Status != RunStatusRunning {
		logger.Info("run transition won before resume", append(runLogFields(ctx, run), state.SummaryFields(checkpoint.Business)...)...)
		if isTerminalRunStatus(run.Status) {
			if err := r.applyRunRetention(context.WithoutCancel(normalizeRunnerContext(ctx)), run.RunID); err != nil {
				return run, checkpoint.Business, err
			}
		}
		return run, checkpoint.Business, nil
	}
	if resolvedToEnd {
		logger.Info("resume resolved to completed run", append(runLogFields(ctx, run), state.SummaryFields(checkpoint.Business)...)...)
		return r.completeRun(ctx, run, checkpoint.Business, checkpoint.Artifacts)
	}
	if suspend != nil {
		checkpointID, checkpointRun, checkpointErr := r.saveCheckpoint(ctx, run, StepRecord{}, waveCheckpointNodeID, CheckpointAfterWave, checkpoint.Business, 0, nil, checkpoint.Artifacts)
		if checkpointErr != nil {
			return r.failRun(ctx, checkpointRun, checkpoint.Business, "checkpoint_failed", checkpointErr.Error())
		}
		run = checkpointRun
		message := fmt.Sprintf("graph interrupted at node %s: %v", checkpoint.Record.NodeID, suspend.Value)
		return r.pauseRunAtCheckpoint(ctx, run, checkpoint.Business, checkpointID, nil, message)
	}

	fields := append(runLogFields(ctx, run),
		zap.Strings("start_nodes", GraphTaskNodeIDs(startTasks)),
		zap.String("resume_checkpoint_id", checkpoint.Record.CheckpointID),
		zap.Int("artifact_count", len(checkpoint.Artifacts)),
	)
	fields = append(fields, state.SummaryFields(checkpoint.Business)...)
	logger.Info("resuming run", fields...)
	return r.execute(ctx, run, checkpoint.Business, startTasks, skip, checkpoint.Artifacts)
}

func (r *GraphRunner) continueRun(ctx context.Context, run RunRecord, checkpoint RestoredCheckpoint, input *state.State) (RunRecord, *state.State, error) {
	if err := r.validateIndependentCheckpoint(checkpoint); err != nil {
		return RunRecord{}, nil, err
	}
	var err error
	input, err = normalizeExternalState(input)
	if err != nil {
		return RunRecord{}, nil, fmt.Errorf("continuation input: %w", err)
	}
	continuedState, err := state.PrepareContinuationState(checkpoint.Business, input)
	if err != nil {
		return RunRecord{}, nil, err
	}
	continuedState = projectBusinessState(continuedState)
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
		if r.hasActiveExecution(runID) {
			run, err = r.persistReservedControlRequest(ctx, runID, runnerControlPause)
			if err != nil {
				return err
			}
			logger.Info("pause requested during execution startup", runLogFields(ctx, run)...)
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
	logger.Info("pause requested", runLogFields(ctx, run)...)
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
		logger.Info("run canceled", runLogFields(ctx, canceledRun)...)
		return r.applyRunRetention(context.WithoutCancel(normalizeRunnerContext(ctx)), canceledRun.RunID)
	case RunStatusPending, RunStatusRunning:
	default:
		return fmt.Errorf("%w: run %q status %q cannot be canceled", ErrRunControlNotAllowed, runID, run.Status)
	}
	execution := r.activeExecution(runID)
	if execution == nil {
		if r.hasActiveExecution(runID) {
			run, err = r.persistReservedControlRequest(ctx, runID, runnerControlCancel)
			if err != nil {
				return err
			}
			logger.Info("cancel requested during execution startup", runLogFields(ctx, run)...)
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
	logger.Info("cancel requested", runLogFields(ctx, run)...)
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
	if err := r.validateRunGraphHash(run); err != nil {
		return RunRecord{}, err
	}
	if isActiveDeleteRunStatus(run.Status) || r.hasActiveExecution(runID) {
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

func (r *GraphRunner) ReconcileRunDeletions(ctx context.Context) error {
	if r == nil || r.runDeleter == nil {
		return nil
	}
	reconciler, ok := r.runDeleter.(interface {
		ReconcileRunDeletions(context.Context) error
	})
	if !ok {
		return nil
	}
	return reconciler.ReconcileRunDeletions(ctx)
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

func (r *GraphRunner) persistReservedControlRequest(ctx context.Context, runID string, kind runnerControlKind) (RunRecord, error) {
	ctx = normalizeRunnerContext(ctx)
	revisionConflicts := 0
	for {
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
		commitResult, err := r.commitRuntime(ctx, Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
			Events: []Event{event},
		})
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunRecord{}, runRevisionRetriesExceeded("persist reserved run control request")
			}
			continue
		}
		if err != nil {
			return RunRecord{}, err
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		return run, nil
	}
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
	if r == nil || strings.TrimSpace(runID) == "" {
		return false
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	return r.activeExecutions != nil && r.activeExecutions[runID] != nil
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
	if r.hasActiveExecution(runID) {
		return run, fmt.Errorf("%w: run %q still has an active execution", ErrRunControlNotAllowed, runID)
	}
	control, err := NewRunControlService(r.executionStore, r.transactionStore, r.eventSink, nil)
	if err != nil {
		return RunRecord{}, err
	}
	control, err = control.WithNow(r.currentTime)
	if err != nil {
		return RunRecord{}, err
	}
	return control.MarkRunExecutionLost(ctx, runID)
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
	fields := append(runLogFields(ctx, run),
		zap.String("interrupt_node_id", interrupt.NodeID),
		zap.String("interrupt_reason", redactSensitiveString(ctx, interrupt.Error())),
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
				schedule, _, scheduleErr := LoadGraphSchedule(checkpointState)
				if scheduleErr != nil {
					return r.failRun(ctx, run, currentState, "checkpoint_failed", scheduleErr.Error())
				}
				if err := StoreGraphSchedule(checkpointState, GraphSchedule{
					CurrentTasks:      []GraphTask{active.task},
					PendingFanInTasks: CloneGraphTasks(schedule.PendingFanInTasks),
				}); err != nil {
					return r.failRun(ctx, run, currentState, "checkpoint_failed", err.Error())
				}
				savedID, checkpointRun, err := r.saveCheckpoint(ctx, run, step, step.NodeID, CheckpointBeforeNode, checkpointState, active.attempts, control.hit, execution.snapshotArtifacts())
				if err != nil {
					return r.failRun(ctx, checkpointRun, currentState, "checkpoint_failed", err.Error())
				}
				run = checkpointRun
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
	expectedRun := run
	persistenceCtx := context.WithoutCancel(normalizeRunnerContext(ctx))
	updatedRun, err := r.commitRunUpdateWithRetry(persistenceCtx, expectedRun, "complete run", func(latestRun RunRecord) (runUpdatePreparation, error) {
		switch {
		case latestRun.CancelRequested:
			return r.prepareCanceledRunUpdate(latestRun, runnerStepTransition{})
		case latestRun.PauseRequested:
			return r.preparePausedRunUpdate(persistenceCtx, latestRun, runnerStepTransition{}, "pause requested before run completion")
		case !isActiveDeleteRunStatus(latestRun.Status):
			return runUpdatePreparation{run: latestRun}, nil
		}

		now := r.currentTime()
		latestRun.Status = RunStatusCompleted
		latestRun.PauseRequested = false
		latestRun.CancelRequested = false
		latestRun.ErrorCode = ""
		latestRun.ErrorMessage = ""
		latestRun.ReturnValue = expectedRun.ReturnValue
		clearRunExecutionPointers(&latestRun)
		latestRun.UpdatedAt = now
		latestRun.FinishedAt = &now
		finalStep := StepRecord{StepID: latestRun.LastStepID}
		checkpointWrite, checkpointEvent, buildErr := r.buildCheckpointWrite(persistenceCtx, latestRun, finalStep, "__final__", CheckpointFinal, finalState, 0, nil, artifacts)
		if buildErr != nil {
			return runUpdatePreparation{}, buildErr
		}
		latestRun.LastCheckpointID = checkpointWrite.Record.CheckpointID
		payload := any(nil)
		if latestRun.ReturnValue != nil {
			payload = map[string]any{"return_value": latestRun.ReturnValue}
		}
		finishedEvent, buildErr := r.buildEvent(latestRun, latestRun.LastStepID, "", "", EventRunFinished, payload)
		if buildErr != nil {
			return runUpdatePreparation{}, buildErr
		}
		commit := Commit{
			Run:         &RunWrite{Mode: RunWriteUpdate, Run: latestRun},
			Checkpoints: []CheckpointWrite{checkpointWrite},
			Events:      []Event{checkpointEvent, finishedEvent},
		}
		return runUpdatePreparation{run: latestRun, commit: &commit}, nil
	})
	if err != nil {
		return updatedRun, finalState, err
	}
	switch updatedRun.Status {
	case RunStatusCompleted:
		logger.Info("run completed", append(runLogFields(ctx, updatedRun), state.SummaryFields(finalState)...)...)
	case RunStatusCanceled:
		logger.Info("run canceled", append(runLogFields(ctx, updatedRun), state.SummaryFields(finalState)...)...)
	case RunStatusPaused:
		logger.Info("run paused before completion", append(runLogFields(ctx, updatedRun), state.SummaryFields(finalState)...)...)
	}
	if isTerminalRunStatus(updatedRun.Status) {
		if err := r.applyRunRetention(persistenceCtx, updatedRun.RunID); err != nil {
			return updatedRun, finalState, err
		}
	}
	return updatedRun, finalState, nil
}

func clearRunExecutionPointers(run *RunRecord) {
	run.CurrentNodeID = ""
	run.CurrentNodeIDs = nil
	run.CurrentStepIDs = nil
	run.NextNodeIDs = nil
	run.ParallelWaveID = ""
}

func (r *GraphRunner) cancelRunWithTransition(ctx context.Context, run RunRecord, currentState *state.State, transition runnerStepTransition) (RunRecord, *state.State, error) {
	persistenceCtx := context.WithoutCancel(normalizeRunnerContext(ctx))
	updatedRun, err := r.commitRunUpdateWithRetry(persistenceCtx, run, "cancel run", func(latestRun RunRecord) (runUpdatePreparation, error) {
		if isTerminalRunStatus(latestRun.Status) {
			return runUpdatePreparation{run: latestRun}, nil
		}
		return r.prepareCanceledRunUpdate(latestRun, transition)
	})
	if err != nil {
		return updatedRun, currentState, err
	}
	if updatedRun.Status == RunStatusCanceled {
		logger.Info("run canceled", append(runLogFields(ctx, updatedRun), state.SummaryFields(currentState)...)...)
		if err := r.applyRunRetention(persistenceCtx, updatedRun.RunID); err != nil {
			return updatedRun, currentState, err
		}
	}
	return updatedRun, currentState, nil
}

type runUpdatePreparation struct {
	run          RunRecord
	commit       *Commit
	taskFailures []TaskFailureTransition
}

func (r *GraphRunner) commitRunUpdateWithRetry(ctx context.Context, expectedRun RunRecord, action string, prepare func(RunRecord) (runUpdatePreparation, error)) (RunRecord, error) {
	latestRun := expectedRun
	for retry := 0; retry < runRevisionRetryLimit; retry++ {
		var err error
		latestRun, err = r.loadRunForUpdate(ctx, expectedRun)
		if err != nil {
			return latestRun, err
		}
		prepared, err := prepare(latestRun)
		if err != nil {
			return latestRun, err
		}
		if prepared.commit == nil {
			return prepared.run, nil
		}
		commitCtx := ctx
		if len(prepared.taskFailures) > 0 {
			commitCtx = withTaskFailures(ctx, prepared.taskFailures)
		}
		commitResult, commitErr := r.commitRuntime(commitCtx, *prepared.commit)
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			continue
		}
		if commitErr != nil {
			return prepared.run, commitErr
		}
		if commitResult.Run != nil {
			prepared.run = *commitResult.Run
		}
		return prepared.run, nil
	}
	return latestRun, runRevisionRetriesExceeded(action)
}

func (r *GraphRunner) loadRunForUpdate(ctx context.Context, expectedRun RunRecord) (RunRecord, error) {
	latestRun, err := r.executionStore.GetRun(ctx, expectedRun.RunID)
	if err != nil {
		return RunRecord{}, err
	}
	if err := validateRunExecutionOwner(ctx, latestRun); err != nil {
		return latestRun, err
	}
	if latestRun.Deletion != nil {
		return latestRun, fmt.Errorf("%w: run %q is reserved for deletion", ErrRunControlNotAllowed, latestRun.RunID)
	}
	if !executionLeaseIdentitiesEqual(expectedRun.ExecutionLease, latestRun.ExecutionLease) {
		return latestRun, fmt.Errorf("%w: run %q execution lease changed", ErrExecutionLeaseLost, latestRun.RunID)
	}
	latestRun.PauseRequested = latestRun.PauseRequested || expectedRun.PauseRequested
	latestRun.CancelRequested = latestRun.CancelRequested || expectedRun.CancelRequested
	if latestRun.CancelRequested {
		latestRun.PauseRequested = false
	}
	return latestRun, nil
}

func (r *GraphRunner) prepareCanceledRunUpdate(run RunRecord, transition runnerStepTransition) (runUpdatePreparation, error) {
	now := r.currentTime()
	run.Status = RunStatusCanceled
	run.PauseRequested = false
	run.CancelRequested = false
	run.UpdatedAt = now
	run.FinishedAt = &now
	canceledEvent, err := r.buildEvent(run, "", "", run.CurrentNodeID, EventRunCanceled, nil)
	if err != nil {
		return runUpdatePreparation{}, err
	}
	events := append([]Event(nil), transition.events...)
	events = append(events, canceledEvent)
	commit := Commit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:  transition.writes,
		Events: events,
	}
	return runUpdatePreparation{run: run, commit: &commit}, nil
}

func (r *GraphRunner) preparePausedRunUpdate(ctx context.Context, run RunRecord, transition runnerStepTransition, message string) (runUpdatePreparation, error) {
	checkpointID := strings.TrimSpace(run.LastCheckpointID)
	if checkpointID == "" {
		for _, step := range transition.steps {
			checkpointID = firstNonEmpty(step.CheckpointBeforeID, step.CheckpointAfterID)
			if checkpointID != "" {
				break
			}
		}
	}
	if checkpointID == "" {
		return runUpdatePreparation{}, fmt.Errorf("pause run %q before terminal transition: no checkpoint available", run.RunID)
	}
	if r.checkpointStore == nil {
		return runUpdatePreparation{}, errors.New("checkpoint store is required")
	}
	checkpoint, _, err := r.checkpointStore.Load(ctx, checkpointID)
	if err != nil {
		return runUpdatePreparation{}, err
	}
	if checkpoint.RunID != run.RunID {
		return runUpdatePreparation{}, fmt.Errorf("checkpoint %q belongs to run %q, not run %q", checkpoint.CheckpointID, checkpoint.RunID, run.RunID)
	}
	if checkpoint.Stage == CheckpointFinal {
		return runUpdatePreparation{}, fmt.Errorf("final checkpoint %q cannot pause run %q", checkpoint.CheckpointID, run.RunID)
	}

	now := r.currentTime()
	run.Status = RunStatusPaused
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.LastCheckpointID = checkpoint.CheckpointID
	run.UpdatedAt = now
	run.FinishedAt = nil
	stepWrites := make([]StepWrite, 0, len(transition.steps))
	for _, transitionStep := range transition.steps {
		if transitionStep.Status == StepStatusSucceeded || transitionStep.Status == StepStatusCanceled {
			continue
		}
		transitionStep.Status = StepStatusPaused
		transitionStep.ErrorCode = ""
		transitionStep.ErrorMessage = ""
		transitionStep.FinishedAt = nil
		transitionStep.UpdatedAt = now
		stepWrites = append(stepWrites, StepWrite{Mode: StepWriteUpdate, Step: transitionStep})
	}
	pausedEvent, err := r.buildEvent(run, checkpoint.StepID, checkpoint.TaskID, checkpoint.NodeID, EventRunPaused, pauseEventPayload(checkpoint.CheckpointID, checkpoint.Stage, checkpoint.NodeID, message, nil))
	if err != nil {
		return runUpdatePreparation{}, err
	}
	commit := Commit{
		Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps:  stepWrites,
		Events: []Event{pausedEvent},
	}
	return runUpdatePreparation{run: run, commit: &commit}, nil
}

func (r *GraphRunner) saveCheckpoint(ctx context.Context, run RunRecord, step StepRecord, nodeID string, stage CheckpointStage, currentState *state.State, attempts int, hit *state.BreakpointHit, artifacts []state.ArtifactRef) (string, RunRecord, error) {
	for retry := 0; retry < runRevisionRetryLimit; retry++ {
		if err := normalizeRunnerContext(ctx).Err(); err != nil {
			return "", run, err
		}
		persistedRun, err := r.executionStore.GetRun(ctx, run.RunID)
		if err != nil {
			return "", run, err
		}
		if err := validateRunExecutionOwner(ctx, persistedRun); err != nil {
			return "", persistedRun, err
		}
		if err := validateNodeExecutionRun(run, persistedRun); err != nil {
			return "", persistedRun, err
		}
		run = persistedRun
		write, event, err := r.buildCheckpointWrite(ctx, run, step, nodeID, stage, currentState, attempts, hit, artifacts)
		if err != nil {
			return "", run, err
		}
		if _, err := r.commitRuntime(ctx, Commit{
			Run:         &RunWrite{Mode: RunWriteCheck, Run: run},
			Checkpoints: []CheckpointWrite{write},
			Events:      []Event{event},
		}); !errors.Is(err, ErrRunRevisionConflict) {
			if err != nil {
				return "", run, err
			}
			return write.Record.CheckpointID, run, nil
		}
	}
	return "", run, runRevisionRetriesExceeded("save checkpoint")
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

func (r *GraphRunner) computeStateDiff(before, after *state.State) ([]state.Change, error) {
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
	stage := pauseCheckpointStage(step, checkpointID)
	expectedRun := run
	for retry := 0; retry < runRevisionRetryLimit; retry++ {
		var err error
		run, err = r.loadPauseRun(ctx, expectedRun)
		if err != nil {
			return run, currentState, err
		}
		if run.CancelRequested {
			transition, buildErr := r.buildPauseCancellationTransition(run, step, stage)
			if buildErr != nil {
				return run, currentState, buildErr
			}
			return r.cancelRunWithTransition(ctx, run, currentState, transition)
		}

		now := r.currentTime()
		run.Status = RunStatusPaused
		run.PauseRequested = false
		run.CancelRequested = false
		run.LastCheckpointID = checkpointID
		run.UpdatedAt = now
		run.FinishedAt = nil
		stepWrites := []StepWrite(nil)
		if stage != CheckpointAfterNode {
			step.Status = StepStatusPaused
			step.UpdatedAt = now
			stepWrites = append(stepWrites, StepWrite{Mode: StepWriteUpdate, Step: step})
		}
		events := make([]Event, 0, 2)
		if hit != nil {
			event, buildErr := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventBreakpointHit, hit)
			if buildErr != nil {
				return run, currentState, buildErr
			}
			events = append(events, event)
		}
		pausedEvent, buildErr := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventRunPaused, pauseEventPayload(checkpointID, stage, step.NodeID, message, hit))
		if buildErr != nil {
			return run, currentState, buildErr
		}
		events = append(events, pausedEvent)
		commitResult, commitErr := r.commitRuntime(ctx, Commit{Run: &RunWrite{Mode: RunWriteUpdate, Run: run}, Steps: stepWrites, Events: events})
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			continue
		}
		if commitErr != nil {
			return run, currentState, commitErr
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		fields := append(runLogFields(ctx, run), stepLogFields(ctx, step)...)
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
	return run, currentState, runRevisionRetriesExceeded("pause run")
}

func (r *GraphRunner) pauseRunAtCheckpoint(ctx context.Context, run RunRecord, currentState *state.State, checkpointID string, hit *state.BreakpointHit, message string) (RunRecord, *state.State, error) {
	expectedRun := run
	for retry := 0; retry < runRevisionRetryLimit; retry++ {
		var err error
		run, err = r.loadPauseRun(ctx, expectedRun)
		if err != nil {
			return run, currentState, err
		}
		if run.CancelRequested {
			return r.cancelRunWithTransition(ctx, run, currentState, runnerStepTransition{})
		}

		now := r.currentTime()
		run.Status = RunStatusPaused
		run.PauseRequested = false
		run.CancelRequested = false
		run.LastCheckpointID = checkpointID
		run.UpdatedAt = now
		run.FinishedAt = nil
		events := make([]Event, 0, 2)
		if hit != nil {
			event, buildErr := r.buildEvent(run, "", "", waveCheckpointNodeID, EventBreakpointHit, hit)
			if buildErr != nil {
				return run, currentState, buildErr
			}
			events = append(events, event)
		}
		pausedEvent, buildErr := r.buildEvent(run, "", "", waveCheckpointNodeID, EventRunPaused, pauseEventPayload(checkpointID, CheckpointAfterWave, waveCheckpointNodeID, message, hit))
		if buildErr != nil {
			return run, currentState, buildErr
		}
		events = append(events, pausedEvent)
		commitResult, commitErr := r.commitRuntime(ctx, Commit{Run: &RunWrite{Mode: RunWriteUpdate, Run: run}, Events: events})
		if errors.Is(commitErr, ErrRunRevisionConflict) {
			continue
		}
		if commitErr != nil {
			return run, currentState, commitErr
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		fields := append(runLogFields(ctx, run), state.SummaryFields(currentState)...)
		if hit != nil {
			fields = append(fields,
				zap.String("breakpoint_id", hit.BreakpointID),
				zap.String("breakpoint_stage", hit.Stage),
			)
		}
		logger.Info("run paused", fields...)
		return run, currentState, nil
	}
	return run, currentState, runRevisionRetriesExceeded("pause run at checkpoint")
}

func (r *GraphRunner) loadPauseRun(ctx context.Context, expectedRun RunRecord) (RunRecord, error) {
	persistedRun, err := r.executionStore.GetRun(ctx, expectedRun.RunID)
	if err != nil {
		return RunRecord{}, err
	}
	if err := validateRunExecutionOwner(ctx, persistedRun); err != nil {
		return persistedRun, err
	}
	if err := validateNodeExecutionRun(expectedRun, persistedRun); err != nil {
		return persistedRun, err
	}
	persistedRun.PauseRequested = persistedRun.PauseRequested || expectedRun.PauseRequested
	persistedRun.CancelRequested = persistedRun.CancelRequested || expectedRun.CancelRequested
	if persistedRun.CancelRequested {
		persistedRun.PauseRequested = false
	}
	return persistedRun, nil
}

func (r *GraphRunner) buildPauseCancellationTransition(run RunRecord, step StepRecord, stage CheckpointStage) (runnerStepTransition, error) {
	if stage == CheckpointAfterNode || strings.TrimSpace(step.StepID) == "" {
		return runnerStepTransition{}, nil
	}
	return r.prepareCanceledStepTransition(run, []StepRecord{step})
}

func (r *GraphRunner) prepareCanceledStepTransition(run RunRecord, steps []StepRecord) (runnerStepTransition, error) {
	transition := runnerStepTransition{
		writes: make([]StepWrite, 0, len(steps)),
		events: make([]Event, 0, len(steps)),
		steps:  make([]StepRecord, 0, len(steps)),
	}
	for _, step := range steps {
		if step.Status == StepStatusSucceeded || step.Status == StepStatusCanceled {
			continue
		}
		now := r.currentTime()
		step.Status = StepStatusCanceled
		step.ErrorCode = "run_canceled"
		step.ErrorMessage = "run canceled"
		step.FinishedAt = &now
		step.UpdatedAt = now
		canceledEvent, err := r.buildEvent(run, step.StepID, step.TaskID, step.NodeID, EventNodeCanceled, map[string]any{
			"attempt":    step.Attempt,
			"error_code": "run_canceled",
			"message":    "run canceled",
		})
		if err != nil {
			return runnerStepTransition{}, err
		}
		transition.writes = append(transition.writes, StepWrite{Mode: StepWriteUpdate, Step: step})
		transition.events = append(transition.events, canceledEvent)
		transition.steps = append(transition.steps, step)
	}
	return transition, nil
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
		return failedRun, currentState, err
	}
	if failedRun.Status == RunStatusFailed {
		return failedRun, currentState, errors.New(message)
	}
	return failedRun, currentState, nil
}

func (r *GraphRunner) persistRunFailureWithTransition(ctx context.Context, run RunRecord, currentState *state.State, code string, message string, transition runnerStepTransition) (RunRecord, error) {
	persistenceCtx := context.WithoutCancel(normalizeRunnerContext(ctx))
	updatedRun, err := r.commitRunUpdateWithRetry(persistenceCtx, run, "fail run", func(latestRun RunRecord) (runUpdatePreparation, error) {
		switch {
		case latestRun.CancelRequested:
			canceledTransition, buildErr := r.prepareCanceledStepTransition(latestRun, transition.steps)
			if buildErr != nil {
				return runUpdatePreparation{}, buildErr
			}
			return r.prepareCanceledRunUpdate(latestRun, canceledTransition)
		case latestRun.PauseRequested:
			return r.preparePausedRunUpdate(persistenceCtx, latestRun, transition, "pause requested before run failure")
		case !isActiveDeleteRunStatus(latestRun.Status):
			return runUpdatePreparation{run: latestRun}, nil
		}

		now := r.currentTime()
		latestRun.Status = RunStatusFailed
		latestRun.PauseRequested = false
		latestRun.CancelRequested = false
		latestRun.ErrorCode = code
		latestRun.ErrorMessage = message
		latestRun.UpdatedAt = now
		latestRun.FinishedAt = &now
		failedEvent, buildErr := r.buildEvent(latestRun, "", "", latestRun.CurrentNodeID, EventRunFailed, map[string]any{
			"error_code":    code,
			"error_message": message,
		})
		if buildErr != nil {
			return runUpdatePreparation{}, buildErr
		}
		events := append([]Event(nil), transition.events...)
		events = append(events, failedEvent)
		commit := Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: latestRun},
			Steps:  transition.writes,
			Events: events,
		}
		return runUpdatePreparation{run: latestRun, commit: &commit, taskFailures: transition.taskFailures}, nil
	})
	if err != nil {
		return updatedRun, err
	}
	switch updatedRun.Status {
	case RunStatusFailed:
		for _, step := range transition.steps {
			logger.Error("nodes failed", append(stepLogFields(ctx, step), zap.String("error", redactSensitiveString(ctx, message)))...)
		}
		logger.Error("run failed", append(runLogFields(ctx, updatedRun), state.SummaryFields(currentState)...)...)
	case RunStatusCanceled:
		logger.Info("run canceled before failure", append(runLogFields(ctx, updatedRun), state.SummaryFields(currentState)...)...)
	case RunStatusPaused:
		logger.Info("run paused before failure", append(runLogFields(ctx, updatedRun), state.SummaryFields(currentState)...)...)
	}
	if isTerminalRunStatus(updatedRun.Status) {
		if err := r.applyRunRetention(persistenceCtx, updatedRun.RunID); err != nil {
			return updatedRun, err
		}
	}
	return updatedRun, nil
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
	scopedRuns := make([]RunRecord, 0, len(runs))
	for _, run := range runs {
		if !r.runBelongsToRunner(run) {
			continue
		}
		byID[run.RunID] = run
		scopedRuns = append(scopedRuns, run)
	}
	for runID, reason := range retentionCandidates(scopedRuns, r.retentionPolicy, r.currentTime()) {
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
	_, updateErr := r.commitRunUpdateWithRetry(failureCtx, run, "abort started run", func(latestRun RunRecord) (runUpdatePreparation, error) {
		if latestRun.CancelRequested {
			return r.prepareCanceledRunUpdate(latestRun, runnerStepTransition{})
		}
		if isTerminalRunStatus(latestRun.Status) {
			return runUpdatePreparation{run: latestRun}, nil
		}
		now := r.currentTime()
		latestRun.Status = RunStatusFailed
		latestRun.PauseRequested = false
		latestRun.CancelRequested = false
		latestRun.ErrorCode = code
		latestRun.ErrorMessage = cause.Error()
		latestRun.UpdatedAt = now
		latestRun.FinishedAt = &now
		failedEvent, buildErr := r.buildEvent(latestRun, "", "", latestRun.CurrentNodeID, EventRunFailed, map[string]any{
			"error_code":    code,
			"error_message": cause.Error(),
		})
		if buildErr != nil {
			return runUpdatePreparation{}, buildErr
		}
		commit := Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: latestRun},
			Events: []Event{failedEvent},
		}
		return runUpdatePreparation{run: latestRun, commit: &commit}, nil
	})
	return errors.Join(cause, updateErr)
}

func (r *GraphRunner) resumeTarget(ctx context.Context, checkpoint CheckpointRecord, runtimeState state.RuntimeState, currentState *state.State) ([]GraphTask, *breakpointSkip, *core.SuspendRequest, error) {
	schedule, _, err := LoadGraphSchedule(currentState)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load graph schedule: %w", err)
	}
	switch checkpoint.Stage {
	case CheckpointBeforeNode:
		nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(schedule.CurrentTasks) != 1 || schedule.CurrentTasks[0].NodeID != nodeID {
			return nil, nil, nil, fmt.Errorf("before-node checkpoint %q has invalid current task schedule", checkpoint.CheckpointID)
		}
		return CloneGraphTasks(schedule.CurrentTasks), &breakpointSkip{NodeID: checkpoint.NodeID, Stage: string(CheckpointBeforeNode)}, nil, nil
	case CheckpointAfterNode:
		nodeID, err := r.runnerGraph().ResolveNodeID(checkpoint.NodeID)
		if err != nil {
			return nil, nil, nil, err
		}
		if isParallelWaveTaskCheckpoint(runtimeState, schedule) {
			return nil, nil, nil, parallelAfterNodeCheckpointError(checkpoint.CheckpointID)
		}
		parent := GraphTask{TaskID: checkpoint.TaskID, NodeID: nodeID}
		if parent.TaskID == "" {
			parent.TaskID = nodeID
		}
		if nextTasks, suspend, handled, commandErr := r.resolveAfterNodeCommand(ctx, checkpoint, parent, currentState); handled || commandErr != nil {
			return nextTasks, nil, suspend, commandErr
		}
		nextTasks, err := r.runnerGraph().ResolveNextTasks(ctx, parent, currentState)
		if err != nil {
			return nil, nil, nil, err
		}
		return CloneGraphTasks(nextTasks), nil, nil, nil
	case CheckpointAfterWave:
		return CloneGraphTasks(schedule.NextTasks), nil, nil, nil
	case CheckpointAgent:
		taskID := checkpoint.TaskID
		for _, task := range schedule.CurrentTasks {
			if task.TaskID == taskID {
				return []GraphTask{task}, nil, nil, nil
			}
		}
		if len(schedule.CurrentTasks) == 1 {
			return []GraphTask{schedule.CurrentTasks[0]}, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("agent checkpoint %q has no current task", checkpoint.CheckpointID)
	case CheckpointFinal:
		return nil, nil, nil, fmt.Errorf("final checkpoint %q is not resumable", checkpoint.CheckpointID)
	default:
		return nil, nil, nil, fmt.Errorf("unsupported checkpoint stage %q", checkpoint.Stage)
	}
}

func (r *GraphRunner) validateIndependentCheckpoint(checkpoint RestoredCheckpoint) error {
	switch checkpoint.Record.Stage {
	case CheckpointAfterNode:
		schedule, _, err := LoadGraphSchedule(checkpoint.Business)
		if err != nil {
			return fmt.Errorf("load graph schedule: %w", err)
		}
		if isParallelWaveTaskCheckpoint(checkpoint.Runtime, schedule) {
			return parallelAfterNodeCheckpointError(checkpoint.Record.CheckpointID)
		}
	}
	return nil
}

func isParallelWaveTaskCheckpoint(runtimeState state.RuntimeState, schedule GraphSchedule) bool {
	if runtimeState.ParallelWaveID != "" || runtimeState.WaveID != "" {
		return true
	}
	if len(schedule.CurrentTasks) > 1 {
		return true
	}
	for _, task := range schedule.CurrentTasks {
		if task.ParallelWaveSize > 1 {
			return true
		}
	}
	return false
}

func parallelAfterNodeCheckpointError(checkpointID string) error {
	return fmt.Errorf("parallel wave after_node checkpoint %q is not independently resumable; resume from an after_wave checkpoint", checkpointID)
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
	event = sanitizeEventPayload(ctx, event)
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

func (r *GraphRunner) commitRuntime(ctx context.Context, commit Commit) (CommitResult, error) {
	if r == nil || r.transactionStore == nil {
		return CommitResult{}, errors.New("runtime transaction store is nil")
	}
	commit = sanitizeCommit(ctx, commit)
	guardedCommit, err := r.guardExecutionCommit(ctx, commit)
	if err != nil {
		return CommitResult{}, err
	}
	var result CommitResult
	committed := guardedCommit
	if failures := taskFailuresFromContext(ctx); len(failures) > 0 {
		queue, atomic := r.taskQueue.(AtomicTaskQueue)
		if !atomic {
			return CommitResult{}, errors.New("node task failure requires an atomic task queue")
		}
		_, result, err = queue.FailWithCommit(ctx, failures, guardedCommit)
	} else if lease, ok := taskCompletionFromContext(ctx); ok {
		queue, atomic := r.taskQueue.(AtomicTaskQueue)
		if !atomic {
			return CommitResult{}, errors.New("node task completion requires an atomic task queue")
		}
		_, result, err = queue.CompleteWithCommit(ctx, lease, TaskResult{}, guardedCommit)
	} else {
		result, committed, err = commitAndResolve(ctx, r.transactionStore, guardedCommit)
	}
	if err != nil {
		return result, err
	}
	if len(committed.Artifacts) > 0 {
		finalizeCtx := context.WithoutCancel(normalizeRunnerContext(ctx))
		if finalizeErr := r.artifactStore.Finalize(finalizeCtx, committed.TransactionID, committed.Artifacts); finalizeErr != nil {
			logger.Error("artifact finalize pending reconciliation",
				zap.String("transaction_id", committed.TransactionID),
				zap.Int("artifact_count", len(committed.Artifacts)),
				zap.Error(finalizeErr),
			)
		} else {
			r.observeFinalizedArtifacts(finalizeCtx, committed.Artifacts)
		}
	}
	observeCommittedEvents(ctx, r.eventSink, r.transactionStore, committed.Events)
	return result, nil
}

func (r *GraphRunner) guardExecutionCommit(ctx context.Context, commit Commit) (Commit, error) {
	if commit.Run != nil && commit.Run.Mode == RunWriteCreate {
		return commit, nil
	}
	runID, err := singleCommitRunID(commit)
	if err != nil {
		return Commit{}, err
	}
	guard, guarded := executionLeaseGuardFromContext(ctx)
	if guarded && guard.RunID != runID {
		return Commit{}, fmt.Errorf("%w: execution lease guard for run %q cannot commit run %q", ErrExecutionLeaseLost, guard.RunID, runID)
	}
	if !guarded {
		return commit, nil
	}
	commit.Lease = &guard
	return commit, nil
}

func singleCommitRunID(commit Commit) (string, error) {
	runID := ""
	addRunID := func(candidate string) error {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return nil
		}
		if runID == "" {
			runID = candidate
			return nil
		}
		if runID != candidate {
			return fmt.Errorf("runtime commit spans runs %q and %q", runID, candidate)
		}
		return nil
	}
	if commit.Run != nil {
		if err := addRunID(commit.Run.Run.RunID); err != nil {
			return "", err
		}
	}
	for _, stepWrite := range commit.Steps {
		if err := addRunID(stepWrite.Step.RunID); err != nil {
			return "", err
		}
	}
	for _, checkpointWrite := range commit.Checkpoints {
		if err := addRunID(checkpointWrite.Record.RunID); err != nil {
			return "", err
		}
	}
	for _, event := range commit.Events {
		if err := addRunID(event.RunID); err != nil {
			return "", err
		}
	}
	for _, stage := range commit.Artifacts {
		if err := addRunID(stage.Ref.RunID); err != nil {
			return "", err
		}
	}
	if runID == "" {
		return "", errors.New("runtime commit requires a run ID")
	}
	return runID, nil
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
		fields := append(runLogFields(ctx, run),
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

func normalizeExternalState(input *state.State) (*state.State, error) {
	if input == nil {
		return nil, nil
	}
	isolated, err := input.CloneStrict()
	if err != nil {
		return nil, fmt.Errorf("state cannot be safely cloned: %w", err)
	}
	exported := isolated.Export()
	business := make(map[string]any, 2)
	for section, value := range exported {
		switch section {
		case state.SectionShared, state.SectionScopes:
			if _, ok := value.(map[string]any); !ok {
				return nil, fmt.Errorf("state section %q must be an object", section)
			}
			business[section] = value
		case state.SectionInternal, state.SectionRuntime:
			mapped, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("state section %q is reserved", section)
			}
			if len(mapped) > 0 {
				return nil, fmt.Errorf("state section %q is reserved", section)
			}
		default:
			return nil, fmt.Errorf("state section %q is unknown", section)
		}
	}
	return state.FromMap(business), nil
}

func projectBusinessState(input *state.State) *state.State {
	if input == nil {
		return state.NewState()
	}
	exported := input.Export()
	business := make(map[string]any, 2)
	for _, section := range []string{state.SectionShared, state.SectionScopes} {
		if values, ok := exported[section].(map[string]any); ok {
			business[section] = values
		}
	}
	return state.FromMap(business)
}

func (r *GraphRunner) recordArtifact(ctx context.Context, transactionID string, artifact Artifact) (ArtifactStage, error) {
	stage := ArtifactStage{TransactionID: transactionID}
	if r == nil || r.artifactStore == nil {
		return stage, ErrArtifactRecorderUnavailable
	}
	if err := validateRunnerStorageID("transaction ID", transactionID); err != nil {
		return stage, err
	}

	metadata, _ := RunnerMetadataFromContext(ctx)
	if strings.TrimSpace(metadata.RunID) != "" {
		boundArtifact, err := bindArtifactRunnerMetadata(artifact, metadata)
		if err != nil {
			return stage, err
		}
		artifact = boundArtifact
	}
	if operation, ok := core.EffectOperationFromContext(ctx); ok && strings.TrimSpace(operation.Key) != "" {
		provided := strings.TrimSpace(artifact.OperationKey)
		if provided != "" && provided != operation.Key {
			return stage, fmt.Errorf("artifact operation key %q does not match execution operation %q", provided, operation.Key)
		}
		artifact.OperationKey = operation.Key
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = r.currentTime()
	}
	if artifact.ID == "" {
		artifact.ID = newRunnerID()
	}
	if artifact.RunID != "" {
		run, err := r.executionStore.GetRun(ctx, artifact.RunID)
		if err != nil {
			return stage, err
		}
		if err := validateRunExecutionOwner(ctx, run); err != nil {
			return stage, err
		}
	}

	stage, err := r.artifactStore.Stage(ctx, transactionID, artifact)
	if err != nil {
		return stage, err
	}
	fields := append(artifactLogFields(stage.Ref), zap.String("transaction_id", transactionID), zap.Int("bytes", len(artifact.Data)))
	logger.Debug("artifact staged", fields...)
	return stage, nil
}

func (r *GraphRunner) observeFinalizedArtifacts(ctx context.Context, stages []ArtifactStage) {
	for _, stage := range stages {
		ref := stage.Ref
		if ref.RunID == "" || ref.ID == "" {
			continue
		}
		r.publishBestEffortEvent(ctx, RunRecord{
			RunID: ref.RunID, ParentRunID: ref.ParentRunID,
			ParentStepID: ref.ParentStepID, ParentTaskID: ref.ParentTaskID, RootRunID: ref.RootRunID,
			RunPath: append([]string(nil), ref.RunPath...), Namespace: ref.Namespace,
		}, ref.StepID, ref.NodeID, EventArtifactCreated, map[string]any{
			"artifact_id":    ref.ID,
			"transaction_id": stage.TransactionID,
			"type":           ref.Type,
			"mime_type":      ref.MIMEType,
			"location":       ref.Location,
		})
	}
}

func bindArtifactRunnerMetadata(artifact Artifact, metadata RunnerMetadata) (Artifact, error) {
	identities := []struct {
		name     string
		provided string
		expected string
	}{
		{name: "run ID", provided: artifact.RunID, expected: metadata.RunID},
		{name: "step ID", provided: artifact.StepID, expected: metadata.StepID},
		{name: "node ID", provided: artifact.NodeID, expected: metadata.NodeID},
		{name: "parent run ID", provided: artifact.ParentRunID, expected: metadata.ParentRunID},
		{name: "parent step ID", provided: artifact.ParentStepID, expected: metadata.ParentStepID},
		{name: "parent task ID", provided: artifact.ParentTaskID, expected: metadata.ParentTaskID},
		{name: "root run ID", provided: artifact.RootRunID, expected: metadata.RootRunID},
	}
	for _, identity := range identities {
		provided := strings.TrimSpace(identity.provided)
		expected := strings.TrimSpace(identity.expected)
		if provided != "" && provided != expected {
			return Artifact{}, fmt.Errorf("artifact %s %q does not match runner metadata %q", identity.name, provided, expected)
		}
	}
	providedNamespace := strings.Trim(strings.TrimSpace(artifact.Namespace), "/")
	expectedNamespace := strings.Trim(strings.TrimSpace(metadata.Namespace), "/")
	if providedNamespace != "" && providedNamespace != expectedNamespace {
		return Artifact{}, fmt.Errorf("artifact namespace %q does not match runner metadata %q", providedNamespace, expectedNamespace)
	}
	if len(artifact.RunPath) > 0 && !slices.Equal(artifact.RunPath, metadata.RunPath) {
		return Artifact{}, fmt.Errorf("artifact run path does not match runner metadata")
	}

	artifact.RunID = strings.TrimSpace(metadata.RunID)
	artifact.StepID = strings.TrimSpace(metadata.StepID)
	artifact.NodeID = strings.TrimSpace(metadata.NodeID)
	artifact.ParentRunID = strings.TrimSpace(metadata.ParentRunID)
	artifact.ParentStepID = strings.TrimSpace(metadata.ParentStepID)
	artifact.ParentTaskID = strings.TrimSpace(metadata.ParentTaskID)
	artifact.RootRunID = strings.TrimSpace(metadata.RootRunID)
	artifact.RunPath = append([]string(nil), metadata.RunPath...)
	artifact.Namespace = expectedNamespace
	return artifact, nil
}

func (r *GraphRunner) validateRestoredCheckpoint(checkpoint RestoredCheckpoint) error {
	if r == nil || r.codec == nil {
		return errors.New("state codec is required")
	}
	return ValidateRestoredCheckpoint(checkpoint, r.codec)
}

func ValidateRestoredCheckpoint(checkpoint RestoredCheckpoint, codec state.Codec) error {
	if codec == nil {
		return errors.New("state codec is required")
	}
	record := checkpoint.Record
	codecName := strings.TrimSpace(record.StateCodec)
	if codecName == "" {
		return fmt.Errorf("checkpoint %q state codec is required", record.CheckpointID)
	}
	if codecName != codec.Name() {
		return fmt.Errorf("checkpoint %q uses state codec %q, runner configured for %q", record.CheckpointID, codecName, codec.Name())
	}
	version := strings.TrimSpace(record.StateVersion)
	if version == "" {
		return fmt.Errorf("checkpoint %q state version is required", record.CheckpointID)
	}
	if version != codec.Version() {
		return fmt.Errorf("checkpoint %q uses state version %q, runner configured for %q", record.CheckpointID, version, codec.Version())
	}
	if checkpoint.Snapshot.Version == "" {
		return fmt.Errorf("checkpoint %q snapshot version is required", record.CheckpointID)
	}
	if version != checkpoint.Snapshot.Version {
		return fmt.Errorf("checkpoint %q state version mismatch: record=%q snapshot=%q", record.CheckpointID, version, checkpoint.Snapshot.Version)
	}
	if err := validateRunnerStorageID("checkpoint record run ID", record.RunID); err != nil {
		return fmt.Errorf("checkpoint %q has invalid record identity: %w", record.CheckpointID, err)
	}
	if err := validateRunnerStorageID("checkpoint snapshot run ID", checkpoint.Runtime.RunID); err != nil {
		return fmt.Errorf("checkpoint %q has invalid snapshot identity: %w", record.CheckpointID, err)
	}
	if record.RunID != checkpoint.Runtime.RunID {
		return fmt.Errorf("checkpoint %q run mismatch: record=%q snapshot=%q", record.CheckpointID, record.RunID, checkpoint.Runtime.RunID)
	}
	if record.StepID != checkpoint.Runtime.CurrentStepID {
		return fmt.Errorf("checkpoint %q step mismatch: record=%q snapshot=%q", record.CheckpointID, record.StepID, checkpoint.Runtime.CurrentStepID)
	}
	if record.TaskID != checkpoint.Runtime.CurrentTaskID {
		return fmt.Errorf("checkpoint %q task mismatch: record=%q snapshot=%q", record.CheckpointID, record.TaskID, checkpoint.Runtime.CurrentTaskID)
	}
	if record.NodeID != checkpoint.Runtime.CurrentNodeID {
		return fmt.Errorf("checkpoint %q nodes mismatch: record=%q snapshot=%q", record.CheckpointID, record.NodeID, checkpoint.Runtime.CurrentNodeID)
	}
	return nil
}

func validateCheckpointRun(run RunRecord, checkpoint RestoredCheckpoint) error {
	recordRunID := strings.TrimSpace(checkpoint.Record.RunID)
	if recordRunID == "" {
		return fmt.Errorf("checkpoint %q has no run id", checkpoint.Record.CheckpointID)
	}
	if recordRunID != strings.TrimSpace(run.RunID) {
		return fmt.Errorf("checkpoint %q belongs to run %q, not run %q", checkpoint.Record.CheckpointID, recordRunID, run.RunID)
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
	if expectedID := strings.TrimSpace(r.graphID); expectedID != "" && strings.TrimSpace(run.GraphID) != expectedID {
		return fmt.Errorf("resume run %q: graph id mismatch: run uses %q, runner uses %q", run.RunID, run.GraphID, expectedID)
	}
	if expectedVersion := strings.TrimSpace(r.graphVersion); expectedVersion != "" && strings.TrimSpace(run.GraphVersion) != expectedVersion {
		return fmt.Errorf("resume run %q: graph version mismatch: run uses %q, runner uses %q", run.RunID, run.GraphVersion, expectedVersion)
	}
	expectedSessionID := r.resolvedGraphSessionID()
	actualSessionID := strings.TrimSpace(run.GraphSessionID)
	if expectedSessionID != "" && actualSessionID != expectedSessionID {
		return fmt.Errorf("resume run %q: graph session mismatch: run uses %q, runner uses %q", run.RunID, actualSessionID, expectedSessionID)
	}
	expected := r.resolvedGraphHash()
	if expected != "" {
		actual := strings.TrimSpace(run.GraphHash)
		if actual != expected {
			return fmt.Errorf("resume run %q: graph hash mismatch: run uses %q, runner uses %q", run.RunID, actual, expected)
		}
	}
	expectedSnapshot := r.resolvedGraphSnapshotHash()
	if expectedSnapshot != "" {
		actualSnapshot := strings.TrimSpace(run.GraphSnapshotHash)
		if actualSnapshot != expectedSnapshot {
			return fmt.Errorf("resume run %q: graph snapshot hash mismatch: run uses %q, runner uses %q", run.RunID, actualSnapshot, expectedSnapshot)
		}
	}
	return nil
}

func (r *GraphRunner) runBelongsToRunner(run RunRecord) bool {
	if r == nil {
		return false
	}
	if expectedID := strings.TrimSpace(r.graphID); expectedID != "" && strings.TrimSpace(run.GraphID) != expectedID {
		return false
	}
	if expectedVersion := strings.TrimSpace(r.graphVersion); expectedVersion != "" && strings.TrimSpace(run.GraphVersion) != expectedVersion {
		return false
	}
	if expectedHash := r.resolvedGraphHash(); expectedHash != "" && strings.TrimSpace(run.GraphHash) != expectedHash {
		return false
	}
	if expectedSnapshot := r.resolvedGraphSnapshotHash(); expectedSnapshot != "" && strings.TrimSpace(run.GraphSnapshotHash) != expectedSnapshot {
		return false
	}
	if expectedSession := r.resolvedGraphSessionID(); expectedSession != "" && strings.TrimSpace(run.GraphSessionID) != expectedSession {
		return false
	}
	return true
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

func stableRuntimeID(kind string, parts ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.TrimSpace(kind)))
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.TrimSpace(part)))
	}
	return strings.TrimSpace(kind) + "-" + fmt.Sprintf("%x", hash.Sum(nil)[:16])
}

type breakpointSkip struct {
	NodeID   string
	Stage    string
	Consumed bool
}
