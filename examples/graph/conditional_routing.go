//go:build ignore

package main

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func main() {
	ctx := context.Background()
	componentRegistry := weaveflow.NewDefaultRegistry()
	registerRoutingTypes(componentRegistry)
	workflow := wfgraph.NewGraph(componentRegistry)

	addRoutingNode(workflow, dsl.GraphNodeSpec{ID: "classify", Type: "example_control", Name: "classify"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.Success(), nil
	})
	addRoutingNode(workflow, routingWriterSpec("priority"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("route"), "priority")
	})
	addRoutingNode(workflow, routingWriterSpec("standard"), func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("route"), "standard")
	})
	addRoutingNode(workflow, dsl.GraphNodeSpec{ID: "finish", Type: "example_control", Name: "finish"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.Success(), nil
	})

	priorityCondition := registry.NewEdgeCondition(
		dsl.GraphConditionSpec{
			Type:  "example_priority",
			State: map[string]dsl.StateBinding{"priority": {Path: "shared.request.priority"}},
		},
		func(_ context.Context, currentState *state.State) (registry.RouteDecision, error) {
			priority, _ := state.ReadPath(currentState, "shared.request.priority")
			matched := priority == "high"
			return registry.RouteDecision{
				Matched: matched,
				Reason:  fmt.Sprintf("request priority is %q", priority),
			}, nil
		},
	)

	mustRouting(workflow.SetEntryPoint("classify"))
	mustRouting(workflow.SetFinishPoint("finish"))
	mustRouting(workflow.AddConditionalEdge("classify", "priority", priorityCondition))
	mustRouting(workflow.AddEdge("classify", "standard"))
	mustRouting(workflow.AddEdge("priority", "finish"))
	mustRouting(workflow.AddEdge("standard", "finish"))

	runner, err := weaveflow.NewInMemoryRunner(workflow)
	mustRouting(err)
	for _, priority := range []string{"high", "normal"} {
		run, finalState, startErr := runner.Start(ctx, state.FromShared(map[string]any{
			"request": map[string]any{"priority": priority},
		}))
		mustRouting(startErr)

		selected, _ := state.ReadPath(finalState, "shared.route")
		fmt.Printf("run=%s status=%s priority=%s route=%v\n", run.RunID, run.Status, priority, selected)

		events, listErr := runner.ListEvents(run.RunID)
		mustRouting(listErr)
		for _, event := range events {
			if event.Type == runtime.EventConditionEvaluated {
				fmt.Printf("condition event: %s\n", event.Payload)
			}
		}
	}
}

func registerRoutingTypes(componentRegistry *registry.Registry) {
	mustRouting(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "example_control"},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
		},
	}))
	mustRouting(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "example_route_writer",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "output", Required: true, Schema: dsl.JSONSchema{"type": "string"},
				Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
		},
	}))
	mustRouting(componentRegistry.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type: "example_priority",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "priority", Required: true, Schema: dsl.JSONSchema{"type": "string"},
				Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Resolve: func(resolved registry.ResolvedConditionSpec) (registry.EdgeCondition, error) {
			return registry.EdgeCondition{}, fmt.Errorf("example condition %q must be constructed directly", resolved.Spec.Type)
		},
	}))
}

func routingWriterSpec(identifier string) dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID: identifier, Type: "example_route_writer", Name: identifier,
		State: map[string]dsl.StateBinding{"output": {Path: "shared.route"}},
	}
}

func addRoutingNode(workflow interface {
	AddNode(core.Node) error
	SetNodeSpec(dsl.GraphNodeSpec) error
}, spec dsl.GraphNodeSpec, execute node.ExecuteFunc) {
	mustRouting(workflow.AddNode(node.NewFuncNode(node.Spec{ID: spec.ID, Name: spec.Name}, execute)))
	mustRouting(workflow.SetNodeSpec(spec))
}

func mustRouting(err error) {
	if err != nil {
		panic(err)
	}
}
