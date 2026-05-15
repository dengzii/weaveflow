package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"weaveflow"
	"weaveflow/builder"
	"weaveflow/builtin"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

func IteratorExample() {
	reg := builtin.NewDefaultRegistry()
	registerCollectIteratorItemNodeType(reg)
	registerPrintStateNodeType(reg)

	definition := dsl.GraphDefinition{
		Name:        "iterator-demo",
		Description: "Demonstrate iterator built-in continue/done edges.",
		EntryPoint:  "loop",
		FinishPoint: "done",
		Nodes: []dsl.GraphNodeSpec{
			{
				ID:   "loop",
				Name: "Iterate Payload Items",
				Type: "iterator",
				Config: map[string]any{
					"state_key":      "payload.items",
					"max_iterations": 3,
					"continue_to":    "collect",
					"done_to":        "done",
				},
			},
			{
				ID:   "collect",
				Name: "Collect Current Item",
				Type: "collect_iterator_item",
				Config: map[string]any{
					"iterator_node_id": "loop",
					"target_key":       "results",
				},
			},
			{
				ID:   "done",
				Name: "Print Final State",
				Type: "print_state",
			},
		},
		Edges: []dsl.GraphEdgeSpec{
			{From: "collect", To: "loop"},
		},
	}

	graph, err := weaveflow.BuildGraph(reg, definition, &builder.BuildContext{})
	must(err)

	state, err := graph.Run(context.Background(), wfstate.State{
		"payload": map[string]any{
			"items": []any{"alpha", "beta", "gamma", "delta"},
		},
	})
	must(err)

	fmt.Println()
	fmt.Println("final state snapshot:")
	printJSON(state)
}

type collectIteratorItemNode struct {
	id             string
	iteratorNodeID string
	targetKey      string
}

func (n collectIteratorItemNode) ID() string   { return n.id }
func (n collectIteratorItemNode) Name() string { return n.id }
func (n collectIteratorItemNode) Description() string {
	return "append current iterator item into a string slice"
}
func (n collectIteratorItemNode) Execute(ctx context.Context, state wfstate.State) (wfstate.StatePatch, error) {
	_ = ctx

	if state == nil {
		return wfstate.StatePatch{}, nil
	}

	namespace := state.Namespace(nodes.IteratorStateNamespace)
	if namespace == nil {
		return wfstate.StatePatch{}, nil
	}
	iteratorState := nestedState(namespace, n.iteratorNodeID)
	if iteratorState == nil {
		return wfstate.StatePatch{}, nil
	}

	item, _ := iteratorState["item"].(string)
	if item == "" {
		return wfstate.StatePatch{}, nil
	}

	fmt.Printf("collect: index=%v iteration=%v item=%q\n", iteratorState["index"], iteratorState["iteration"], item)
	return wfstate.StatePatch{
		n.targetKey: []string{item},
	}, nil
}

type printStateNode struct {
	id string
}

func (n printStateNode) ID() string          { return n.id }
func (n printStateNode) Name() string        { return n.id }
func (n printStateNode) Description() string { return "print a compact summary of the final state" }
func (n printStateNode) Execute(ctx context.Context, state wfstate.State) (wfstate.StatePatch, error) {
	_ = ctx

	results, _ := state["results"].([]string)
	iteratorState := nestedState(state.Namespace(nodes.IteratorStateNamespace), "loop")

	fmt.Println("iterator done:", iteratorState["done"])
	fmt.Println("results:", results)
	return wfstate.StatePatch{}, nil
}

func registerCollectIteratorItemNodeType(r *registry.Registry) {
	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "collect_iterator_item",
			Title:       "Collect Iterator Item",
			Description: "Append the current iterator item into a target string slice.",
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
			iteratorNodeID := registry.StringConfig(spec.Config, "iterator_node_id")
			targetKey := registry.StringConfig(spec.Config, "target_key")
			fields := make([]dsl.StateFieldRef, 0, 4)
			if iteratorNodeID != "" {
				prefix := nodes.IteratorStateRootKey + "." + iteratorNodeID
				fields = append(fields,
					dsl.StateFieldRef{Path: prefix + ".item", Mode: dsl.StateAccessRead},
					dsl.StateFieldRef{Path: prefix + ".index", Mode: dsl.StateAccessRead},
					dsl.StateFieldRef{Path: prefix + ".iteration", Mode: dsl.StateAccessRead},
				)
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
				iteratorNodeID: registry.StringConfig(spec.Config, "iterator_node_id"),
				targetKey:      registry.StringConfig(spec.Config, "target_key"),
			}, nil
		}),
	})
}

func registerPrintStateNodeType(r *registry.Registry) {
	r.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "print_state",
			Title:       "Print State",
			Description: "Print a compact summary of the final state.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"additionalProperties": false,
			},
			StateContract: &dsl.StateContract{
				Fields: []dsl.StateFieldRef{
					{Path: "results", Mode: dsl.StateAccessRead},
					{Path: nodes.IteratorStateRootKey + ".loop.done", Mode: dsl.StateAccessRead},
				},
			},
		},
		Build: builder.AdaptNodeBuilder(func(ctx *builder.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node, error) {
			_ = ctx
			return printStateNode{id: spec.ID}, nil
		}),
	})
}

func nestedState(values map[string]any, key string) wfstate.State {
	if values == nil {
		return nil
	}
	switch typed := values[key].(type) {
	case wfstate.State:
		return typed
	case map[string]any:
		return wfstate.State(typed)
	default:
		return nil
	}
}

func printJSON(value any) {
	normalized := sortStateKeys(value)
	data, err := json.MarshalIndent(normalized, "", "  ")
	must(err)
	fmt.Println(string(data))
}

func sortStateKeys(value any) any {
	switch typed := value.(type) {
	case wfstate.State:
		return sortStateKeys(map[string]any(typed))
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		result := make(map[string]any, len(typed))
		for _, key := range keys {
			result[key] = sortStateKeys(typed[key])
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = sortStateKeys(item)
		}
		return result
	default:
		return value
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
