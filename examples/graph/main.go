package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"weaveflow"
	"weaveflow/builder"
	"weaveflow/core"
	"weaveflow/llms/openai"
	"weaveflow/runtime"
	"weaveflow/state"
	"weaveflow/state/accessors"

	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()
	weaveflow.SetLogger(logger)

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
	tryPanic(state.SetPath(currentState, state.Scope(reactAgentStateScope, "pending_human_input").String(), "24+5*8-2=? 现在是几点."))

	baseDir := ".local/instance"
	graph, err := weaveflow.LoadGraphFromFile(&builder.BuildContext{}, filepath.Join(baseDir, "graph.json"))
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
	answer, _ := state.NewAccess(nil, currentState).ReadAny(state.Scope(reactAgentStateScope, accessors.KeyConversation, accessors.ConversationFieldFinalAnswer))
	fmt.Println(answer)
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
		state.NewJSONStateCodec(""),
		sink,
	)
	runner.ArtifactStore = runtime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	runner.GraphID = "graph-runner"
	runner.GraphVersion = "v1.0.0"
	return runner
}
