package falcon

import (
	"context"
	fruntime "falcon/runtime"
	"os"
	"path/filepath"

	"github.com/tmc/langchaingo/llms/openai"
	"go.uber.org/zap"
)

func NewGraphRunner(graph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec fruntime.StateCodec, eventSink fruntime.EventSink) *fruntime.GraphRunner {
	return fruntime.NewGraphRunner(NewRunnerGraph(graph), executionStore, checkpointStore, codec, eventSink)
}

func RunGraphWithRunner(baseDir string, graph *Graph, initState fruntime.State) error {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}

	err := graph.WriteToFile(filepath.Join(baseDir, "graph.json"))
	if err != nil {
		return err
	}

	log, err := zap.NewDevelopment()
	if err != nil {
		return err
	}
	sink := fruntime.NewLoggerEventSink(log)

	executionStore := fruntime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	checkpointStore := fruntime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	stateCodec := fruntime.NewJSONStateCodec(fruntime.DefaultStateVersion)
	artifactStore := fruntime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))

	runner := NewGraphRunner(graph, executionStore, checkpointStore, stateCodec, sink)
	runner.ArtifactStore = artifactStore
	runner.GraphID = "graph-runner"
	runner.GraphVersion = "v1.0.0"

	record, state, err := runner.Start(context.Background(), initState)
	logger.Debug("run finished",
		zap.Any("status", record.Status),
		zap.Any("current_node", record.CurrentNodeID),
		zap.String("error_code", record.ErrorCode),
		zap.String("error_message", record.ErrorMessage),
		zap.Any("state", state),
	)
	return err
}

func ResumeGraphRunnerFromDirectory(baseDir string, state State) error {
	toolSets := map[string]Tool{
		"current_time": NewCurrentTime(),
		"calculator":   NewCalculator(),
	}

	model, err := openai.New()
	if err != nil {
		return err
	}

	buildContext := &BuildContext{
		Model: model,
		Tools: toolSets,
	}

	graph, err := LoadGraphFromFile(buildContext, filepath.Join(baseDir, "graph.json"))
	if err != nil {
		return err
	}

	log, err := zap.NewDevelopment()
	if err != nil {
		return err
	}
	sink := fruntime.NewLoggerEventSink(log)

	executionStore := fruntime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	checkpointStore := fruntime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	stateCodec := fruntime.NewJSONStateCodec(fruntime.DefaultStateVersion)
	artifactStore := fruntime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))

	runner := NewGraphRunner(graph, executionStore, checkpointStore, stateCodec, sink)
	runner.ArtifactStore = artifactStore
	runner.GraphID = "graph-runner"
	runner.GraphVersion = "v1.0.0"

	run, err := runner.GetResumableRun(context.Background())
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}

	_, _, err = runner.Resume(context.Background(), run.RunID, state)
	return err
}
