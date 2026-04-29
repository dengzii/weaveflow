package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"weaveflow"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()
	weaveflow.SetLogger(logger)

	runWithRunner()

	time.Sleep(time.Second)
	fmt.Println("=================== resume ===================")
	resumeFromCheckpoint()
}

func runWithRunner() {
	baseDir := ".local/instance"
	graph := newReActAgentGraph()
	tryPanic(os.MkdirAll(baseDir, 0o755))
	tryPanic(graph.WriteToFile(filepath.Join(baseDir, "graph.json")))

	runner := newExampleRunner(baseDir, graph)
	_, _, err := runner.Start(context.Background(), newReActAgentInitialState())
	tryPanic(err)
}

func resumeFromCheckpoint() {
	state := runtime.State{
		"scopes": map[string]any{
			reactAgentStateScope: map[string]any{
				nodes.PendingHumanInputStateKey: "24+5*8-2=? 现在是几点.",
			},
		},
	}

	baseDir := ".local/instance"
	buildCtx := newReActAgentBuildContext()
	graph, err := weaveflow.LoadGraphFromFile(buildCtx, filepath.Join(baseDir, "graph.json"))
	tryPanic(err)

	runner := newExampleRunner(baseDir, graph)
	run, err := runner.GetContinuableRun(context.Background())
	tryPanic(err)
	if run == nil {
		panic("no continuable run")
	}

	_, state, err = runner.Resume(context.Background(), run.RunID, state)
	tryPanic(err)

	conv := runtime.Conversation(state, reactAgentStateScope)
	fmt.Println("=========Final Answer==========")
	fmt.Println(conv.FinalAnswer())
}

func tryPanic(error interface{}) {
	if error != nil {
		panic(error)
	}
}

func newExampleRunner(baseDir string, graph *weaveflow.Graph) *runtime.GraphRunner {
	log, err := zap.NewDevelopment()
	tryPanic(err)

	sink := runtime.NewCombineEventSink(
		runtime.NewLoggerEventSink(log),
		runtime.NewFileEventSink(filepath.Join(baseDir, "events")),
	)

	runner := weaveflow.NewGraphRunner(
		graph,
		runtime.NewFileExecutionStore(filepath.Join(baseDir, "execution")),
		runtime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints")),
		runtime.NewJSONStateCodec(runtime.DefaultStateVersion),
		sink,
	)
	runner.ArtifactStore = runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	runner.GraphID = "graph-runner"
	runner.GraphVersion = "v1.0.0"
	return runner
}
