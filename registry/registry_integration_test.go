package registry_test

import (
	"context"
	"strings"
	"testing"
	"weaveflow"
	"weaveflow/builder"
	"weaveflow/builtin"
	"weaveflow/dsl"
	"weaveflow/nodes"
	wfregistry "weaveflow/registry"
	wfstate "weaveflow/state"
)

type assignStateNode struct {
	id    string
	key   string
	value any
}

func (n assignStateNode) ID() string          { return n.id }
func (n assignStateNode) Name() string        { return n.id }
func (n assignStateNode) Description() string { return "assign state value" }
func (n assignStateNode) Execute(ctx context.Context, state wfstate.State) (wfstate.StatePatch, error) {
	_ = ctx
	_ = state
	if n.key == "" {
		return wfstate.StatePatch{}, nil
	}
	return wfstate.StatePatch{n.key: n.value}, nil
}

func registerAssignNodeType(registry *wfregistry.Registry) {
	registry.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "assign",
			Description: "Assign a value into shared state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"key":   dsl.JSONSchema{"type": "string"},
					"value": dsl.JSONSchema{},
				},
				"required":             []string{"key"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			key := wfregistry.StringConfig(spec.Config, "key")
			if key == "" {
				return dsl.StateContract{}, nil
			}
			return dsl.StateContract{
				Fields: []dsl.StateFieldRef{{
					Path: key,
					Mode: dsl.StateAccessWrite,
				}},
			}, nil
		},
		Build: builder.AdaptNodeBuilder(func(ctx *builder.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node, error) {
			return assignStateNode{
				id:    spec.ID,
				key:   wfregistry.StringConfig(spec.Config, "key"),
				value: spec.Config["value"],
			}, nil
		}),
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
func (n collectIteratorItemNode) Execute(ctx context.Context, state wfstate.State) (wfstate.StatePatch, error) {
	_ = ctx

	if state == nil {
		return wfstate.StatePatch{}, nil
	}

	namespace := state.Namespace(nodes.IteratorStateNamespace)
	if namespace == nil {
		return wfstate.StatePatch{}, nil
	}
	rawIteratorState, ok := namespace[n.iteratorNodeID]
	if !ok {
		return wfstate.StatePatch{}, nil
	}

	iteratorState, ok := rawIteratorState.(map[string]any)
	if !ok {
		if typed, ok := rawIteratorState.(wfstate.State); ok {
			iteratorState = typed
		} else {
			return wfstate.StatePatch{}, nil
		}
	}

	item, ok := iteratorState["item"].(string)
	if !ok || item == "" {
		return wfstate.StatePatch{}, nil
	}

	return wfstate.StatePatch{
		n.targetKey: []string{item},
	}, nil
}

func registerCollectIteratorItemNodeType(registry *wfregistry.Registry) {
	registry.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "collect_iterator_item",
			Description: "Collect the current iterator item into a string slice.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"iterator_node_id": dsl.JSONSchema{"type": "string"},
					"target_key":       dsl.JSONSchema{"type": "string"},
				},
				"required":             []string{"iterator_node_id", "target_key"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			iteratorNodeID := wfregistry.StringConfig(spec.Config, "iterator_node_id")
			targetKey := wfregistry.StringConfig(spec.Config, "target_key")
			fields := make([]dsl.StateFieldRef, 0, 2)
			if iteratorNodeID != "" {
				fields = append(fields, dsl.StateFieldRef{
					Path: nodes.IteratorStateRootKey + "." + iteratorNodeID + ".item",
					Mode: dsl.StateAccessRead,
				})
			}
			if targetKey != "" {
				fields = append(fields, dsl.StateFieldRef{
					Path:          targetKey,
					Mode:          dsl.StateAccessReadWrite,
					MergeStrategy: dsl.StateMergeAppend,
				})
			}
			return dsl.StateContract{Fields: fields}, nil
		},
		Build: builder.AdaptNodeBuilder(func(ctx *builder.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node, error) {
			_ = ctx
			return collectIteratorItemNode{
				id:             spec.ID,
				iteratorNodeID: wfregistry.StringConfig(spec.Config, "iterator_node_id"),
				targetKey:      wfregistry.StringConfig(spec.Config, "target_key"),
			}, nil
		}),
	})
}

func registerNoContractNodeType(registry *wfregistry.Registry) {
	registry.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "no_contract",
			Description: "Test node with no declared state contract.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		Build: builder.AdaptNodeBuilder(func(ctx *builder.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node, error) {
			_ = ctx
			return assignStateNode{id: spec.ID}, nil
		}),
	})
}

func TestBuildGraphRequiresEntryPoint(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		FinishPoint: "tools",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "tools", Type: "tools"},
		},
	}

	_, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "entry point") {
		t.Fatalf("expected missing entry point error, got %v", err)
	}
}

func TestBuildGraphRejectsRemovedSubgraphNode(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "sub",
				Type: "subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}

	_, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), `node type "subgraph" is not registered`) {
		t.Fatalf("expected removed subgraph error, got %v", err)
	}
}

func TestBuildGraphRejectsNodeTypeWithoutExplicitStateContract(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerNoContractNodeType(registry)

	_, err := weaveflow.BuildGraph(registry, dsl.GraphDefinition{
		EntryPoint:  "noop",
		FinishPoint: "noop",
		Nodes: []dsl.GraphNodeSpec{
			{ID: "noop", Type: "no_contract"},
		},
	}, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "must declare a state contract") {
		t.Fatalf("expected missing state contract error, got %v", err)
	}
}

func TestBuildGraphRequiresIteratorConfig(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		EntryPoint:  "loop",
		FinishPoint: "loop",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key": "items",
				},
			},
		},
	}

	_, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("expected missing max_iterations error, got %v", err)
	}
}

func TestBuildGraphRejectsPartialIteratorBuiltInEdges(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		EntryPoint: "loop",
		Nodes: []dsl.GraphNodeSpec{
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

	_, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "continue_to and done_to") {
		t.Fatalf("expected partial built-in edge config error, got %v", err)
	}
}

func TestBuildGraphRejectsIteratorBuiltInEdgesWithExplicitOutgoingEdge(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		EntryPoint: "loop",
		Nodes: []dsl.GraphNodeSpec{
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

	_, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with explicit outgoing edges") {
		t.Fatalf("expected mixed outgoing edge error, got %v", err)
	}
}

func TestBuildGraphRequiresGraphResolverForMappedSubgraph(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "sub",
				Type: "mapped_subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}

	_, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "graph resolver") {
		t.Fatalf("expected missing graph resolver error, got %v", err)
	}
}

func TestBuildGraphInvokesMappedSubgraphByGraphRef(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	registerAssignNodeType(registry)

	root := dsl.GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "sub",
				Type: "mapped_subgraph",
				Config: map[string]any{
					"graph_ref":  "child",
					"output_map": map[string]any{"answer": "answer"},
				},
			},
		},
	}
	child := dsl.GraphDefinition{
		EntryPoint:  "set",
		FinishPoint: "set",
		Nodes: []dsl.GraphNodeSpec{
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

	graph, err := weaveflow.BuildGraph(registry, root, &builder.BuildContext{
		GraphResolver: func(graphRef string) (dsl.GraphDefinition, error) {
			if graphRef != "child" {
				t.Fatalf("unexpected graph_ref %q", graphRef)
			}
			return child, nil
		},
	})
	if err != nil {
		t.Fatalf("build graph with mapped subgraph: %v", err)
	}

	state, err := graph.Run(context.Background(), wfstate.State{})
	if err != nil {
		t.Fatalf("run graph with mapped subgraph: %v", err)
	}
	if got := state["answer"]; got != "ok" {
		t.Fatalf("expected mapped subgraph to update state, got %#v", state)
	}
}

func TestBuildGraphRejectsCyclicMappedSubgraphRefs(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	root := dsl.GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "sub",
				Type: "mapped_subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}
	child := dsl.GraphDefinition{
		EntryPoint:  "sub",
		FinishPoint: "sub",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "sub",
				Type: "mapped_subgraph",
				Config: map[string]any{
					"graph_ref": "child",
				},
			},
		},
	}

	_, err := weaveflow.BuildGraph(registry, root, &builder.BuildContext{
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

	registry := builtin.NewDefaultRegistry()
	registerCollectIteratorItemNodeType(registry)

	def := dsl.GraphDefinition{
		EntryPoint:  "loop",
		FinishPoint: "loop",
		Nodes: []dsl.GraphNodeSpec{
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

	graph, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
	if err != nil {
		t.Fatalf("build graph with iterator: %v", err)
	}

	state, err := graph.Run(context.Background(), wfstate.State{
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
		if typed, ok := namespace["loop"].(wfstate.State); ok {
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

	registry := builtin.NewDefaultRegistry()
	def := dsl.GraphDefinition{
		EntryPoint: "loop",
		Nodes: []dsl.GraphNodeSpec{
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

	graph, err := weaveflow.BuildGraph(registry, def, &builder.BuildContext{})
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

	var iteratorNode *dsl.GraphNodeSpec
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

func TestResolveDefaultNodeStateContracts(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()

	cases := []struct {
		name  string
		spec  dsl.GraphNodeSpec
		paths []string
		modes []dsl.StateAccessMode
	}{
		{
			name: "mapped_subgraph",
			spec: dsl.GraphNodeSpec{
				ID:   "sub",
				Type: "mapped_subgraph",
				Config: map[string]any{
					"graph_ref":  "child",
					"input_map":  map[string]any{"request.input": "request.input"},
					"output_map": map[string]any{"result.answer": "answer"},
				},
			},
			paths: []string{"shared.request.input", "shared.answer"},
			modes: []dsl.StateAccessMode{dsl.StateAccessRead, dsl.StateAccessWrite},
		},
		{
			name: "iterator",
			spec: dsl.GraphNodeSpec{
				ID:   "loop",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "payload.items",
					"max_iterations": 2,
				},
			},
			paths: []string{"shared.payload.items", nodes.IteratorStateRootKey + ".loop"},
			modes: []dsl.StateAccessMode{dsl.StateAccessRead, dsl.StateAccessReadWrite},
		},
		{
			name: "memory_recall",
			spec: dsl.GraphNodeSpec{
				ID:   "recall",
				Type: "memory_recall",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{
				"shared.memory",
				"shared.request.input",
				"shared.orchestration",
				"scopes.agent.messages",
			},
			modes: []dsl.StateAccessMode{
				dsl.StateAccessWrite,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
			},
		},
		{
			name: "memory_write",
			spec: dsl.GraphNodeSpec{
				ID:   "write_memory",
				Type: "memory_write",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{
				"shared.request.input",
				"scopes.agent.final_answer",
				"shared.planner",
				"shared.memory",
			},
			modes: []dsl.StateAccessMode{
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessWrite,
			},
		},
		{
			name: "verifier",
			spec: dsl.GraphNodeSpec{
				ID:   "verify",
				Type: "verifier",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{
				"shared.planner.plan",
				"shared.planner.current_step_id",
				"shared.planner.objective",
				"shared.execution.route",
				"shared.execution.step_results",
				"shared.request.input",
				"shared.observations",
				"shared.evidence",
				"scopes.agent.messages",
				"scopes.agent.final_answer",
				"shared.verification",
				"shared.token_usage",
			},
			modes: []dsl.StateAccessMode{
				dsl.StateAccessReadWrite,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessRead,
				dsl.StateAccessWrite,
				dsl.StateAccessWrite,
			},
		},
		{
			name: "human_message",
			spec: dsl.GraphNodeSpec{
				ID:   "ask",
				Type: "human_message",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{"scopes.agent.messages", "scopes.agent." + nodes.PendingHumanInputStateKey},
			modes: []dsl.StateAccessMode{dsl.StateAccessReadWrite, dsl.StateAccessReadWrite},
		},
		{
			name: "context_reducer",
			spec: dsl.GraphNodeSpec{
				ID:   "reduce",
				Type: "context_reducer",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{"scopes.agent.messages"},
			modes: []dsl.StateAccessMode{dsl.StateAccessReadWrite},
		},
		{
			name: "llm",
			spec: dsl.GraphNodeSpec{
				ID:   "model",
				Type: "llm",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{
				"scopes.agent.messages",
				"scopes.agent.iteration_count",
				"scopes.agent.max_iterations",
				"scopes.agent.final_answer",
				"shared." + nodes.TokenUsageStateKey,
			},
			modes: []dsl.StateAccessMode{
				dsl.StateAccessReadWrite,
				dsl.StateAccessReadWrite,
				dsl.StateAccessRead,
				dsl.StateAccessWrite,
				dsl.StateAccessWrite,
			},
		},
		{
			name: "tools",
			spec: dsl.GraphNodeSpec{
				ID:   "call_tools",
				Type: "tools",
				Config: map[string]any{
					"state_scope": "agent",
				},
			},
			paths: []string{"scopes.agent.messages"},
			modes: []dsl.StateAccessMode{dsl.StateAccessReadWrite},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			contract, err := registry.ResolveNodeStateContract(tc.spec)
			if err != nil {
				t.Fatalf("resolve state contract: %v", err)
			}
			if len(contract.Fields) != len(tc.paths) {
				t.Fatalf("expected %d fields, got %#v", len(tc.paths), contract.Fields)
			}
			for i, field := range contract.Fields {
				if field.Path != tc.paths[i] {
					t.Fatalf("field %d path: expected %q, got %#v", i, tc.paths[i], field)
				}
				if field.Mode != tc.modes[i] {
					t.Fatalf("field %d mode: expected %q, got %#v", i, tc.modes[i], field)
				}
			}
		})
	}
}

func TestResolveSubgraphStateContractMissingRegistration(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	_, err := registry.ResolveNodeStateContract(dsl.GraphNodeSpec{
		ID:   "sub",
		Type: "subgraph",
		Config: map[string]any{
			"graph_ref": "child",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `node type "subgraph" is not registered`) {
		t.Fatalf("expected removed subgraph error, got %v", err)
	}
}

func TestResolveHumanMessageDefaultScopeUsesConversationAndSharedPartitions(t *testing.T) {
	t.Parallel()

	registry := builtin.NewDefaultRegistry()
	contract, err := registry.ResolveNodeStateContract(dsl.GraphNodeSpec{
		ID:   "ask",
		Type: "human_message",
	})
	if err != nil {
		t.Fatalf("resolve human_message state contract: %v", err)
	}
	if len(contract.Fields) != 2 {
		t.Fatalf("expected 2 contract fields, got %#v", contract.Fields)
	}
	if contract.Fields[0].Path != "conversation.messages" {
		t.Fatalf("expected default conversation path, got %#v", contract.Fields[0])
	}
	if contract.Fields[1].Path != "shared."+nodes.PendingHumanInputStateKey {
		t.Fatalf("expected shared pending input path, got %#v", contract.Fields[1])
	}
}
