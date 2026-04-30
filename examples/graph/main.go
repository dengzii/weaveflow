package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"weaveflow"
	"weaveflow/llms/openai"
	"weaveflow/nodes"
	"weaveflow/runtime"

	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()
	weaveflow.SetLogger(logger)

	ctx := runtime.WithServices(context.Background(), newReActAgentServices())

	runWithRunner(ctx)

	time.Sleep(time.Second)
	fmt.Println("=================== resume ===================")
	resumeFromCheckpoint(ctx)
}

func newReActAgentServices() *runtime.Services {
	model, err := openai.New()
	tryPanic(err)

	return &runtime.Services{
		Model:  runtime.WrapLLM(model),
		Memory: newReActAgentMemory(),
		Tools:  newReActAgentTools(),
	}
}

func runWithRunner(ctx context.Context) {
	baseDir := ".local/instance"
	graph := newReActAgentGraph()
	tryPanic(os.MkdirAll(baseDir, 0o755))
	tryPanic(graph.WriteToFile(filepath.Join(baseDir, "graph.json")))

	runner := newExampleRunner(baseDir, graph)
	_, _, err := runner.Start(ctx, newReActAgentInitialState())
	tryPanic(err)
}

func resumeFromCheckpoint(ctx context.Context) {
	state := runtime.State{
		"scopes": map[string]any{
			reactAgentStateScope: map[string]any{
				nodes.PendingHumanInputStateKey: "24+5*8-2=? 现在是几点.",
			},
		},
	}

	baseDir := ".local/instance"
	graph, err := weaveflow.LoadGraphFromFile(&weaveflow.BuildContext{}, filepath.Join(baseDir, "graph.json"))
	tryPanic(err)

	runner := newExampleRunner(baseDir, graph)
	run, err := runner.GetContinuableRun(ctx)
	tryPanic(err)
	if run == nil {
		panic("no continuable run")
	}

	_, state, err = runner.Resume(ctx, run.RunID, state)
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
