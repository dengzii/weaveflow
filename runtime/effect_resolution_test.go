package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestEffectResolutionTransitionIsForwardOnly(t *testing.T) {
	store := NewMemoryExecutionStore()
	run := RunRecord{RunID: "run-effect-resolution", Status: RunStatusFailed}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	step := StepRecord{
		StepID: "step-effect-resolution", RunID: run.RunID, TaskID: "task", NodeID: "writer",
		OperationKey: "operation-effect-resolution", EffectClass: core.EffectNonIdempotentWrite,
		EffectStatus: core.EffectUnknown, Status: StepStatusFailed,
	}
	if err := store.AppendStep(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	requestedAt := time.Now().UTC()
	step.EffectResolution = &EffectResolution{
		ID: "resolution-1", AttemptID: "attempt-1", Action: EffectResolutionConfirmNotApplied,
		Status: EffectResolutionIntent, Actor: "operator-1", Reason: "provider rejected the write", RequestedAt: requestedAt,
	}
	if err := store.UpdateStep(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	resolvedAt := requestedAt.Add(time.Second)
	step.EffectResolution.Status = EffectResolutionSucceeded
	step.EffectResolution.ResolvedAt = &resolvedAt
	step.EffectStatus = core.EffectNotApplied
	if err := store.UpdateStep(context.Background(), step); err != nil {
		t.Fatal(err)
	}

	changed := cloneStepRecord(step)
	changed.EffectResolution.Reason = "changed reason"
	if err := store.UpdateStep(context.Background(), changed); err == nil {
		t.Fatal("UpdateStep() changed a terminal effect resolution identity")
	}
	rolledBack := cloneStepRecord(step)
	rolledBack.EffectResolution.Status = EffectResolutionIntent
	rolledBack.EffectResolution.ResolvedAt = nil
	rolledBack.EffectStatus = core.EffectUnknown
	if err := store.UpdateStep(context.Background(), rolledBack); err == nil {
		t.Fatal("UpdateStep() rolled a terminal effect resolution backward")
	}
}

func TestNormalizeEffectResolutionRequest(t *testing.T) {
	request, err := normalizeEffectResolutionRequest(" run-1 ", EffectResolutionRequest{
		ResolutionID: " resolution-1 ", StepID: " step-1 ", Action: EffectResolutionConfirmNotApplied,
		Actor: " operator ", Reason: " provider rejected the write ",
	})
	if err != nil {
		t.Fatalf("normalizeEffectResolutionRequest() error = %v", err)
	}
	if request.ResolutionID != "resolution-1" || request.StepID != "step-1" || request.Actor != "operator" || request.Reason != "provider rejected the write" {
		t.Fatalf("normalized request = %#v", request)
	}

	tests := []struct {
		name     string
		runID    string
		request  EffectResolutionRequest
		contains string
	}{
		{name: "run", runID: " ", request: request, contains: "run ID"},
		{name: "resolution", runID: "run-1", request: EffectResolutionRequest{StepID: "step-1", Action: EffectResolutionConfirmNotApplied, Actor: "operator", Reason: "reason"}, contains: "resolution ID"},
		{name: "step", runID: "run-1", request: EffectResolutionRequest{ResolutionID: "resolution-1", Action: EffectResolutionConfirmNotApplied, Actor: "operator", Reason: "reason"}, contains: "step ID"},
		{name: "actor", runID: "run-1", request: EffectResolutionRequest{ResolutionID: "resolution-1", StepID: "step-1", Action: EffectResolutionConfirmNotApplied, Reason: "reason"}, contains: "actor is required"},
		{name: "reason", runID: "run-1", request: EffectResolutionRequest{ResolutionID: "resolution-1", StepID: "step-1", Action: EffectResolutionConfirmNotApplied, Actor: "operator"}, contains: "reason is required"},
		{name: "action", runID: "run-1", request: EffectResolutionRequest{ResolutionID: "resolution-1", StepID: "step-1", Action: "retry", Actor: "operator", Reason: "reason"}, contains: "unsupported"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := normalizeEffectResolutionRequest(testCase.runID, testCase.request)
			if err == nil || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("normalizeEffectResolutionRequest() error = %v, want %q", err, testCase.contains)
			}
		})
	}
}

func TestEffectResolutionClaimsAndResolvedRunGuard(t *testing.T) {
	runner := &GraphRunner{}
	if err := runner.claimEffectResolution("run", "step", "resolution-1"); err != nil {
		t.Fatalf("claimEffectResolution() error = %v", err)
	}
	if err := runner.claimEffectResolution("run", "step", "resolution-2"); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("duplicate claim error = %v", err)
	}
	runner.releaseEffectResolution("run", "step", "resolution-2")
	if err := runner.claimEffectResolution("run", "step", "resolution-2"); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("mismatched release cleared claim: %v", err)
	}
	runner.releaseEffectResolution("run", "step", "resolution-1")
	if err := runner.claimEffectResolution("run", "step", "resolution-2"); err != nil {
		t.Fatalf("claim after release error = %v", err)
	}

	store := NewMemoryRuntimeStore()
	runner.executionStore = store
	if err := runner.ensureRunEffectsResolved(context.Background(), RunRecord{RunID: "running", Status: RunStatusRunning}); err != nil {
		t.Fatalf("ensureRunEffectsResolved(running) error = %v", err)
	}
	failed := RunRecord{RunID: "failed", Status: RunStatusFailed}
	if err := store.CreateRun(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendStep(context.Background(), StepRecord{
		StepID: "failed-step", RunID: failed.RunID, OperationKey: "operation", EffectClass: core.EffectNonIdempotentWrite,
		EffectStatus: core.EffectUnknown, Status: StepStatusFailed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runner.ensureRunEffectsResolved(context.Background(), failed); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("ensureRunEffectsResolved(failed) error = %v", err)
	}
	if err := runner.ensureRunEffectsResolved(context.Background(), RunRecord{RunID: failed.RunID, Status: RunStatusCompleted}); err != nil {
		t.Fatalf("ensureRunEffectsResolved(completed) error = %v", err)
	}
}

func TestEffectOperationsForStepUsesLatestSortedChildOperations(t *testing.T) {
	store := NewMemoryRuntimeStore()
	runner := &GraphRunner{eventSink: store}
	step := StepRecord{StepID: "step", OperationKey: "parent-operation"}
	publishOperation := func(id, stepID string, eventType EventType, operation core.EffectOperation) {
		t.Helper()
		payload, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Publish(context.Background(), Event{ID: id, RunID: "run", StepID: stepID, Type: eventType, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	publishOperation("event-b-intent", step.StepID, EventEffectIntent, core.EffectOperation{Key: "child-b", Status: core.EffectIntent})
	publishOperation("event-parent", step.StepID, EventEffectOutcome, core.EffectOperation{Key: step.OperationKey, Status: core.EffectSucceeded})
	publishOperation("event-other", "other-step", EventEffectOutcome, core.EffectOperation{Key: "child-c", Status: core.EffectSucceeded})
	publishOperation("event-a", step.StepID, EventEffectOutcome, core.EffectOperation{Key: "child-a", Status: core.EffectFailed})
	publishOperation("event-b-outcome", step.StepID, EventEffectOutcome, core.EffectOperation{Key: "child-b", Status: core.EffectSucceeded})

	operations, err := runner.effectOperationsForStep("run", step)
	if err != nil {
		t.Fatalf("effectOperationsForStep() error = %v", err)
	}
	if len(operations) != 2 || operations[0].Key != "child-a" || operations[1].Key != "child-b" || operations[1].Status != core.EffectSucceeded {
		t.Fatalf("effect operations = %#v", operations)
	}
	operation, found, err := runner.effectOperationByKey("run", step, "child-b")
	if err != nil || !found || operation.Status != core.EffectSucceeded {
		t.Fatalf("effectOperationByKey() = %#v, %v, %v", operation, found, err)
	}

	if err := store.Publish(context.Background(), Event{ID: "event-malformed", RunID: "run", StepID: "malformed-step", Type: EventEffectIntent, Payload: []byte(`{`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.effectOperationsForStep("run", StepRecord{StepID: "malformed-step"}); err == nil || !strings.Contains(err.Error(), "decode effect operation") {
		t.Fatalf("malformed effect event error = %v", err)
	}
}

func TestResolveEffectConfirmNotAppliedIsIdempotent(t *testing.T) {
	runner, store, run, step := newEffectResolutionFixture(t, &blockingChildRunGraph{})
	request := EffectResolutionRequest{
		ResolutionID: "resolution-confirm", StepID: step.StepID, Action: EffectResolutionConfirmNotApplied,
		Actor: "operator", Reason: "provider rejected the write",
	}
	result, err := runner.ResolveEffect(context.Background(), run.RunID, request, nil)
	if err != nil {
		t.Fatalf("ResolveEffect() error = %v", err)
	}
	if result.Run.Status != RunStatusPaused || result.Resolution.Status != EffectResolutionSucceeded || result.Resolution.ResolvedAt == nil || result.Continued {
		t.Fatalf("ResolveEffect() result = %#v", result)
	}
	persistedStep, err := store.GetStep(context.Background(), step.StepID)
	if err != nil || persistedStep.EffectStatus != core.EffectNotApplied || persistedStep.EffectResolution == nil || persistedStep.EffectResolution.ID != request.ResolutionID {
		t.Fatalf("persisted step = %#v, %v", persistedStep, err)
	}
	events, err := store.ListEvents(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(events, EventEffectResolutionRequested) || !containsEventType(events, EventEffectResolutionOutcome) || !containsEventType(events, EventRunPaused) {
		t.Fatalf("effect resolution events = %#v", events)
	}

	replayed, err := runner.ResolveEffect(context.Background(), run.RunID, request, nil)
	if err != nil || replayed.Resolution.ID != request.ResolutionID || replayed.Run.Status != RunStatusPaused {
		t.Fatalf("replayed ResolveEffect() = %#v, %v", replayed, err)
	}
	conflicting := request
	conflicting.Reason = "different reason"
	if _, err := runner.ResolveEffect(context.Background(), run.RunID, conflicting, nil); !errors.Is(err, ErrRunControlNotAllowed) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestResolveEffectCompensationPersistsSuccessAndUnknownOutcome(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		graph := &effectCompensationTestGraph{blockingChildRunGraph: &blockingChildRunGraph{}}
		runner, store, run, step := newEffectResolutionFixture(t, graph)
		publishEffectOperation(t, store, run.RunID, step.StepID, "child-operation", core.EffectSucceeded)
		request := EffectResolutionRequest{
			ResolutionID: "resolution-compensate", StepID: step.StepID, Action: EffectResolutionCompensate,
			Actor: "operator", Reason: "undo uncertain write",
		}
		result, err := runner.ResolveEffect(context.Background(), run.RunID, request, nil)
		if err != nil {
			t.Fatalf("ResolveEffect(compensate) error = %v", err)
		}
		if result.Run.Status != RunStatusPaused || graph.calls != 1 || len(graph.operations) != 1 || graph.operations[0].Key != "child-operation" {
			t.Fatalf("compensation result = %#v, calls=%d operations=%#v", result, graph.calls, graph.operations)
		}
		persisted, err := store.GetStep(context.Background(), step.StepID)
		if err != nil || persisted.EffectStatus != core.EffectCompensated || persisted.EffectResolution == nil || persisted.EffectResolution.CompensationKey == "" {
			t.Fatalf("persisted compensated step = %#v, %v", persisted, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		graph := &effectCompensationTestGraph{blockingChildRunGraph: &blockingChildRunGraph{}, err: errors.New("provider timeout")}
		runner, store, run, step := newEffectResolutionFixture(t, graph)
		request := EffectResolutionRequest{
			ResolutionID: "resolution-unknown", StepID: step.StepID, Action: EffectResolutionCompensate,
			Actor: "operator", Reason: "undo uncertain write",
		}
		result, err := runner.ResolveEffect(context.Background(), run.RunID, request, nil)
		if !errors.Is(err, ErrRunControlNotAllowed) || result.Resolution.Status != EffectResolutionUnknown || !strings.Contains(result.Resolution.Error, "provider timeout") {
			t.Fatalf("ResolveEffect(unknown) = %#v, %v", result, err)
		}
		persisted, loadErr := store.GetStep(context.Background(), step.StepID)
		if loadErr != nil || persisted.EffectResolution == nil || persisted.EffectResolution.Status != EffectResolutionUnknown || persisted.EffectStatus != core.EffectUnknown {
			t.Fatalf("persisted unknown step = %#v, %v", persisted, loadErr)
		}
	})
}

type effectCompensationTestGraph struct {
	*blockingChildRunGraph
	err        error
	calls      int
	operations []core.EffectOperation
}

func (graph *effectCompensationTestGraph) CompensateEffect(_ context.Context, _ string, request core.EffectCompensationRequest, _ *state.State) error {
	graph.calls++
	graph.operations = append([]core.EffectOperation(nil), request.Operations...)
	return graph.err
}

func newEffectResolutionFixture(t *testing.T, graph RunnerGraph) (*GraphRunner, *MemoryRuntimeStore, RunRecord, StepRecord) {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(900, 0).UTC()
	codec := state.NewJSONStateCodec("")
	store := NewMemoryRuntimeStore()
	run := RunRecord{
		RunID: "run-effect", RootRunID: "run-effect", RunPath: []string{"run-effect"}, Namespace: "run-effect",
		GraphID: "graph", GraphVersion: "v1", Status: RunStatusFailed, EntryNodeID: "writer", CurrentNodeID: "writer",
		ErrorCode: "side_effect_failed", ErrorMessage: "provider outcome unknown", StartedAt: now, UpdatedAt: now,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	step := StepRecord{
		StepID: "step-effect", RunID: run.RunID, TaskID: "task-effect", NodeID: "writer", OperationKey: "operation-effect",
		EffectClass: core.EffectCompensatable, EffectStatus: core.EffectUnknown, Status: StepStatusFailed,
		CheckpointBeforeID: "checkpoint-before-effect", StartedAt: now, UpdatedAt: now,
	}
	if err := store.AppendStep(ctx, step); err != nil {
		t.Fatal(err)
	}
	checkpointState := state.NewState()
	if err := StoreGraphSchedule(checkpointState, GraphSchedule{CurrentTasks: []GraphTask{{TaskID: step.TaskID, NodeID: step.NodeID}}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := state.SnapshotFromStateWithRuntime(checkpointState, state.RuntimeState{
		RunID: run.RunID, CurrentStepID: step.StepID, CurrentTaskID: step.TaskID, CurrentNodeID: step.NodeID,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := codec.Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointRecord{
		CheckpointID: step.CheckpointBeforeID, RunID: run.RunID, StepID: step.StepID, TaskID: step.TaskID,
		RootRunID: run.RootRunID, RunPath: append([]string(nil), run.RunPath...), Namespace: run.Namespace,
		NodeID: step.NodeID, Stage: CheckpointBeforeNode, StateCodec: codec.Name(), StateVersion: codec.Version(), CreatedAt: now,
	}
	if err := store.Save(ctx, checkpoint, payload); err != nil {
		t.Fatal(err)
	}
	runner := &GraphRunner{
		graph: graph, executionStore: store, checkpointStore: store, transactionStore: store, eventSink: store,
		artifactStore: NewNoopArtifactStore(), codec: codec, now: func() time.Time { return now.Add(time.Minute) },
	}
	return runner, store, run, step
}

func publishEffectOperation(t *testing.T, store EventSink, runID, stepID, key string, status core.EffectStatus) {
	t.Helper()
	payload, err := json.Marshal(core.EffectOperation{Key: key, Kind: "tool", Name: "provider", Class: core.EffectCompensatable, Status: status})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(context.Background(), Event{ID: "event-" + key, RunID: runID, StepID: stepID, Type: EventEffectOutcome, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func containsEventType(events []Event, eventType EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
