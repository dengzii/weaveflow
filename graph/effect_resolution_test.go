package graph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
	"github.com/dengzii/weaveflow/node"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type effectResolutionTestNode struct {
	node.Base
	execute    func(core.Context, *state.Access) (core.NodeResult, error)
	compensate func(core.Context, core.EffectCompensationRequest, *state.Access) error
}

func (target *effectResolutionTestNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return target.execute(ctx, access)
}

func (target *effectResolutionTestNode) CompensateEffect(ctx core.Context, request core.EffectCompensationRequest, access *state.Access) error {
	if target.compensate == nil {
		return errors.New("compensation is not configured")
	}
	return target.compensate(ctx, request, access)
}

func TestEffectCompensationResolvesUnknownAndContinuesSameRun(t *testing.T) {
	var attempts int
	var effects int
	var compensations int
	var compensationKey string
	target := &effectResolutionTestNode{
		Base: node.NewBase(node.Spec{ID: "writer", Name: "writer"}),
		execute: func(core.Context, *state.Access) (core.NodeResult, error) {
			attempts++
			effects++
			if attempts == 1 {
				return core.NodeResult{}, core.NewExecutionError(core.ErrorSideEffectFailed, "provider response lost", nil, nil)
			}
			return core.Success(), nil
		},
		compensate: func(ctx core.Context, request core.EffectCompensationRequest, _ *state.Access) error {
			compensations++
			key, ok := core.IdempotencyKeyFromContext(ctx)
			if !ok || !strings.HasPrefix(key, "compensation-") {
				return errors.New("compensation idempotency key is missing")
			}
			compensationKey = key
			if request.Operation.Status != core.EffectUnknown || request.Operation.Key == "" {
				return errors.New("unknown operation is missing")
			}
			effects--
			return nil
		},
	}
	target.Effect = core.EffectCompensatable
	runner, store := newEffectResolutionTestRunner(t, target)

	failed, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || failed.Status != fruntime.RunStatusFailed || attempts != 1 || effects != 1 {
		t.Fatalf("Start() run = %#v, attempts = %d, effects = %d, error = %v", failed, attempts, effects, err)
	}
	if _, _, err := runner.Resume(context.Background(), failed.RunID, nil); !errors.Is(err, fruntime.ErrRunControlNotAllowed) {
		t.Fatalf("Resume() error = %v, want unresolved effect rejection", err)
	}
	steps, err := runner.ListSteps(context.Background(), failed.RunID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}
	request := fruntime.EffectResolutionRequest{
		ResolutionID: "resolution-compensate-1",
		StepID:       steps[0].StepID,
		Action:       fruntime.EffectResolutionCompensate,
		Actor:        "operator-1",
		Reason:       "provider confirmed the original write may have completed",
		Continue:     true,
	}
	result, err := runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if err != nil || result.Run.Status != fruntime.RunStatusCompleted || !result.Continued {
		t.Fatalf("ResolveEffect() = %#v, %v", result, err)
	}
	if attempts != 2 || effects != 1 || compensations != 1 || compensationKey == "" {
		t.Fatalf("attempts = %d, effects = %d, compensations = %d, key = %q", attempts, effects, compensations, compensationKey)
	}
	persisted, err := runner.ExecutionStore().GetStep(context.Background(), steps[0].StepID)
	if err != nil || persisted.EffectStatus != core.EffectCompensated || persisted.EffectResolution == nil || persisted.EffectResolution.Status != fruntime.EffectResolutionSucceeded {
		t.Fatalf("resolved step = %#v, %v", persisted, err)
	}
	replayed, err := runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if err != nil || replayed.Run.Status != fruntime.RunStatusCompleted || compensations != 1 || attempts != 2 {
		t.Fatalf("replayed ResolveEffect() = %#v, compensations = %d, attempts = %d, error = %v", replayed, compensations, attempts, err)
	}
	events, err := store.ListEvents(failed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	assertEffectResolutionEvent(t, events, fruntime.EventEffectResolutionRequested)
	assertEffectResolutionEvent(t, events, fruntime.EventEffectResolutionOutcome)
}

func TestUnknownCompensationOutcomeIsNotReplayed(t *testing.T) {
	var attempts int
	var compensations int
	target := &effectResolutionTestNode{
		Base: node.NewBase(node.Spec{ID: "writer", Name: "writer"}),
		execute: func(core.Context, *state.Access) (core.NodeResult, error) {
			attempts++
			return core.NodeResult{}, core.NewExecutionError(core.ErrorSideEffectFailed, "provider response lost", nil, nil)
		},
		compensate: func(core.Context, core.EffectCompensationRequest, *state.Access) error {
			compensations++
			return errors.New("compensation response lost")
		},
	}
	target.Effect = core.EffectCompensatable
	runner, _ := newEffectResolutionTestRunner(t, target)
	failed, _, _ := runner.Start(context.Background(), state.NewState())
	steps, err := runner.ListSteps(context.Background(), failed.RunID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}
	request := fruntime.EffectResolutionRequest{
		ResolutionID: "resolution-unknown-1",
		StepID:       steps[0].StepID,
		Action:       fruntime.EffectResolutionCompensate,
		Actor:        "operator-1",
		Reason:       "compensate the uncertain provider write",
		Continue:     true,
	}
	result, err := runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if !errors.Is(err, fruntime.ErrRunControlNotAllowed) || result.Resolution.Status != fruntime.EffectResolutionUnknown || compensations != 1 {
		t.Fatalf("ResolveEffect() = %#v, compensations = %d, error = %v", result, compensations, err)
	}
	result, err = runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if !errors.Is(err, fruntime.ErrRunControlNotAllowed) || result.Resolution.Status != fruntime.EffectResolutionUnknown || compensations != 1 {
		t.Fatalf("replayed ResolveEffect() = %#v, compensations = %d, error = %v", result, compensations, err)
	}
	if _, _, err := runner.Resume(context.Background(), failed.RunID, nil); !errors.Is(err, fruntime.ErrRunControlNotAllowed) || attempts != 1 {
		t.Fatalf("Resume() error = %v, attempts = %d", err, attempts)
	}
}

func TestConcurrentEffectResolutionDoesNotDuplicateCompensation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var compensations int
	target := &effectResolutionTestNode{
		Base: node.NewBase(node.Spec{ID: "writer", Name: "writer"}),
		execute: func(core.Context, *state.Access) (core.NodeResult, error) {
			return core.NodeResult{}, core.NewExecutionError(core.ErrorSideEffectFailed, "provider response lost", nil, nil)
		},
		compensate: func(core.Context, core.EffectCompensationRequest, *state.Access) error {
			compensations++
			close(entered)
			<-release
			return nil
		},
	}
	target.Effect = core.EffectCompensatable
	runner, _ := newEffectResolutionTestRunner(t, target)
	failed, _, _ := runner.Start(context.Background(), state.NewState())
	steps, err := runner.ListSteps(context.Background(), failed.RunID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}
	request := fruntime.EffectResolutionRequest{
		ResolutionID: "resolution-concurrent-1",
		StepID:       steps[0].StepID,
		Action:       fruntime.EffectResolutionCompensate,
		Actor:        "operator-1",
		Reason:       "compensate the uncertain provider write",
	}
	type resolutionOutcome struct {
		result fruntime.EffectResolutionResult
		err    error
	}
	firstDone := make(chan resolutionOutcome, 1)
	go func() {
		result, resolveErr := runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
		firstDone <- resolutionOutcome{result: result, err: resolveErr}
	}()
	<-entered
	second, err := runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if !errors.Is(err, fruntime.ErrRunControlNotAllowed) || second.Resolution.Status != fruntime.EffectResolutionIntent {
		t.Fatalf("concurrent ResolveEffect() = %#v, %v", second, err)
	}
	close(release)
	first := <-firstDone
	if first.err != nil || first.result.Run.Status != fruntime.RunStatusPaused || compensations != 1 {
		t.Fatalf("first ResolveEffect() = %#v, compensations = %d, error = %v", first.result, compensations, first.err)
	}
}

func TestConfirmedNotAppliedEffectCanContinueWithoutCompensation(t *testing.T) {
	var attempts int
	target := &effectResolutionTestNode{
		Base: node.NewBase(node.Spec{ID: "writer", Name: "writer"}),
		execute: func(core.Context, *state.Access) (core.NodeResult, error) {
			attempts++
			if attempts == 1 {
				return core.NodeResult{}, core.NewExecutionError(core.ErrorSideEffectFailed, "request was rejected before apply", nil, nil)
			}
			return core.Success(), nil
		},
	}
	target.Effect = core.EffectNonIdempotentWrite
	runner, _ := newEffectResolutionTestRunner(t, target)
	failed, _, _ := runner.Start(context.Background(), state.NewState())
	steps, err := runner.ListSteps(context.Background(), failed.RunID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}
	result, err := runner.ResolveEffect(context.Background(), failed.RunID, fruntime.EffectResolutionRequest{
		ResolutionID: "resolution-confirm-1",
		StepID:       steps[0].StepID,
		Action:       fruntime.EffectResolutionConfirmNotApplied,
		Actor:        "operator-1",
		Reason:       "provider audit proves the write was rejected",
		Continue:     true,
	}, nil)
	if err != nil || result.Run.Status != fruntime.RunStatusCompleted || attempts != 2 {
		t.Fatalf("ResolveEffect() = %#v, attempts = %d, error = %v", result, attempts, err)
	}
	persisted, err := runner.ExecutionStore().GetStep(context.Background(), steps[0].StepID)
	if err != nil || persisted.EffectStatus != core.EffectNotApplied {
		t.Fatalf("resolved step = %#v, %v", persisted, err)
	}
}

func TestEffectResolutionSurvivesFileStoreReopenBeforeContinue(t *testing.T) {
	var attempts int
	target := &effectResolutionTestNode{
		Base: node.NewBase(node.Spec{ID: "writer", Name: "writer"}),
		execute: func(core.Context, *state.Access) (core.NodeResult, error) {
			attempts++
			if attempts == 1 {
				return core.NodeResult{}, core.NewExecutionError(core.ErrorSideEffectFailed, "request rejected", nil, nil)
			}
			return core.Success(), nil
		},
	}
	target.Effect = core.EffectNonIdempotentWrite
	workflow := newEffectResolutionTestGraph(t, target)
	directory := t.TempDir()
	store, err := filestore.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	runner := mustNewGraphRunner(t, workflow, store.ExecutionStore(), store.CheckpointStore(), state.NewJSONStateCodec(""), store.EventSink(),
		fruntime.WithRuntimeTransactionStore(store), fruntime.WithStoreCloser(store))
	failed, _, _ := runner.Start(context.Background(), state.NewState())
	steps, err := runner.ListSteps(context.Background(), failed.RunID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}
	request := fruntime.EffectResolutionRequest{
		ResolutionID: "resolution-reopen-1",
		StepID:       steps[0].StepID,
		Action:       fruntime.EffectResolutionConfirmNotApplied,
		Actor:        "operator-1",
		Reason:       "provider audit proves the write was rejected",
	}
	resolved, err := runner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if err != nil || resolved.Run.Status != fruntime.RunStatusPaused || resolved.Continued {
		t.Fatalf("ResolveEffect() = %#v, %v", resolved, err)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedStore, err := filestore.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	reopenedRunner := mustNewGraphRunner(t, workflow, reopenedStore.ExecutionStore(), reopenedStore.CheckpointStore(), state.NewJSONStateCodec(""), reopenedStore.EventSink(),
		fruntime.WithRuntimeTransactionStore(reopenedStore), fruntime.WithStoreCloser(reopenedStore))
	t.Cleanup(func() { _ = reopenedRunner.Close() })
	request.Continue = true
	continued, err := reopenedRunner.ResolveEffect(context.Background(), failed.RunID, request, nil)
	if err != nil || continued.Run.Status != fruntime.RunStatusCompleted || !continued.Continued || attempts != 2 {
		t.Fatalf("reopened ResolveEffect() = %#v, attempts = %d, error = %v", continued, attempts, err)
	}
}

func TestEffectResolutionRecoveryUsesDurableCompensationOutcome(t *testing.T) {
	var compensations int
	target := &effectResolutionTestNode{
		Base: node.NewBase(node.Spec{ID: "writer", Name: "writer"}),
		execute: func(core.Context, *state.Access) (core.NodeResult, error) {
			return core.NodeResult{}, core.NewExecutionError(core.ErrorSideEffectFailed, "provider response lost", nil, nil)
		},
		compensate: func(core.Context, core.EffectCompensationRequest, *state.Access) error {
			compensations++
			return nil
		},
	}
	target.Effect = core.EffectCompensatable
	workflow := newEffectResolutionTestGraph(t, target)
	directory := t.TempDir()
	store, err := filestore.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	runner := mustNewGraphRunner(t, workflow, store.ExecutionStore(), store.CheckpointStore(), state.NewJSONStateCodec(""), store.EventSink(),
		fruntime.WithRuntimeTransactionStore(store), fruntime.WithStoreCloser(store))
	failed, _, _ := runner.Start(context.Background(), state.NewState())
	steps, err := runner.ListSteps(context.Background(), failed.RunID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}
	step := steps[0]
	requestedAt := time.Now().UTC()
	resolution := fruntime.EffectResolution{
		ID: "resolution-recovery-1", AttemptID: "resolution-attempt-1",
		Action: fruntime.EffectResolutionCompensate, Status: fruntime.EffectResolutionIntent,
		Actor: "operator-1", Reason: "recover a committed compensation outcome",
		CompensationKey: "compensation-recovery-1", RequestedAt: requestedAt,
	}
	step.EffectResolution = &resolution
	operation := core.EffectOperation{
		Key: resolution.CompensationKey, ParentKey: step.OperationKey, Kind: "compensation", Name: step.NodeID,
		Class: core.EffectCompensatable, Status: core.EffectSucceeded, IdempotencyKey: resolution.CompensationKey,
	}
	payload, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	event := fruntime.Event{
		ID: "effect-recovery-outcome", GraphID: failed.GraphID, GraphSessionID: failed.GraphSessionID,
		RunID: failed.RunID, StepID: step.StepID, TaskID: step.TaskID, NodeID: step.NodeID,
		OperationKey: operation.Key, Type: fruntime.EventEffectOutcome, Timestamp: requestedAt, Payload: payload,
	}
	commitResult, err := store.Commit(context.Background(), fruntime.Commit{
		TransactionID: "effect-recovery-fixture",
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteUpdate, Run: failed},
		Steps:         []fruntime.StepWrite{{Mode: fruntime.StepWriteUpdate, Step: step}},
		Events:        []fruntime.Event{event},
	})
	if err != nil || commitResult.Outcome != fruntime.TransactionCommitted {
		t.Fatalf("Commit() = %#v, %v", commitResult, err)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedStore, err := filestore.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	reopenedRunner := mustNewGraphRunner(t, workflow, reopenedStore.ExecutionStore(), reopenedStore.CheckpointStore(), state.NewJSONStateCodec(""), reopenedStore.EventSink(),
		fruntime.WithRuntimeTransactionStore(reopenedStore), fruntime.WithStoreCloser(reopenedStore))
	t.Cleanup(func() { _ = reopenedRunner.Close() })
	result, err := reopenedRunner.ResolveEffect(context.Background(), failed.RunID, fruntime.EffectResolutionRequest{
		ResolutionID: resolution.ID, StepID: step.StepID, Action: resolution.Action,
		Actor: resolution.Actor, Reason: resolution.Reason,
	}, nil)
	if err != nil || result.Run.Status != fruntime.RunStatusPaused || result.Resolution.Status != fruntime.EffectResolutionSucceeded || compensations != 0 {
		t.Fatalf("recovered ResolveEffect() = %#v, compensations = %d, error = %v", result, compensations, err)
	}
}

func newEffectResolutionTestRunner(t *testing.T, target core.Node) (*fruntime.GraphRunner, *fruntime.MemoryRuntimeStore) {
	t.Helper()
	workflow := newEffectResolutionTestGraph(t, target)
	store := fruntime.NewMemoryRuntimeStore()
	return mustNewGraphRunner(t, workflow, store, store, state.NewJSONStateCodec(""), store, fruntime.WithRuntimeTransactionStore(store)), store
}

func newEffectResolutionTestGraph(t *testing.T, target core.Node) *Graph {
	t.Helper()
	workflow := NewGraph(nil)
	if err := workflow.AddNode(target); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{ID: target.ID(), Type: "test", Name: target.Name()}); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetEntryPoint(target.ID()); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint(target.ID()); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func assertEffectResolutionEvent(t *testing.T, events []fruntime.Event, eventType fruntime.EventType) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return
		}
	}
	t.Fatalf("event %q missing from %#v", eventType, events)
}
