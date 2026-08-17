package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestGraphAllowsMultipleDefaultEdges(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a", "b")
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}

	next, err := newRunnerGraph(g).ResolveNextNodes(context.Background(), "router", state.NewState())
	if err != nil {
		t.Fatalf("resolve next nodes: %v", err)
	}
	if strings.Join(next, ",") != "a,b" {
		t.Fatalf("expected next nodes in definition order, got %#v", next)
	}
}

func TestGraphRejectsDuplicateEdge(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a")
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "a"); err == nil {
		t.Fatal("expected duplicate edge error")
	}
}

func TestGraphDefinitionFanOutRoundTrip(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a", "b", "done")
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", "done"); err != nil {
		t.Fatalf("add a -> done: %v", err)
	}
	if err := g.AddEdge("b", "done"); err != nil {
		t.Fatalf("add b -> done: %v", err)
	}

	def, err := g.Definition()
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if len(def.Edges) != 4 {
		t.Fatalf("expected four edges, got %#v", def.Edges)
	}
	if def.Edges[0].From != "router" || def.Edges[0].To != "a" {
		t.Fatalf("unexpected first edge %#v", def.Edges[0])
	}
	if def.Edges[1].From != "router" || def.Edges[1].To != "b" {
		t.Fatalf("unexpected second edge %#v", def.Edges[1])
	}
}

func TestGraphDefinitionPreservesMetadata(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "input")
	g.setDefinitionMetadata(dsl.GraphDefinition{
		Version:      dsl.GraphDefinitionVersion,
		Name:         "debug_graph",
		Description:  "debug description",
		StateModules: []dsl.StateModuleRef{{Name: "weaveflow.protocols", Version: "1"}},
		Metadata: map[string]any{
			"web": map[string]any{
				"positions": map[string]any{
					"input": map[string]any{"x": 120, "y": 80},
				},
				"virtual_node_ids": []any{"__start__", "__end__"},
			},
		},
	})

	def, err := g.Definition()
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if def.Name != "debug_graph" || def.Description != "debug description" {
		t.Fatalf("metadata fields not preserved: %#v", def)
	}
	web, ok := def.Metadata["web"].(map[string]any)
	if !ok {
		t.Fatalf("web metadata missing: %#v", def.Metadata)
	}
	if _, ok := web["positions"].(map[string]any); !ok {
		t.Fatalf("positions metadata missing: %#v", web)
	}
}

func TestResolveNextNodeRejectsFanOut(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a", "b")
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}

	_, err := newRunnerGraph(g).ResolveNextNode(context.Background(), "router", state.NewState())
	if err == nil || !strings.Contains(err.Error(), "ResolveNextNodes") {
		t.Fatalf("expected fan-out compatibility error, got %v", err)
	}
}

func TestGraphRejectsConditionalEdgeWithMultipleDefaultFallbacks(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a", "b", "fallback", "done")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{Reason: "not matched"}, nil
	})
	if err := g.AddConditionalEdge("router", "a", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add fallback b: %v", err)
	}
	if err := g.AddEdge("router", "fallback"); err != nil {
		t.Fatalf("add fallback fallback: %v", err)
	}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "multiple default fallback edges") {
		t.Fatalf("expected conditional multi-fallback validation error, got %v", err)
	}
}

func TestResolveNextNodesConditionalMatchingFallbackAndErrors(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "matched", "fallback")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(ctx context.Context, current *state.State) (registry.RouteDecision, error) {
		value, ok := state.NewAccess(current).ReadAny(state.Shared("route"))
		return registry.RouteDecision{Matched: ok && value == "matched", Reason: "route compared"}, nil
	})
	if err := g.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("router", "fallback"); err != nil {
		t.Fatalf("add fallback edge: %v", err)
	}
	if err := g.AddEdge("matched", EndNodeRef); err != nil {
		t.Fatalf("add matched -> end: %v", err)
	}

	input := state.NewState()
	if err := state.SetPath(input, "shared.route", "matched"); err != nil {
		t.Fatalf("set route: %v", err)
	}
	next, err := newRunnerGraph(g).ResolveNextNodes(context.Background(), "router", input)
	if err != nil {
		t.Fatalf("resolve matched conditional edge: %v", err)
	}
	if strings.Join(next, ",") != "matched" {
		t.Fatalf("next nodes = %#v, want [matched]", next)
	}

	input = state.NewState()
	next, err = newRunnerGraph(g).ResolveNextNodes(context.Background(), "router", input)
	if err != nil {
		t.Fatalf("resolve fallback edge: %v", err)
	}
	if strings.Join(next, ",") != "fallback" {
		t.Fatalf("next nodes = %#v, want [fallback]", next)
	}

	g.defaultEdges["router"] = nil
	_, err = newRunnerGraph(g).ResolveNextNodes(context.Background(), "router", input)
	if err == nil || !strings.Contains(err.Error(), "no matching conditional edge") {
		t.Fatalf("expected no-match error, got %v", err)
	}
}

func TestGraphRunAndRunnerStartConditionalRoutingAgree(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "matched", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("result"), "matched")
	})
	mustAddNode(t, g, "fallback", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("result"), "fallback")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(ctx context.Context, current *state.State) (registry.RouteDecision, error) {
		value, ok := state.NewAccess(current).ReadAny(state.Shared("route"))
		return registry.RouteDecision{Matched: ok && value == "matched", Reason: "route compared"}, nil
	})
	if err := g.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("router", "fallback"); err != nil {
		t.Fatalf("add fallback edge: %v", err)
	}
	if err := g.AddEdge("matched", EndNodeRef); err != nil {
		t.Fatalf("add matched -> end: %v", err)
	}
	if err := g.AddEdge("fallback", EndNodeRef); err != nil {
		t.Fatalf("add fallback -> end: %v", err)
	}

	initial := state.NewState()
	if err := state.SetPath(initial, "shared.route", "matched"); err != nil {
		t.Fatalf("set route: %v", err)
	}

	graphState, err := g.Run(context.Background(), initial)
	if err != nil {
		t.Fatalf("graph run: %v", err)
	}

	dir := t.TempDir()
	runner, _ := mustNewFileGraphRunner(t, g, dir)
	run, runnerState, err := runner.Start(context.Background(), initial)
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}

	graphResult, graphOK := state.NewAccess(graphState).ReadAny(state.Shared("result"))
	runnerValue, runnerOK := state.NewAccess(runnerState).ReadAny(state.Shared("result"))
	if !graphOK || !runnerOK || graphResult != runnerValue || graphResult != "matched" {
		t.Fatalf("graph result=%#v ok=%v runner result=%#v ok=%v", graphResult, graphOK, runnerValue, runnerOK)
	}
}

func TestGraphRunAndRunnerStartContractWriteBehaviorAgree(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "writer", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("forbidden"), true)
	})
	if err := g.SetEntryPoint("writer"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("writer", EndNodeRef); err != nil {
		t.Fatalf("add writer -> end: %v", err)
	}
	g.setNodeContracts(map[string]state.Contract{
		"writer": state.NewContract(state.FieldAccess{
			Path: state.Shared("allowed"),
			Mode: state.AccessWrite,
		}),
	})

	if _, err := g.Run(context.Background(), state.NewState()); err == nil || !strings.Contains(err.Error(), "undeclared path") {
		t.Fatalf("expected Graph.Run contract violation, got %v", err)
	}

	dir := t.TempDir()
	runner, _ := mustNewFileGraphRunner(t, g, dir, fruntime.WithContractPolicy(fruntime.ContractPolicy{
		EnforceWrites: true,
	}))
	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "undeclared path") {
		t.Fatalf("expected GraphRunner.Start contract violation, got %v", err)
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
}

func TestResolvedContractsProjectNodeInputAndPreserveUnboundState(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "worker", func(_ context.Context, access *state.Access) error {
		if _, ok := access.ReadAny(state.Shared("secret")); ok {
			return fmt.Errorf("unbound secret was visible")
		}
		value, ok := access.ReadAny(state.Shared("allowed"))
		if !ok {
			return fmt.Errorf("allowed input is missing")
		}
		return access.SetAny(state.Shared("result"), value)
	})
	if err := g.SetEntryPoint("worker"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("worker", EndNodeRef); err != nil {
		t.Fatalf("add worker -> end: %v", err)
	}
	g.setInitialStatePaths([]string{"shared.allowed"})
	g.setNodeContracts(map[string]state.Contract{
		"worker": state.NewContract(
			state.FieldAccess{Path: state.Shared("allowed"), Mode: state.AccessRead, Required: true, Merge: state.MergeReplace},
			state.FieldAccess{Path: state.Shared("result"), Mode: state.AccessWrite, Merge: state.MergeReplace},
		),
	})
	initial := state.FromShared(map[string]any{"allowed": "visible", "secret": "hidden"})

	finalState, err := g.Run(context.Background(), initial)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	access := state.NewAccess(finalState)
	if result, ok := access.ReadAny(state.Shared("result")); !ok || result != "visible" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
	if secret, ok := access.ReadAny(state.Shared("secret")); !ok || secret != "hidden" {
		t.Fatalf("unbound state was not preserved: secret=%#v ok=%v", secret, ok)
	}

	dir := t.TempDir()
	runner, _ := mustNewFileGraphRunner(t, g, dir)
	if runner.ContractValidation() != core.ContractValidationStrict {
		t.Fatalf("runner contract validation = %q, want strict", runner.ContractValidation())
	}
	_, runnerState, err := runner.Start(context.Background(), initial)
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	runnerAccess := state.NewAccess(runnerState)
	if result, ok := runnerAccess.ReadAny(state.Shared("result")); !ok || result != "visible" {
		t.Fatalf("runner result = %#v ok=%v", result, ok)
	}
	if secret, ok := runnerAccess.ReadAny(state.Shared("secret")); !ok || secret != "hidden" {
		t.Fatalf("runner unbound state was not preserved: secret=%#v ok=%v", secret, ok)
	}
}

func TestResolveNextNodesUsesProvidedContext(t *testing.T) {
	t.Parallel()

	type routeKey struct{}
	g := newTestGraph(t, "router", "matched", "fallback")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(ctx context.Context, current *state.State) (registry.RouteDecision, error) {
		value, _ := ctx.Value(routeKey{}).(string)
		return registry.RouteDecision{Matched: value == "matched", Reason: "context route compared"}, nil
	})
	if err := g.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("router", "fallback"); err != nil {
		t.Fatalf("add fallback edge: %v", err)
	}
	if err := g.AddEdge("matched", EndNodeRef); err != nil {
		t.Fatalf("add matched -> end: %v", err)
	}

	ctx := context.WithValue(context.Background(), routeKey{}, "matched")
	next, err := newRunnerGraph(g).ResolveNextNodes(ctx, "router", state.NewState())
	if err != nil {
		t.Fatalf("resolve next nodes: %v", err)
	}
	if strings.Join(next, ",") != "matched" {
		t.Fatalf("next nodes = %#v, want [matched]", next)
	}
}

func TestGraphRejectsConditionalEdgeWithoutFallback(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "matched", "done")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{Reason: "not matched"}, nil
	})
	if err := g.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("matched", "done"); err != nil {
		t.Fatalf("add matched -> done: %v", err)
	}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "no default fallback edge") {
		t.Fatalf("expected missing conditional fallback validation error, got %v", err)
	}
}

func TestGraphAllowsConditionalEdgeWithDefaultFallback(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "matched", "done")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{Matched: true, Reason: "matched"}, nil
	})
	if err := g.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("router", "done"); err != nil {
		t.Fatalf("add fallback edge: %v", err)
	}
	if err := g.AddEdge("matched", "done"); err != nil {
		t.Fatalf("add matched -> done: %v", err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("validate graph: %v", err)
	}
}

func TestGraphRejectsUnreachableNode(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "entry", "done", "unused")
	if err := g.AddEdge("entry", "done"); err != nil {
		t.Fatalf("add entry -> done: %v", err)
	}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected unreachable validation error, got %v", err)
	}
}

func TestGraphRejectsReachableDeadEnd(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "entry", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "dead", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	if err := g.SetEntryPoint("entry"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("entry", "dead"); err != nil {
		t.Fatalf("add entry -> dead: %v", err)
	}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "no outgoing edge") {
		t.Fatalf("expected dead-end validation error, got %v", err)
	}
}

func TestGraphRejectsReachableCycleWithoutEnd(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	if err := g.SetEntryPoint("a"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatalf("add a -> b: %v", err)
	}
	if err := g.AddEdge("b", "a"); err != nil {
		t.Fatalf("add b -> a: %v", err)
	}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "cannot reach graph end") {
		t.Fatalf("expected no terminal path validation error, got %v", err)
	}
}

func TestGraphRejectsFinishPointOutgoingEdge(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "entry", "done", "after")
	if err := g.SetFinishPoint("done"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	if err := g.AddEdge("entry", "done"); err != nil {
		t.Fatalf("add entry -> done: %v", err)
	}
	if err := g.AddEdge("done", "after"); err != nil {
		t.Fatalf("add done -> after: %v", err)
	}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "finish point") {
		t.Fatalf("expected finish point outgoing validation error, got %v", err)
	}
}

func TestFanOutFanInCompile(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls []string
	)
	record := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, id)
	}

	g := NewGraph(nil)
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		record("router")
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		record("a")
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		record("b")
		return access.AppendAny(state.Shared("branches"), "b")
	})
	mustAddNode(t, g, "collector", func(ctx context.Context, access *state.Access) error {
		record("collector")
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})

	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector"},
		{"b", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	finalState, err := g.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("run fan-out/fan-in graph: %v", err)
	}
	count, ok := state.NewAccess(finalState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("expected collector to see two branches, got %#v ok=%v", count, ok)
	}

	mu.Lock()
	defer mu.Unlock()
	collectorCalls := 0
	for _, call := range calls {
		if call == "collector" {
			collectorCalls++
		}
	}
	if collectorCalls != 1 {
		t.Fatalf("expected collector to execute once, calls=%#v", calls)
	}
}

func TestFanOutFanInWaitsForUnevenBranches(t *testing.T) {
	t.Parallel()

	var collectorCalls atomic.Int32
	g := NewGraph(nil)
	mustAddNode(t, g, "router", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "fast", func(_ context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "fast")
	})
	mustAddNode(t, g, "slow", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "slow_tail", func(_ context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "slow")
	})
	mustAddNode(t, g, "collector", func(_ context.Context, access *state.Access) error {
		collectorCalls.Add(1)
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})

	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "fast"},
		{"router", "slow"},
		{"fast", "collector"},
		{"slow", "slow_tail"},
		{"slow_tail", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	finalState, err := g.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("run uneven fan-in graph: %v", err)
	}
	count, ok := state.NewAccess(finalState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("collector branch count = %#v ok=%v, want 2", count, ok)
	}
	if calls := collectorCalls.Load(); calls != 1 {
		t.Fatalf("collector calls = %d, want 1", calls)
	}
	if _, ok := state.ReadPath(finalState, "internal.graph_scheduler"); ok {
		t.Fatal("final state retained graph scheduler metadata")
	}

	collectorCalls.Store(0)
	runnable, err := g.Compile()
	if err != nil {
		t.Fatalf("compile uneven fan-in graph: %v", err)
	}
	var streamedState *state.State
	for event := range runnable.Stream(context.Background(), state.NewState()) {
		if event.Event != EventChainEnd {
			continue
		}
		if event.Error != nil {
			t.Fatalf("stream uneven fan-in graph: %v", event.Error)
		}
		streamedState = event.State
	}
	if streamedState == nil {
		t.Fatal("stream did not emit final state")
	}
	streamedCount, ok := state.NewAccess(streamedState).ReadAny(state.Shared("branch_count"))
	if !ok || streamedCount != 2 {
		t.Fatalf("streamed collector branch count = %#v ok=%v, want 2", streamedCount, ok)
	}
	if calls := collectorCalls.Load(); calls != 1 {
		t.Fatalf("streamed collector calls = %d, want 1", calls)
	}
}

func TestStreamPublishesIndependentStrictStateSnapshots(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "writer", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("output"), map[string]any{"value": "scheduler"})
	})
	if err := workflow.SetEntryPoint("writer"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := workflow.SetFinishPoint("writer"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	runnable, err := workflow.Compile()
	if err != nil {
		t.Fatalf("compile graph: %v", err)
	}
	initialState := state.FromShared(map[string]any{
		"input": map[string]any{"value": "caller"},
	})

	var startState *state.State
	var finalState *state.State
	nodeStates := make([]*state.State, 0)
	for event := range runnable.Stream(context.Background(), initialState) {
		if event.Error != nil {
			t.Fatalf("stream event %q error = %v", event.Event, event.Error)
		}
		switch event.Event {
		case EventChainStart:
			startState = event.State
		case EventNodeStart, EventNodeComplete:
			nodeStates = append(nodeStates, event.State)
		case EventChainEnd:
			finalState = event.State
		}
	}
	if startState == nil || finalState == nil || len(nodeStates) != 2 {
		t.Fatalf("stream states: start=%p nodes=%d final=%p", startState, len(nodeStates), finalState)
	}
	if startState == initialState {
		t.Fatal("chain-start event exposed caller-owned initial state")
	}
	for _, nodeState := range nodeStates {
		if nodeState == initialState || nodeState == startState || nodeState == finalState {
			t.Fatal("stream reused a mutable state envelope across ownership boundaries")
		}
	}
	if err := startState.SetSection(state.SectionShared, map[string]any{"poisoned": "start"}); err != nil {
		t.Fatalf("mutate start snapshot: %v", err)
	}
	if err := nodeStates[0].SetSection(state.SectionShared, map[string]any{"poisoned": "node"}); err != nil {
		t.Fatalf("mutate node snapshot: %v", err)
	}
	initialValue, ok := state.NewAccess(initialState).ReadAny(state.Shared("input"))
	if !ok || initialValue.(map[string]any)["value"] != "caller" {
		t.Fatalf("caller initial state = %#v, found = %v", initialValue, ok)
	}
	finalValue, ok := state.NewAccess(finalState).ReadAny(state.Shared("output"))
	if !ok || finalValue.(map[string]any)["value"] != "scheduler" {
		t.Fatalf("final stream state = %#v, found = %v", finalValue, ok)
	}
}

func TestStreamPublishesCloneErrorWithoutExposingOpaqueInitialState(t *testing.T) {
	workflow := NewGraph(nil)
	var executed atomic.Bool
	mustAddNode(t, workflow, "entry", func(context.Context, *state.Access) error {
		executed.Store(true)
		return nil
	})
	if err := workflow.SetEntryPoint("entry"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := workflow.SetFinishPoint("entry"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	runnable, err := workflow.Compile()
	if err != nil {
		t.Fatalf("compile graph: %v", err)
	}
	initialState := state.FromShared(map[string]any{"opaque": make(chan int)})

	events := make([]StreamEvent, 0)
	for event := range runnable.Stream(context.Background(), initialState) {
		events = append(events, event)
	}
	if len(events) != 1 {
		t.Fatalf("stream emitted %d events, want one clone failure", len(events))
	}
	event := events[0]
	if event.Event != EventChainStart || event.State != nil || event.Error == nil || !strings.Contains(event.Error.Error(), "state cannot be safely cloned") {
		t.Fatalf("clone failure event = %#v", event)
	}
	if executed.Load() {
		t.Fatal("graph executed after the initial stream snapshot failed")
	}
}

func TestGraphNodeInputOmitsReservedState(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "entry", func(_ context.Context, access *state.Access) error {
		for _, path := range []state.Path{
			state.Internal("graph_scheduler"),
			state.Internal("private"),
			state.Runtime("run_id"),
		} {
			if value, found := access.ReadAny(path); found {
				return fmt.Errorf("node observed reserved state %q: %#v", path.String(), value)
			}
		}
		return access.SetAny(state.Shared("executed"), true)
	})
	if err := workflow.SetEntryPoint("entry"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := workflow.SetFinishPoint("entry"); err != nil {
		t.Fatalf("set finish: %v", err)
	}

	initialState := state.NewState()
	if err := state.SetPath(initialState, state.Internal("private").String(), "hidden"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetPath(initialState, state.Runtime("run_id").String(), "hidden-run"); err != nil {
		t.Fatal(err)
	}
	finalState, err := workflow.Run(context.Background(), initialState)
	if err != nil {
		t.Fatalf("run graph: %v", err)
	}
	executed, found := state.NewAccess(finalState).ReadAny(state.Shared("executed"))
	if !found || executed != true {
		t.Fatalf("node output = %#v, found = %v", executed, found)
	}
}

func TestGraphConditionReceivesIsolatedBusinessState(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "matched", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("matched"), true)
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("matched"); err != nil {
		t.Fatal(err)
	}
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(_ context.Context, current *state.State) (registry.RouteDecision, error) {
		for _, path := range []state.Path{state.Internal("graph_scheduler"), state.Internal("private"), state.Runtime("run_id")} {
			if value, found := state.ReadPath(current, path.String()); found {
				return registry.RouteDecision{}, fmt.Errorf("condition observed reserved state %q: %#v", path.String(), value)
			}
		}
		if err := state.SetPath(current, state.Shared("condition_mutation").String(), true); err != nil {
			return registry.RouteDecision{}, err
		}
		return registry.RouteDecision{Matched: true, Reason: "isolated"}, nil
	})
	if err := workflow.AddConditionalEdge("router", "matched", condition); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("router", EndNodeRef); err != nil {
		t.Fatal(err)
	}

	initialState := state.NewState()
	if err := state.SetPath(initialState, state.Internal("private").String(), "hidden"); err != nil {
		t.Fatal(err)
	}
	if err := state.SetPath(initialState, state.Runtime("run_id").String(), "hidden-run"); err != nil {
		t.Fatal(err)
	}
	finalState, err := workflow.Run(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, found := state.ReadPath(finalState, "shared.condition_mutation"); found {
		t.Fatal("condition mutation leaked into graph state")
	}
	if matched, found := state.ReadPath(finalState, "shared.matched"); !found || matched != true {
		t.Fatalf("matched = %#v, found=%v", matched, found)
	}
}

func TestFanInDoesNotWaitForInactiveConditionalPredecessor(t *testing.T) {
	t.Parallel()

	var collectorCalls atomic.Int32
	g := NewGraph(nil)
	mustAddNode(t, g, "router", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "selected", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("selected"), true)
	})
	mustAddNode(t, g, "inactive", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("inactive"), true)
	})
	mustAddNode(t, g, "collector", func(context.Context, *state.Access) error {
		collectorCalls.Add(1)
		return nil
	})

	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) (registry.RouteDecision, error) {
		return registry.RouteDecision{Matched: true, Reason: "matched"}, nil
	})
	if err := g.AddConditionalEdge("router", "selected", condition); err != nil {
		t.Fatalf("add selected condition: %v", err)
	}
	if err := g.AddEdge("router", "inactive"); err != nil {
		t.Fatalf("add inactive fallback: %v", err)
	}
	if err := g.AddEdge("selected", "collector"); err != nil {
		t.Fatalf("add selected -> collector: %v", err)
	}
	if err := g.AddEdge("inactive", "collector"); err != nil {
		t.Fatalf("add inactive -> collector: %v", err)
	}

	finalState, err := g.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("run conditional fan-in graph: %v", err)
	}
	access := state.NewAccess(finalState)
	if selected, ok := access.ReadAny(state.Shared("selected")); !ok || selected != true {
		t.Fatalf("selected branch value = %#v ok=%v", selected, ok)
	}
	if _, ok := access.ReadAny(state.Shared("inactive")); ok {
		t.Fatal("inactive conditional predecessor executed")
	}
	if calls := collectorCalls.Load(); calls != 1 {
		t.Fatalf("collector calls = %d, want 1", calls)
	}
}

func TestFanOutFanInCompileRejectsParallelMergeConflict(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("answer"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("answer"), "b")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	_, err := g.Run(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "parallel state merge conflict") {
		t.Fatalf("expected parallel merge conflict, got %v", err)
	}
}

func TestFanOutFanInCompileSurfacesOriginalBranchFailure(t *testing.T) {
	t.Parallel()

	g := NewGraph(nil)
	mustAddNode(t, g, "router", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "success", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("result"), "ok")
	})
	mustAddNode(t, g, "failed", func(context.Context, *state.Access) error {
		return errors.New("branch failed")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "success"},
		{"router", "failed"},
		{"success", EndNodeRef},
		{"failed", EndNodeRef},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	_, err := g.Run(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "branch failed") {
		t.Fatalf("expected original branch failure, got %v", err)
	}
	if strings.Contains(err.Error(), "parallel state merge requires branch patches") {
		t.Fatalf("branch failure was masked by merge error: %v", err)
	}
}

func newTestGraph(t *testing.T, ids ...string) *Graph {
	t.Helper()
	g := NewGraph(nil)
	for _, id := range ids {
		mustAddNode(t, g, id, func(ctx context.Context, access *state.Access) error {
			return nil
		})
	}
	if len(ids) > 0 {
		if err := g.SetEntryPoint(ids[0]); err != nil {
			t.Fatalf("set entry point: %v", err)
		}
		if err := g.SetFinishPoint(ids[len(ids)-1]); err != nil {
			t.Fatalf("set finish point: %v", err)
		}
	}
	return g
}

func mustAddNode(t *testing.T, g *Graph, id string, fn func(context.Context, *state.Access) error) {
	t.Helper()
	err := g.AddNode(node.NewFuncNode(node.Spec{ID: id, Name: id}, func(ctx core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), fn(ctx, access)
	}))
	if err != nil {
		t.Fatalf("add node %q: %v", id, err)
	}
	if err := g.SetNodeSpec(dsl.GraphNodeSpec{ID: id, Type: "test", Name: id}); err != nil {
		t.Fatalf("set node spec: %v", err)
	}
}
