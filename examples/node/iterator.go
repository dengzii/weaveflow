package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"weaveflow"
	"weaveflow/dsl"
	"weaveflow/nodes"
)

func IteratorExample() {
	registry := weaveflow.DefaultRegistry()
	registerCollectIteratorItemNodeType(registry)
	registerPrintStateNodeType(registry)

	definition := weaveflow.GraphDefinition{
		Name:        "iterator-demo",
		Description: "Demonstrate iterator built-in continue/done edges.",
		EntryPoint:  "loop",
		FinishPoint: "done",
		Nodes: []weaveflow.GraphNodeSpec{
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

	graph, err := registry.BuildGraph(definition, &weaveflow.BuildContext{})
	must(err)

	state, err := graph.Run(context.Background(), weaveflow.State{
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
func (n collectIteratorItemNode) Invoke(ctx context.Context, state weaveflow.State) (weaveflow.State, error) {
	_ = ctx

	if state == nil {
		state = weaveflow.State{}
	}

	namespace := state.Namespace(nodes.IteratorStateNamespace)
	if namespace == nil {
		return state, nil
	}
	iteratorState := nestedState(namespace, n.iteratorNodeID)
	if iteratorState == nil {
		return state, nil
	}

	item, _ := iteratorState["item"].(string)
	if item == "" {
		return state, nil
	}

	results, _ := state[n.targetKey].([]string)
	results = append(results, item)
	state[n.targetKey] = results

	fmt.Printf("collect: index=%v iteration=%v item=%q\n", iteratorState["index"], iteratorState["iteration"], item)
	return state, nil
}

type printStateNode struct {
	id string
}

func (n printStateNode) ID() string          { return n.id }
func (n printStateNode) Name() string        { return n.id }
func (n printStateNode) Description() string { return "print a compact summary of the final state" }
func (n printStateNode) Invoke(ctx context.Context, state weaveflow.State) (weaveflow.State, error) {
	_ = ctx

	results, _ := state["results"].([]string)
	iteratorState := nestedState(state.Namespace(nodes.IteratorStateNamespace), "loop")

	fmt.Println("iterator done:", iteratorState["done"])
	fmt.Println("results:", results)
	return state, nil
}

func registerCollectIteratorItemNodeType(registry *weaveflow.Registry) {
	registry.RegisterNodeType(weaveflow.NodeTypeDefinition{
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
		Build: func(ctx *weaveflow.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[weaveflow.State], error) {
			_ = ctx
			return collectIteratorItemNode{
				id:             spec.ID,
				iteratorNodeID: stringConfig(spec.Config, "iterator_node_id"),
				targetKey:      stringConfig(spec.Config, "target_key"),
			}, nil
		},
	})
}

func registerPrintStateNodeType(registry *weaveflow.Registry) {
	registry.RegisterNodeType(weaveflow.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "print_state",
			Title:       "Print State",
			Description: "Print a compact summary of the final state.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		Build: func(ctx *weaveflow.BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[weaveflow.State], error) {
			_ = ctx
			return printStateNode{id: spec.ID}, nil
		},
	})
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, _ := config[key].(string)
	return value
}

func nestedState(values map[string]any, key string) weaveflow.State {
	if values == nil {
		return nil
	}
	switch typed := values[key].(type) {
	case weaveflow.State:
		return typed
	case map[string]any:
		return weaveflow.State(typed)
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
	case weaveflow.State:
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
