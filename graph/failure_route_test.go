package graph

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestFailureRouteRunsAfterNodeFailure(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "failed", func(context.Context, *state.Access) error {
		return core.NewExecutionError(core.ErrorUnavailable, "provider unavailable", nil, nil)
	})
	mustAddResultNode(t, workflow, "fallback", func(ctx core.Context, access *state.Access) (core.NodeResult, error) {
		failure, ok := ctx.Failure()
		if !ok || failure.Stage != string(dsl.FailureStageNode) || failure.ErrorClass != core.ErrorUnavailable || failure.SourceNodeID != "failed" {
			return core.NodeResult{}, errors.New("failure context is missing or invalid")
		}
		return core.Success(), access.SetAny(state.Shared("handled"), true)
	})
	if err := workflow.SetEntryPoint("failed"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := workflow.AddFailureRoute("failed", "fallback", dsl.FailureRouteSpec{Stages: []dsl.FailureStage{dsl.FailureStageNode}, ErrorClasses: []string{string(core.ErrorUnavailable)}}); err != nil {
		t.Fatalf("add failure route: %v", err)
	}
	if err := workflow.AddEdge("failed", EndNodeRef); err != nil {
		t.Fatalf("add success edge: %v", err)
	}
	if err := workflow.SetFinishPoint("fallback"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	result, err := workflow.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	value, ok := state.ReadPath(result, "shared.handled")
	if !ok || value != true {
		t.Fatalf("handled = %#v", value)
	}
}

func TestRunnerFailureRoutePersistsFailedStepAndRoutedEvent(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "failed", func(context.Context, *state.Access) error {
		return core.NewExecutionError(core.ErrorUnavailable, "provider unavailable", nil, map[string]any{"provider": "test"})
	})
	mustAddResultNode(t, workflow, "fallback", func(ctx core.Context, access *state.Access) (core.NodeResult, error) {
		failure, ok := ctx.Failure()
		if !ok || failure.Details["provider"] != "test" {
			return core.NodeResult{}, errors.New("persisted failure context is missing")
		}
		return core.Success(), access.SetAny(state.Shared("handled"), true)
	})
	if err := workflow.SetEntryPoint("failed"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddFailureRoute("failed", "fallback", dsl.FailureRouteSpec{CatchAll: true}); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("failed", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("fallback"); err != nil {
		t.Fatal(err)
	}
	store := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, store, store, state.NewJSONStateCodec(""), store)
	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("status = %q", run.Status)
	}
	if handled, _ := state.ReadPath(finalState, "shared.handled"); handled != true {
		t.Fatalf("handled = %#v", handled)
	}
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]fruntime.StepStatus{}
	for _, step := range steps {
		statuses[step.NodeID] = step.Status
	}
	if statuses["failed"] != fruntime.StepStatusFailed || statuses["fallback"] != fruntime.StepStatusSucceeded {
		t.Fatalf("step statuses = %#v", statuses)
	}
	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		found = found || event.Type == fruntime.EventFailureRouted
	}
	if !found {
		t.Fatalf("events missing failure.routed: %#v", events)
	}
}

func TestFailureRoutePreservesSuccessfulParallelSuccessors(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "successful", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("branch_value"), "ready")
	})
	mustAddNode(t, workflow, "failed", func(context.Context, *state.Access) error {
		return core.NewExecutionError(core.ErrorUnavailable, "provider unavailable", nil, nil)
	})
	mustAddNode(t, workflow, "success_next", func(_ context.Context, access *state.Access) error {
		value, ok := access.ReadAny(state.Shared("branch_value"))
		if !ok || value != "ready" {
			return errors.New("successful branch state is missing")
		}
		return access.SetAny(state.Shared("success_next"), true)
	})
	mustAddResultNode(t, workflow, "fallback", func(ctx core.Context, access *state.Access) (core.NodeResult, error) {
		failure, ok := ctx.Failure()
		if !ok || failure.SourceNodeID != "failed" {
			return core.NodeResult{}, errors.New("failure context is missing")
		}
		value, ok := access.ReadAny(state.Shared("branch_value"))
		if !ok || value != "ready" {
			return core.NodeResult{}, errors.New("fallback cannot see successful branch state")
		}
		return core.Success(), access.SetAny(state.Shared("fallback"), true)
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddFailureRoute("failed", "fallback", dsl.FailureRouteSpec{CatchAll: true}); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{
		{"router", "successful"},
		{"router", "failed"},
		{"successful", "success_next"},
		{"failed", EndNodeRef},
		{"success_next", EndNodeRef},
		{"fallback", EndNodeRef},
	} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	store := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, store, store, state.NewJSONStateCodec(""), store)
	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil || run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run = %#v, error = %v", run, err)
	}
	for _, path := range []string{"shared.success_next", "shared.fallback"} {
		if value, _ := state.ReadPath(finalState, path); value != true {
			t.Fatalf("%s = %#v, want true", path, value)
		}
	}
}

func TestFailureRouteDoesNotCatchUnclassifiedError(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "failed", func(context.Context, *state.Access) error { return errors.New("plain failure") })
	mustAddNode(t, workflow, "fallback", func(context.Context, *state.Access) error { return nil })
	if err := workflow.SetEntryPoint("failed"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := workflow.AddFailureRoute("failed", "fallback", dsl.FailureRouteSpec{CatchAll: true}); err != nil {
		t.Fatalf("add failure route: %v", err)
	}
	if err := workflow.AddEdge("failed", EndNodeRef); err != nil {
		t.Fatalf("add success edge: %v", err)
	}
	if err := workflow.SetFinishPoint("fallback"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	if _, err := workflow.Run(context.Background(), state.NewState()); err == nil {
		t.Fatal("expected terminal failure")
	}
}

func TestFailureRouteDoesNotRouteMultipleFailuresFromParallelWave(t *testing.T) {
	workflow := NewGraph(nil)
	var fallbackCalls atomic.Int32
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "successful", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("partial_commit"), true)
	})
	mustAddNode(t, workflow, "fallback", func(context.Context, *state.Access) error {
		fallbackCalls.Add(1)
		return nil
	})
	for _, nodeID := range []string{"first", "second"} {
		identifier := nodeID
		mustAddNode(t, workflow, identifier, func(context.Context, *state.Access) error {
			return core.NewExecutionError(core.ErrorUnavailable, identifier+" unavailable", nil, nil)
		})
		if err := workflow.AddFailureRoute(identifier, "fallback", dsl.FailureRouteSpec{CatchAll: true}); err != nil {
			t.Fatalf("add %s failure route: %v", identifier, err)
		}
	}
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "first"}, {"router", "second"}, {"router", "successful"}, {"first", EndNodeRef}, {"second", EndNodeRef}, {"successful", EndNodeRef}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}
	if err := workflow.SetFinishPoint("fallback"); err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Run(context.Background(), state.NewState())
	if err == nil {
		t.Fatal("expected parallel wave failure")
	}
	if !strings.Contains(err.Error(), "first unavailable") || !strings.Contains(err.Error(), "second unavailable") {
		t.Fatalf("parallel failure = %v, want both branch errors", err)
	}
	if value, ok := state.ReadPath(result, "shared.partial_commit"); ok {
		t.Fatalf("partial committed value = %#v", value)
	}
	if fallbackCalls.Load() != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallbackCalls.Load())
	}
}

func TestParallelWaveUnroutedFailureDoesNotCommitSuccessfulSibling(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "successful", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("partial_commit"), true)
	})
	mustAddNode(t, workflow, "failed", func(context.Context, *state.Access) error {
		return core.NewExecutionError(core.ErrorUnavailable, "provider unavailable", nil, nil)
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "successful"}, {"router", "failed"}, {"successful", EndNodeRef}, {"failed", EndNodeRef}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	result, err := workflow.Run(context.Background(), state.NewState())
	if err == nil {
		t.Fatal("expected parallel wave failure")
	}
	if value, ok := state.ReadPath(result, "shared.partial_commit"); ok {
		t.Fatalf("partial committed value = %#v", value)
	}
}

func TestFailureRouteTimeoutPreservesCompletedSiblingPatch(t *testing.T) {
	workflow := NewGraph(nil)
	siblingCompleted := make(chan struct{})
	releaseSlow := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseSlow)
		}
	}()
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "successful", func(_ context.Context, access *state.Access) error {
		if err := access.SetAny(state.Shared("successful"), true); err != nil {
			return err
		}
		close(siblingCompleted)
		return nil
	})
	mustAddNode(t, workflow, "slow", func(context.Context, *state.Access) error {
		<-siblingCompleted
		<-releaseSlow
		return nil
	})
	mustAddNode(t, workflow, "fallback", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("fallback"), true)
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "successful"}, {"router", "slow"}, {"successful", EndNodeRef}, {"slow", EndNodeRef}, {"fallback", EndNodeRef}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}
	if err := workflow.AddFailureRoute("slow", "fallback", dsl.FailureRouteSpec{
		Stages:       []dsl.FailureStage{dsl.FailureStageNode},
		ErrorClasses: []string{string(core.ErrorTimeout)},
	}); err != nil {
		t.Fatal(err)
	}
	policy := workflow.ExecutionPolicy().NodeDefaults
	policy.Timeout = 20 * time.Millisecond
	policy.Retry.MaxAttempts = 1
	if err := workflow.SetNodeExecutionPolicy("slow", policy); err != nil {
		t.Fatal(err)
	}

	result, err := workflow.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, path := range []string{"shared.successful", "shared.fallback"} {
		if value, ok := state.ReadPath(result, path); !ok || value != true {
			t.Fatalf("%s = %#v, present = %v", path, value, ok)
		}
	}
	close(releaseSlow)
	released = true
}

func TestFailureRouteDoesNotCatchCanceledCondition(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "matched", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "fallback", func(context.Context, *state.Access) error { return nil })
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{}, context.Canceled
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("router", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("matched", EndNodeRef); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddFailureRoute("router", "fallback", dsl.FailureRouteSpec{Stages: []dsl.FailureStage{dsl.FailureStageCondition}, CatchAll: true}); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("fallback"); err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Run(context.Background(), state.NewState()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
