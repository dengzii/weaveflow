package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()
	runtime.SetLogger(logger)

	ctx := newReActAgentContext()

	runWithRunner(ctx)

	time.Sleep(time.Second)
	fmt.Println("=================== resume ===================")
	resumeFromCheckpoint(ctx)
}

func newReActAgentContext() core.Context {
	model, err := openai.New()
	tryPanic(err)

	ctx := context.Background()
	ctx = core.WithModel(ctx, model)
	ctx = core.WithMemory(ctx, newReActAgentMemory())
	ctx = core.WithTools(ctx, newReActAgentTools())
	return core.NewContext(ctx)
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
	currentState := state.NewState()
	tryPanic(state.SetPath(currentState, reactAgentPendingInputPath.String(), "24+5*8-2=? 现在是几点."))

	baseDir := ".local/instance"
	graph, err := weaveflow.LoadGraphFromFile(filepath.Join(baseDir, "graph.json"), weaveflow.WithBuildContext(&registry.BuildContext{}))
	tryPanic(err)

	runner := newExampleRunner(baseDir, graph)
	run, err := runner.GetContinuableRun(ctx)
	tryPanic(err)
	if run == nil {
		panic("no continuable run")
	}

	_, currentState, err = runner.Resume(ctx, run.RunID, currentState)
	tryPanic(err)

	fmt.Println("=========Final Answer==========")
	answer, _ := state.NewAccess(currentState).ReadAny(reactAgentConversationPath.MustChild("final_answer"))
	fmt.Println(answer)
}

func tryPanic(error interface{}) {
	if error != nil {
		panic(error)
	}
}

func newExampleRunner(baseDir string, graph *wfgraph.Graph) *runtime.GraphRunner {
	log, err := zap.NewDevelopment()
	tryPanic(err)

	sink := runtime.NewCombineEventSink(
		runtime.NewLoggerEventSink(log),
		runtime.NewFileEventSink(filepath.Join(baseDir, "events")),
	)

	runner, err := weaveflow.NewRunner(
		graph,
		weaveflow.WithExecutionStore(runtime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))),
		weaveflow.WithCheckpointStore(runtime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))),
		weaveflow.WithEventSink(sink),
		weaveflow.WithArtifactStore(runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))),
		weaveflow.WithGraphID("graph-runner"),
		weaveflow.WithGraphVersion("v1.0.0"),
	)
	tryPanic(err)
	return runner
}
