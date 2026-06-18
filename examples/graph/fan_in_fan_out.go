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
	"github.com/dengzii/weaveflow/node"
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

	runner := weaveflow.NewGraphRunner(
		graph,
		runtime.NewFileExecutionStore(filepath.Join(baseDir, "execution")),
		runtime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints")),
		state.NewJSONStateCodec(""),
		runtime.NewFileEventSink(filepath.Join(baseDir, "events")),
	)
	run, _, err := runner.Start(ctx, state.NewState())
	must(err)

	barrierID := findBarrierCheckpoint(ctx, runner, run.RunID)
	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(ctx, barrierID, nil)
	must(err)
	fmt.Printf("resumed status: %s\n", resumedRun.Status)
	printResult(resumedState)
}

func newFanInFanOutGraph() *weaveflow.Graph {
	g := weaveflow.NewGraph()
	addFuncNode(g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	addFuncNode(g, "research", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "research")
	})
	addFuncNode(g, "draft", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "draft")
	})
	addFuncNode(g, "collector", func(ctx context.Context, access *state.Access) error {
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})

	must(g.SetEntryPoint("router"))
	must(g.SetFinishPoint("collector"))
	must(g.AddEdge("router", "research"))
	must(g.AddEdge("router", "draft"))
	must(g.AddEdge("research", "collector"))
	must(g.AddEdge("draft", "collector"))
	return g
}

func addFuncNode(g *weaveflow.Graph, id string, fn func(context.Context, *state.Access) error) {
	must(g.AddNode(node.NewFuncNode(node.Spec{ID: id, Name: id}, func(ctx core.Context, access *state.Access) error {
		return fn(ctx, access)
	})))
	g.SetNodeSpec(dsl.GraphNodeSpec{ID: id, Type: "example", Name: id})
}

func findBarrierCheckpoint(ctx context.Context, runner *runtime.GraphRunner, runID string) string {
	checkpoints, err := runner.ListCheckpoints(ctx, runID)
	must(err)
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage == runtime.CheckpointAfterParallelWave {
			return checkpoint.CheckpointID
		}
	}
	panic("after_parallel_wave checkpoint not found")
}

func printResult(currentState *state.State) {
	access := state.NewAccess(nil, currentState)
	branches, _ := access.ReadAny(state.Shared("branches"))
	count, _ := access.ReadAny(state.Shared("branch_count"))
	fmt.Printf("branches: %#v\nbranch_count: %#v\n", branches, count)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
