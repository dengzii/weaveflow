package weaveflow_test

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func ExampleRegistry_RegisterNodeType() {
	reg := weaveflow.NewDefaultRegistry()
	_ = reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "set_answer", StatePorts: []dsl.StatePortDefinition{{Name: "output", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace}}},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (node.Node, error) {
			spec := resolved.Spec
			value, _ := spec.Config["value"].(string)
			output := resolved.State["output"].Path
			return node.NewFuncNode(node.Spec{ID: spec.ID}, func(_ core.Context, access *state.Access) (core.NodeResult, error) {
				return core.Success(), access.SetAny(output, value)
			}), nil
		},
	})

	graph, _ := weaveflow.BuildGraph(reg, dsl.GraphDefinition{
		Version: "2.0", StateModules: []dsl.StateModuleRef{{Name: "weaveflow.protocols", Version: "1"}},
		Nodes: []dsl.GraphNodeSpec{{
			ID:     "answer",
			Type:   "set_answer",
			Config: map[string]any{"value": "done"},
			State:  map[string]dsl.StateBinding{"output": {Path: "shared.answer"}},
		}},
		EntryPoint: "answer",
		Edges: []dsl.GraphEdgeSpec{{
			From: "answer",
			To:   weaveflow.EndNodeRef,
		}},
	})
	runner, _ := weaveflow.NewRunner(graph)
	_, finalState, _ := runner.Start(context.Background(), state.NewState())
	answer, _ := state.NewAccess(finalState).ReadAny(state.Shared("answer"))
	fmt.Println(answer)

	// Output:
	// done
}

func ExampleRegistry_RegisterCondition() {
	reg := weaveflow.NewDefaultRegistry()
	_ = reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "set_flag", StatePorts: []dsl.StatePortDefinition{{Name: "output", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace}}},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (node.Node, error) {
			spec := resolved.Spec
			output := resolved.State["output"].Path
			value := spec.Config["value"]
			return node.NewFuncNode(node.Spec{ID: spec.ID}, func(_ core.Context, access *state.Access) (core.NodeResult, error) {
				return core.Success(), access.SetAny(output, value)
			}), nil
		},
	})
	_ = reg.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{Type: "shared_equals", StatePorts: []dsl.StatePortDefinition{{Name: "value", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace}}},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			spec := resolved.Spec
			path := resolved.State["value"].Path
			want := spec.Config["value"]
			return registry.NewEdgeCondition(spec, func(_ context.Context, current *state.State) (bool, error) {
				got, ok := state.NewAccess(current).ReadAny(path)
				return ok && got == want, nil
			}), nil
		},
	})

	graph, _ := weaveflow.BuildGraph(reg, dsl.GraphDefinition{
		Version: "2.0", StateModules: []dsl.StateModuleRef{{Name: "weaveflow.protocols", Version: "1"}},
		Nodes: []dsl.GraphNodeSpec{
			{ID: "start", Type: "set_flag", Config: map[string]any{"value": "yes"}, State: map[string]dsl.StateBinding{"output": {Path: "shared.route"}}},
			{ID: "yes", Type: "set_flag", Config: map[string]any{"value": "matched"}, State: map[string]dsl.StateBinding{"output": {Path: "shared.answer"}}},
			{ID: "fallback", Type: "set_flag", Config: map[string]any{"value": "fallback"}, State: map[string]dsl.StateBinding{"output": {Path: "shared.answer"}}},
		},
		EntryPoint: "start",
		Edges: []dsl.GraphEdgeSpec{
			{
				From:      "start",
				To:        "yes",
				Condition: &dsl.GraphConditionSpec{Type: "shared_equals", Config: map[string]any{"value": "yes"}, State: map[string]dsl.StateBinding{"value": {Path: "shared.route"}}},
			},
			{From: "start", To: "fallback"},
			{From: "yes", To: weaveflow.EndNodeRef},
			{From: "fallback", To: weaveflow.EndNodeRef},
		},
	})
	runner, _ := weaveflow.NewRunner(graph)
	_, finalState, _ := runner.Start(context.Background(), state.NewState())
	answer, _ := state.NewAccess(finalState).ReadAny(state.Shared("answer"))
	fmt.Println(answer)

	// Output:
	// matched
}
