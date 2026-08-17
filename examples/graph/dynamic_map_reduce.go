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
	registerMapReduceTypes(componentRegistry)
	workflow := wfgraph.NewGraph(componentRegistry)

	sends := []core.Send{
		{Target: "worker", Input: itemPatch("third", 3), CorrelationKey: "2", OrderKey: "b"},
		{Target: "worker", Input: itemPatch("second", 2), CorrelationKey: "2", OrderKey: "a"},
		{Target: "worker", Input: itemPatch("first", 1), CorrelationKey: "1", OrderKey: "a"},
	}
	addMapReduceNode(workflow, node.NewFuncNode(node.Spec{ID: "mapper", Name: "mapper"}, func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{Command: core.Command{Send: sends}}, nil
	}), dsl.GraphNodeSpec{
		ID: "mapper", Type: "example_control", Name: "mapper",
		State: map[string]dsl.StateBinding{
			"item":  {Path: "shared.item"},
			"value": {Path: "shared.value"},
		},
	})

	worker := node.NewFuncNode(node.Spec{ID: "worker", Name: "worker"}, func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		itemValue, _ := access.ReadAny(state.Shared("item"))
		numericValue, _ := access.ReadAny(state.Shared("value"))
		value, ok := numericValue.(int)
		if !ok {
			return core.NodeResult{}, fmt.Errorf("worker value has type %T", numericValue)
		}
		square := value * value
		return core.NodeResult{
			Patch: state.NewPatch(
				state.PatchOp{Kind: state.OpAppend, Path: state.Shared("results"), Value: fmt.Sprintf("%v=%d", itemValue, square)},
				state.PatchOp{Kind: state.OpReduce, Path: state.Shared("total"), Value: square, Reducer: "sum.v1"},
				state.PatchOp{Kind: state.OpReduce, Path: state.Shared("maximum"), Value: square, Reducer: "max.v1"},
			),
			Events: []core.EventDraft{{
				Type:    string(runtime.EventNodeCustom),
				Payload: map[string]any{"item": itemValue, "square": square},
			}},
			Artifacts: []core.ArtifactDraft{{
				Type:     "example.worker_result",
				MIMEType: "text/plain",
				Data:     []byte(fmt.Sprintf("%v=%d", itemValue, square)),
			}},
		}, nil
	})
	addMapReduceNode(workflow, worker, dsl.GraphNodeSpec{
		ID: "worker", Type: "example_reducing_worker", Name: "worker",
		State: map[string]dsl.StateBinding{
			"item":    {Path: "shared.item"},
			"value":   {Path: "shared.value"},
			"results": {Path: "shared.results"},
			"total":   {Path: "shared.total"},
			"maximum": {Path: "shared.maximum"},
		},
	})
	addMapReduceNode(workflow, node.NewFuncNode(node.Spec{ID: "collector", Name: "collector"}, func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		results, _ := access.ReadAny(state.Shared("results"))
		total, _ := access.ReadAny(state.Shared("total"))
		maximum, _ := access.ReadAny(state.Shared("maximum"))
		return core.Success(), access.SetAny(state.Shared("summary"), map[string]any{
			"results": results,
			"total":   total,
			"maximum": maximum,
		})
	}), dsl.GraphNodeSpec{
		ID: "collector", Type: "example_map_reduce_collector", Name: "collector",
		State: map[string]dsl.StateBinding{
			"results": {Path: "shared.results"},
			"total":   {Path: "shared.total"},
			"maximum": {Path: "shared.maximum"},
			"summary": {Path: "shared.summary"},
		},
	})

	mustMapReduce(workflow.SetEntryPoint("mapper"))
	mustMapReduce(workflow.SetFinishPoint("collector"))
	mustMapReduce(workflow.AddEdge("mapper", "worker"))
	mustMapReduce(workflow.AddEdge("worker", "collector"))

	runner, err := weaveflow.NewInMemoryRunner(workflow)
	mustMapReduce(err)
	run, finalState, err := runner.Start(ctx, state.NewState())
	mustMapReduce(err)

	summary, _ := state.ReadPath(finalState, "shared.summary")
	artifacts, err := runner.ListArtifacts(ctx, run.RunID)
	mustMapReduce(err)
	events, err := runner.ListEvents(run.RunID)
	mustMapReduce(err)
	customEventCount := 0
	for _, event := range events {
		if event.Type == runtime.EventNodeCustom {
			customEventCount++
		}
	}
	workerArtifacts := artifacts[:0]
	for _, artifact := range artifacts {
		if artifact.Type == "example.worker_result" {
			workerArtifacts = append(workerArtifacts, artifact)
		}
	}

	fmt.Printf("run=%s status=%s\n", run.RunID, run.Status)
	fmt.Printf("summary=%#v\n", summary)
	fmt.Printf("custom_events=%d worker_artifacts=%d\n", customEventCount, len(workerArtifacts))
	if len(workerArtifacts) > 0 {
		artifact, loadErr := runner.LoadArtifact(ctx, workerArtifacts[0])
		mustMapReduce(loadErr)
		fmt.Printf("first_artifact=%s\n", artifact.Data)
	}
}

func registerMapReduceTypes(componentRegistry *registry.Registry) {
	directBuild := func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
		return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
	}
	mustMapReduce(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "example_control",
			StatePorts: []dsl.StatePortDefinition{
				{Name: "item", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace},
				{Name: "value", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace},
			},
		},
		Build: directBuild,
	}))
	mustMapReduce(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "example_reducing_worker",
			StatePorts: []dsl.StatePortDefinition{
				{Name: "item", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "value", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "results", Required: true, Schema: dsl.JSONSchema{"type": "array", "items": map[string]any{"type": "string"}}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeAppend},
				{Name: "total", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace, Reducer: "sum.v1"},
				{Name: "maximum", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace, Reducer: "max.v1"},
			},
		},
		Build: directBuild,
	}))
	mustMapReduce(componentRegistry.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "example_map_reduce_collector",
			StatePorts: []dsl.StatePortDefinition{
				{Name: "results", Required: true, Schema: dsl.JSONSchema{"type": "array", "items": map[string]any{"type": "string"}}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "total", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "maximum", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "summary", Required: true, Schema: dsl.JSONSchema{"type": "object"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace},
			},
		},
		Build: directBuild,
	}))
}

func itemPatch(item string, value int) state.Patch {
	return state.NewPatch(
		state.PatchOp{Kind: state.OpSet, Path: state.Shared("item"), Value: item},
		state.PatchOp{Kind: state.OpSet, Path: state.Shared("value"), Value: value},
	)
}

func addMapReduceNode(workflow interface {
	AddNode(core.Node) error
	SetNodeSpec(dsl.GraphNodeSpec) error
}, target core.Node, spec dsl.GraphNodeSpec) {
	mustMapReduce(workflow.AddNode(target))
	mustMapReduce(workflow.SetNodeSpec(spec))
}

func mustMapReduce(err error) {
	if err != nil {
		panic(err)
	}
}
