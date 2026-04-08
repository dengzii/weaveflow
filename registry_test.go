package weaveflow

import (
	"context"
	"strings"
	"testing"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type stubBuildModel struct{}

func (stubBuildModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return nil, nil
}

func (stubBuildModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
}

type assignStateNode struct {
	id    string
	key   string
	value any
}

func (n assignStateNode) ID() string          { return n.id }
func (n assignStateNode) Name() string        { return n.id }
func (n assignStateNode) Description() string { return "assign state value" }
func (n assignStateNode) Invoke(ctx context.Context, state State) (State, error) {
	if state == nil {
		state = State{}
	}
	state[n.key] = n.value
	return state, nil
}

func registerAssignNodeType(registry *Registry) {
	registry.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "assign",
			Description: "Assign a value into shared state.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"key":   JSONSchema{"type": "string"},
					"value": JSONSchema{},
				},
				"required":             []string{"key"},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			return assignStateNode{
				id:    spec.ID,
				key:   stringConfig(spec.Config, "key"),
				value: spec.Config["value"],
			}, nil
		},
	})
}

type collectIteratorItemNode struct {
	id             string
	iteratorNodeID string
	targetKey      string
}

func (n collectIteratorItemNode) ID() string          { return n.id }
func (n collectIteratorItemNode) Name() string        { return n.id }
func (n collectIteratorItemNode) Description() string { return "collect iterator item" }
func (n collectIteratorItemNode) Invoke(ctx context.Context, state State) (State, error) {
	_ = ctx

	if state == nil {
		state = State{}
	}

	namespace := state.Namespace(nodes.IteratorStateNamespace)
	if namespace == nil {
		return state, nil
	}
	rawIteratorState, ok := namespace[n.iteratorNodeID]
	if !ok {
		return state, nil
	}

	iteratorState, ok := rawIteratorState.(map[string]any)
	if !ok {
		if typed, ok := rawIteratorState.(State); ok {
			iteratorState = typed
		} else {
			return state, nil
		}
	}

	results, _ := state[n.targetKey].([]string)
	if item, ok := iteratorState["item"].(string); ok {
		results = append(results, item)
	}
	state[n.targetKey] = results
	return state, nil
}

func registerCollectIteratorItemNodeType(registry *Registry) {
	registry.RegisterNodeType(NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "collect_iterator_item",
			Description: "Collect the current iterator item into a string slice.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"iterator_node_id": JSONSchema{"type": "string"},
					"target_key":       JSONSchema{"type": "string"},
				},
				"required":             []string{"iterator_node_id", "target_key"},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			_ = ctx
			return collectIteratorItemNode{
				id:             spec.ID,
				iteratorNodeID: stringConfig(spec.Config, "iterator_node_id"),
				targetKey:      stringConfig(spec.Config, "target_key"),
			}, nil
		},
	})
}

func TestBuildGraphRequiresEntryPoint(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		FinishPoint: "tools",
		Nodes: []GraphNodeSpec{
			{ID: "tools", Type: "tools"},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "entry point") {
		t.Fatalf("expected missing entry point error, got %v", err)
	}
}

func TestBuildGraphRejectsUnknownToolIDs(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint:  "llm",
		FinishPoint: "llm",
		Nodes: []GraphNodeSpec{
			{
				ID:   "llm",
				Type: "llm",
				Config: map[string]any{
					"tool_ids": []any{"missing_tool"},
				},
			},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{
		Model: stubBuildModel{},
		Tools: map[string]tools.Tool{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing_tool") {
		t.Fatalf("expected unknown tool_id error, got %v", err)
	}
}

func TestBuildGraphRequiresGraphResolverForSubgraph(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []GraphNodeSpec{
			{
				ID:   "sub",
				Type: "subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "graph resolver") {
		t.Fatalf("expected missing graph resolver error, got %v", err)
	}
}

func TestBuildGraphRequiresModelForContextReducer(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint:  "reduce",
		FinishPoint: "reduce",
		Nodes: []GraphNodeSpec{
			{
				ID:   "reduce",
				Type: "context_reducer",
			},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected missing model error, got %v", err)
	}
}

func TestBuildGraphRequiresIteratorConfig(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint:  "loop",
		FinishPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key": "items",
				},
			},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("expected missing max_iterations error, got %v", err)
	}
}

func TestBuildGraphRejectsPartialIteratorBuiltInEdges(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "items",
					"max_iterations": 1,
					"continue_to":    "body",
				},
			},
			{ID: "body", Type: "human_message"},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "continue_to and done_to") {
		t.Fatalf("expected partial built-in edge config error, got %v", err)
	}
}

func TestBuildGraphRejectsIteratorBuiltInEdgesWithExplicitOutgoingEdge(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "items",
					"max_iterations": 1,
					"continue_to":    "body",
					"done_to":        "after",
				},
			},
			{ID: "body", Type: "human_message"},
			{ID: "after", Type: "human_message"},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "loop", To: "body"},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with explicit outgoing edges") {
		t.Fatalf("expected mixed outgoing edge error, got %v", err)
	}
}

func TestBuildGraphInvokesSubgraphByGraphRef(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerAssignNodeType(registry)

	root := GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []GraphNodeSpec{
			{
				ID:   "sub",
				Type: "subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}
	child := GraphDefinition{
		EntryPoint:  "set",
		FinishPoint: "set",
		Nodes: []GraphNodeSpec{
			{
				ID:   "set",
				Type: "assign",
				Config: map[string]any{
					"key":   "answer",
					"value": "ok",
				},
			},
		},
	}

	graph, err := registry.BuildGraph(root, &BuildContext{
		GraphResolver: func(graphRef string) (dsl.GraphDefinition, error) {
			if graphRef != "child" {
				t.Fatalf("unexpected graph_ref %q", graphRef)
			}
			return child, nil
		},
	})
	if err != nil {
		t.Fatalf("build graph with subgraph: %v", err)
	}

	state, err := graph.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("run graph with subgraph: %v", err)
	}
	if got := state["answer"]; got != "ok" {
		t.Fatalf("expected subgraph to update state, got %#v", state)
	}
}

func TestBuildGraphRejectsCyclicSubgraphRefs(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	root := GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []GraphNodeSpec{
			{
				ID:   "sub",
				Type: "subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}
	child := GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []GraphNodeSpec{
			{
				ID:   "sub",
				Type: "subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}

	_, err := registry.BuildGraph(root, &BuildContext{
		GraphResolver: func(graphRef string) (dsl.GraphDefinition, error) {
			return child, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cyclic graph_ref dependency") {
		t.Fatalf("expected cyclic graph_ref error, got %v", err)
	}
}

func TestBuildGraphIteratesWithIteratorNode(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registerCollectIteratorItemNodeType(registry)

	def := GraphDefinition{
		EntryPoint:  "loop",
		FinishPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "payload.items",
					"max_iterations": 2,
					"continue_to":    "collect",
					"done_to":        dsl.EndNodeRef,
				},
			},
			{
				ID:   "collect",
				Type: "collect_iterator_item",
				Config: map[string]any{
					"iterator_node_id": "loop",
					"target_key":       "results",
				},
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "collect", To: "loop"},
		},
	}

	graph, err := registry.BuildGraph(def, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph with iterator: %v", err)
	}

	state, err := graph.Run(context.Background(), State{
		"payload": map[string]any{
			"items": []any{"alpha", "beta", "gamma"},
		},
	})
	if err != nil {
		t.Fatalf("run graph with iterator: %v", err)
	}

	results, ok := state["results"].([]string)
	if !ok {
		t.Fatalf("expected collected results slice, got %#v", state["results"])
	}
	if len(results) != 2 || results[0] != "alpha" || results[1] != "beta" {
		t.Fatalf("expected first two items to be collected, got %#v", results)
	}

	namespace := state.Namespace(nodes.IteratorStateNamespace)
	if namespace == nil {
		t.Fatalf("expected iterator namespace to be present")
	}
	iteratorState, ok := namespace["loop"].(map[string]any)
	if !ok {
		if typed, ok := namespace["loop"].(State); ok {
			iteratorState = typed
		} else {
			t.Fatalf("expected iterator state map, got %#v", namespace["loop"])
		}
	}
	if got := iteratorState["done"]; got != true {
		t.Fatalf("expected iterator to finish, got %#v", iteratorState)
	}
	if _, exists := iteratorState["item"]; exists {
		t.Fatalf("expected current item to be cleared after completion, got %#v", iteratorState)
	}
}

func TestBuildGraphDefinitionKeepsIteratorBuiltInEdgesInConfig(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint: "loop",
		Nodes: []GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "items",
					"max_iterations": 2,
					"continue_to":    "body",
					"done_to":        "after",
				},
			},
			{ID: "body", Type: "human_message"},
			{ID: "after", Type: "human_message"},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "body", To: "loop"},
		},
	}

	graph, err := registry.BuildGraph(def, &BuildContext{})
	if err != nil {
		t.Fatalf("build graph with iterator built-in edges: %v", err)
	}

	serialized, err := graph.Definition()
	if err != nil {
		t.Fatalf("serialize graph definition: %v", err)
	}

	if len(serialized.Edges) != 1 || serialized.Edges[0].From != "body" || serialized.Edges[0].To != "loop" {
		t.Fatalf("expected only explicit body->loop edge to be serialized, got %#v", serialized.Edges)
	}
	if len(serialized.Nodes) != 3 {
		t.Fatalf("expected 3 serialized nodes, got %d", len(serialized.Nodes))
	}

	var iteratorNode *GraphNodeSpec
	for i := range serialized.Nodes {
		if serialized.Nodes[i].ID == "loop" {
			iteratorNode = &serialized.Nodes[i]
			break
		}
	}
	if iteratorNode == nil {
		t.Fatalf("expected serialized iterator node")
	}
	if got := iteratorNode.Config["continue_to"]; got != "body" {
		t.Fatalf("expected continue_to to stay in iterator config, got %#v", iteratorNode.Config)
	}
	if got := iteratorNode.Config["done_to"]; got != "after" {
		t.Fatalf("expected done_to to stay in iterator config, got %#v", iteratorNode.Config)
	}
}
