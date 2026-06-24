package weaveflow_test

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/state"
)

func ExampleRegistry_RegisterNodeType() {
	reg := weaveflow.NewDefaultRegistry()
	_ = reg.RegisterNodeType(weaveflow.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "set_answer"},
		ResolveStateContract: func(dsl.GraphNodeSpec) (dsl.StateContract, error) {
			return dsl.StateContract{
				Fields: []dsl.StateFieldRef{{Path: "shared.answer", Mode: dsl.StateAccessWrite}},
			}, nil
		},
		Build: func(_ *weaveflow.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			value, _ := spec.Config["value"].(string)
			return node.NewFuncNode(node.Spec{ID: spec.ID}, func(_ core.Context, access *state.Access) error {
				return access.SetAny(state.Shared("answer"), value)
			}), nil
		},
	})

	graph, _ := weaveflow.BuildGraph(reg, weaveflow.GraphDefinition{
		Nodes: []weaveflow.GraphNodeSpec{{
			ID:     "answer",
			Type:   "set_answer",
			Config: map[string]any{"value": "done"},
		}},
		EntryPoint: "answer",
		Edges: []weaveflow.GraphEdgeSpec{{
			From: "answer",
			To:   weaveflow.EndNodeRef,
		}},
	})
	runner, _ := weaveflow.NewRunner(graph)
	_, finalState, _ := runner.Start(context.Background(), weaveflow.NewState())
	answer, _ := state.NewAccess(nil, finalState).ReadAny(state.Shared("answer"))
	fmt.Println(answer)

	// Output:
	// done
}

func ExampleRegistry_RegisterCondition() {
	reg := weaveflow.NewDefaultRegistry()
	_ = reg.RegisterNodeType(weaveflow.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "set_flag"},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			key, _ := spec.Config["key"].(string)
			return dsl.StateContract{
				Fields: []dsl.StateFieldRef{{Path: "shared." + key, Mode: dsl.StateAccessWrite}},
			}, nil
		},
		Build: func(_ *weaveflow.BuildContext, spec dsl.GraphNodeSpec) (node.Node, error) {
			key, _ := spec.Config["key"].(string)
			value := spec.Config["value"]
			return node.NewFuncNode(node.Spec{ID: spec.ID}, func(_ core.Context, access *state.Access) error {
				return access.SetAny(state.Shared(key), value)
			}), nil
		},
	})
	_ = reg.RegisterCondition(weaveflow.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{Type: "shared_equals"},
		Resolve: func(spec dsl.GraphConditionSpec) (weaveflow.EdgeCondition, error) {
			key, _ := spec.Config["key"].(string)
			want := spec.Config["value"]
			return weaveflow.NewEdgeCondition(spec, func(_ context.Context, current *state.State) bool {
				got, ok := state.NewAccess(nil, current).ReadAny(state.Shared(key))
				return ok && got == want
			}), nil
		},
	})

	graph, _ := weaveflow.BuildGraph(reg, weaveflow.GraphDefinition{
		Nodes: []weaveflow.GraphNodeSpec{
			{ID: "start", Type: "set_flag", Config: map[string]any{"key": "route", "value": "yes"}},
			{ID: "yes", Type: "set_flag", Config: map[string]any{"key": "answer", "value": "matched"}},
			{ID: "fallback", Type: "set_flag", Config: map[string]any{"key": "answer", "value": "fallback"}},
		},
		EntryPoint: "start",
		Edges: []weaveflow.GraphEdgeSpec{
			{
				From:      "start",
				To:        "yes",
				Condition: &weaveflow.GraphConditionSpec{Type: "shared_equals", Config: map[string]any{"key": "route", "value": "yes"}},
			},
			{From: "start", To: "fallback"},
			{From: "yes", To: weaveflow.EndNodeRef},
			{From: "fallback", To: weaveflow.EndNodeRef},
		},
	})
	runner, _ := weaveflow.NewRunner(graph)
	_, finalState, _ := runner.Start(context.Background(), weaveflow.NewState())
	answer, _ := state.NewAccess(nil, finalState).ReadAny(state.Shared("answer"))
	fmt.Println(answer)

	// Output:
	// matched
}
