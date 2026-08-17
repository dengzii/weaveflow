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
	registerFallbackType(componentRegistry)
	workflow := wfgraph.NewGraph(componentRegistry)

	addFallbackNode(workflow, dsl.GraphNodeSpec{ID: "primary", Type: "example_control", Name: "primary"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{}, core.NewExecutionError(
			core.ErrorUnavailable,
			"primary provider is unavailable",
			nil,
			map[string]any{"provider": "primary"},
		)
	})
	addFallbackNode(workflow, dsl.GraphNodeSpec{
		ID: "fallback", Type: "example_fallback_writer", Name: "fallback",
		State: map[string]dsl.StateBinding{"output": {Path: "shared.fallback"}},
	}, func(ctx core.Context, access *state.Access) (core.NodeResult, error) {
		failure, ok := ctx.Failure()
		if !ok {
			return core.NodeResult{}, fmt.Errorf("failure context is missing")
		}
		return core.NodeResult{
				Command: core.Command{Return: &core.ReturnCommand{Value: map[string]any{
					"handled":     true,
					"source_node": failure.SourceNodeID,
					"error_class": failure.ErrorClass,
				}}},
			}, access.SetAny(state.Shared("fallback"), map[string]any{
				"handled":     true,
				"source_node": failure.SourceNodeID,
				"error_class": failure.ErrorClass,
				"details":     failure.Details,
			})
	})

	mustFallback(workflow.SetEntryPoint("primary"))
	mustFallback(workflow.SetFinishPoint("fallback"))
	mustFallback(workflow.AddFailureRoute("primary", "fallback", dsl.FailureRouteSpec{
		Stages:       []dsl.FailureStage{dsl.FailureStageNode},
		ErrorClasses: []string{string(core.ErrorUnavailable)},
	}))
	mustFallback(workflow.AddEdge("primary", weaveflow.EndNodeRef))

	runner, err := weaveflow.NewInMemoryRunner(workflow)
	mustFallback(err)
	run, finalState, err := runner.Start(ctx, state.NewState())
	mustFallback(err)

	fallback, _ := state.ReadPath(finalState, "shared.fallback")
	steps, err := runner.ListSteps(ctx, run.RunID)
	mustFallback(err)
	events, err := runner.ListEvents(run.RunID)
	mustFallback(err)
	failureRouted := false
	for _, event := range events {
		failureRouted = failureRouted || event.Type == runtime.EventFailureRouted
	}

	fmt.Printf("run=%s status=%s return=%#v\n", run.RunID, run.Status, run.ReturnValue)
	fmt.Printf("fallback=%#v failure_routed=%t\n", fallback, failureRouted)
	for _, step := range steps {
		fmt.Printf("step node=%s status=%s class=%s\n", step.NodeID, step.Status, step.ErrorCode)
	}
}

func registerFallbackType(componentRegistry *registry.Registry) {
	mustFallback(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "example_control"},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
		},
	}))
	mustFallback(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "example_fallback_writer",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "output", Required: true, Schema: dsl.JSONSchema{"type": "object"},
				Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
		},
	}))
}

func addFallbackNode(workflow interface {
	AddNode(core.Node) error
	SetNodeSpec(dsl.GraphNodeSpec) error
}, spec dsl.GraphNodeSpec, execute node.ExecuteFunc) {
	mustFallback(workflow.AddNode(node.NewFuncNode(node.Spec{ID: spec.ID, Name: spec.Name}, execute)))
	mustFallback(workflow.SetNodeSpec(spec))
}

func mustFallback(err error) {
	if err != nil {
		panic(err)
	}
}
