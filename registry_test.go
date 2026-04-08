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
