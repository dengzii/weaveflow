//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	graph := newFanInFanOutGraph()

	fmt.Println("== Graph.Run ==")
	finalState, err := graph.Run(ctx, state.NewState())
	must(err)
	printResult(finalState)

	fmt.Println("== GraphRunner barrier resume ==")
	baseDir := filepath.Join(".local", "fan-in-fan-out")
	must(os.RemoveAll(baseDir))
	must(os.MkdirAll(baseDir, 0o755))
	must(graph.WriteToFile(filepath.Join(baseDir, "graph.json")))

	runner, err := weaveflow.NewLocalRunner(graph, baseDir)
	must(err)
	defer func() { _ = runner.Close() }()
	run, _, err := runner.Start(ctx, state.NewState())
	must(err)

	barrierID := findBarrierCheckpoint(ctx, runner, run.RunID)
	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(ctx, barrierID, nil)
	must(err)
	fmt.Printf("resumed status: %s\n", resumedRun.Status)
	printResult(resumedState)
}

func newFanInFanOutGraph() *wfgraph.Graph {
	componentRegistry := weaveflow.NewDefaultRegistry()
	registerFanInFanOutTypes(componentRegistry)
	workflow := wfgraph.NewGraph(componentRegistry)
	addFuncNode(workflow, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	addFuncNode(workflow, "research", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "research")
	})
	addFuncNode(workflow, "draft", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "draft")
	})
	addFuncNode(workflow, "collector", func(ctx context.Context, access *state.Access) error {
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})

	must(workflow.SetEntryPoint("router"))
	must(workflow.SetFinishPoint("collector"))
	must(workflow.AddEdge("router", "research"))
	must(workflow.AddEdge("router", "draft"))
	must(workflow.AddEdge("research", "collector"))
	must(workflow.AddEdge("draft", "collector"))
	return workflow
}

func addFuncNode(workflow *wfgraph.Graph, identifier string, execute func(context.Context, *state.Access) error) {
	must(workflow.AddNode(node.NewFuncNode(node.Spec{ID: identifier, Name: identifier}, func(ctx core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), execute(ctx, access)
	})))
	must(workflow.SetNodeSpec(fanInFanOutSpec(identifier)))
}

func registerFanInFanOutTypes(componentRegistry *registry.Registry) {
	directBuild := func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
		return nil, fmt.Errorf("example node %q must be constructed directly", resolved.Spec.ID)
	}
	definitions := []registry.NodeTypeDefinition{
		{NodeTypeSchema: dsl.NodeTypeSchema{Type: "example_control"}, Build: directBuild},
		{
			NodeTypeSchema: dsl.NodeTypeSchema{Type: "example_branch", StatePorts: []dsl.StatePortDefinition{{
				Name: "branches", Required: true, Schema: dsl.JSONSchema{"type": "array", "items": map[string]any{"type": "string"}},
				Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeAppend,
			}}},
			Build: directBuild,
		},
		{
			NodeTypeSchema: dsl.NodeTypeSchema{Type: "example_collector", StatePorts: []dsl.StatePortDefinition{
				{Name: "branches", Required: true, Schema: dsl.JSONSchema{"type": "array", "items": map[string]any{"type": "string"}}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace},
				{Name: "count", Required: true, Schema: dsl.JSONSchema{"type": "integer"}, Mode: dsl.StateAccessWrite, MergeStrategy: dsl.StateMergeReplace},
			}},
			Build: directBuild,
		},
	}
	for _, definition := range definitions {
		must(componentRegistry.RegisterNodeType(definition))
	}
}

func fanInFanOutSpec(identifier string) dsl.GraphNodeSpec {
	spec := dsl.GraphNodeSpec{ID: identifier, Type: "example_control", Name: identifier}
	switch identifier {
	case "research", "draft":
		spec.Type = "example_branch"
		spec.State = map[string]dsl.StateBinding{"branches": {Path: "shared.branches"}}
	case "collector":
		spec.Type = "example_collector"
		spec.State = map[string]dsl.StateBinding{
			"branches": {Path: "shared.branches"},
			"count":    {Path: "shared.branch_count"},
		}
	}
	return spec
}

func findBarrierCheckpoint(ctx context.Context, runner *runtime.GraphRunner, runID string) string {
	checkpoints, err := runner.ListCheckpoints(ctx, runID)
	must(err)
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage == runtime.CheckpointAfterWave {
			return checkpoint.CheckpointID
		}
	}
	panic("after_parallel_wave checkpoint not found")
}

func printResult(currentState *state.State) {
	access := state.NewAccess(currentState)
	branches, _ := access.ReadAny(state.Shared("branches"))
	count, _ := access.ReadAny(state.Shared("branch_count"))
	fmt.Printf("branches: %#v\nbranch_count: %#v\n", branches, count)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
