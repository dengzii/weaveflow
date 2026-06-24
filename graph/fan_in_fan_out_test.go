package graph

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
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

	next, err := newRunnerGraph(g).ResolveNextNodes("router", state.NewState())
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

func TestResolveNextNodeRejectsFanOut(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a", "b")
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}

	_, err := newRunnerGraph(g).ResolveNextNode("router", state.NewState())
	if err == nil || !strings.Contains(err.Error(), "ResolveNextNodes") {
		t.Fatalf("expected fan-out compatibility error, got %v", err)
	}
}

func TestGraphRejectsConditionalEdgeWithMultipleDefaultFallbacks(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "a", "b", "fallback", "done")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) bool {
		return false
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

func TestGraphRejectsConditionalEdgeWithoutFallback(t *testing.T) {
	t.Parallel()

	g := newTestGraph(t, "router", "matched", "done")
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) bool {
		return false
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
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(context.Context, *state.State) bool {
		return true
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

	g := NewGraph()
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

	g := NewGraph()
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

	g := NewGraph()
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
	count, ok := state.NewAccess(nil, finalState).ReadAny(state.Shared("branch_count"))
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

func TestFanOutFanInCompileRejectsParallelMergeConflict(t *testing.T) {
	t.Parallel()

	g := NewGraph()
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

func newTestGraph(t *testing.T, ids ...string) *Graph {
	t.Helper()
	g := NewGraph()
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
	err := g.AddNode(node.NewFuncNode(node.Spec{ID: id, Name: id}, func(ctx core.Context, access *state.Access) error {
		return fn(ctx, access)
	}))
	if err != nil {
		t.Fatalf("add node %q: %v", id, err)
	}
	g.SetNodeSpec(dsl.GraphNodeSpec{ID: id, Type: "test", Name: id})
}
