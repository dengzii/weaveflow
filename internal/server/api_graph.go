package server

import (
	"context"
	"errors"
	"path/filepath"
	"weaveflow"
	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"go.uber.org/zap"
)

type apiGraph struct {
	graphs   map[string]*fruntime.GraphRunner
	buildCtx *weaveflow.BuildContext
	registry *weaveflow.Registry
	baseDir  string
}

func newRunnerApi() (*apiGraph, error) {
	tootSets := map[string]tools.Tool{
		"current_time": tools.NewCurrentTime(),
		"calculator":   tools.NewCalculator(),
	}

	m, err := openai.New()
	if err != nil {
		return nil, err
	}

	buildContext := weaveflow.BuildContext{
		Model: m,
		Tools: tootSets,
	}

	registry := weaveflow.NewRegistry()

	return &apiGraph{
		graphs:   map[string]*fruntime.GraphRunner{},
		buildCtx: &buildContext,
		registry: registry,
		baseDir:  "graph_run",
	}, nil
}

type NewRunRequest struct {
	ID    string                     `json:"ID"`
	Graph *weaveflow.GraphDefinition `json:"graph"`
	State *weaveflow.State           `json:"state"`
}

func (a *apiGraph) NewRun(ctx *gin.Context, request *NewRunRequest) error {

	var instanceId string
	if request.ID != "" {
		instanceId = request.ID
	} else {
		instanceId = uuid.New().String()
	}

	baseDir := filepath.Join(a.baseDir, instanceId)
	graphPath := filepath.Join(a.baseDir, baseDir, "graph.json")

	var graph *weaveflow.Graph
	var err error
	if request.Graph != nil {
		graph, err = a.registry.BuildGraph(*request.Graph, a.buildCtx)
		if err != nil {
			return err
		}
		err = graph.WriteToFile(graphPath)
	} else {
		graph, err = weaveflow.LoadGraphFromFile(a.buildCtx, graphPath)
	}
	if err != nil {
		return err
	}

	var runner *fruntime.GraphRunner

	if a.graphs[instanceId] != nil {
		runner = a.graphs[instanceId]
		record, err := runner.GetContinuableRun(ctx)
		if err != nil {
			return err
		}
		if record == nil {
			return errors.New("no continuable run found")
		}
		var input fruntime.State
		if request.State != nil {
			input = *request.State
		}
		_, _, err = runner.Resume(ctx, record.RunID, input)
		return err
	}
	systemPrompt := "You are a helpful ReAct agent. " +
		"Use tools when they improve correctness, and return the final answer in plain text."

	messages := make([]llms.MessageContent, 0, 2)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt))
	initState := fruntime.NewBaseState(messages, 10)

	err = graph.WriteToFile(filepath.Join(baseDir, "graph.json"))
	if err != nil {
		return err
	}

	log, err := zap.NewDevelopment()
	fruntime.SetLogger(log)
	sink := fruntime.NewCombineEventSink(
		fruntime.NewLoggerEventSink(log),
		fruntime.NewFileEventSink(filepath.Join(baseDir, "events")),
	)

	executionStore := fruntime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	checkpointStore := fruntime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	stateCodec := fruntime.NewJSONStateCodec(fruntime.DefaultStateVersion)
	artifactStore := fruntime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))

	runner = weaveflow.NewGraphRunner(graph, executionStore, checkpointStore, stateCodec, sink)
	runner.ArtifactStore = artifactStore
	runner.GraphID = "graph-runner"
	runner.GraphVersion = "v1.0.0"
	a.graphs[instanceId] = runner

	record, _, err := runner.Start(context.Background(), initState)
	if err != nil {
		return err
	}

	return responseSuccess(ctx, record)

}
