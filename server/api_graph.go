package server

import (
	"context"
	"errors"
	"falcon"
	fruntime "falcon/runtime"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"go.uber.org/zap"
)

type apiGraph struct {
	graphs   map[string]*fruntime.GraphRunner
	buildCtx *falcon.BuildContext
	registry *falcon.Registry
	baseDir  string
}

func newRunnerApi() (*apiGraph, error) {
	tootSets := map[string]falcon.Tool{
		"current_time": falcon.NewCurrentTime(),
		"calculator":   falcon.NewCalculator(),
	}

	m, err := openai.New()
	if err != nil {
		return nil, err
	}

	buildContext := falcon.BuildContext{
		Model: m,
		Tools: tootSets,
	}

	registry := falcon.NewRegistry()

	return &apiGraph{
		graphs:   map[string]*fruntime.GraphRunner{},
		buildCtx: &buildContext,
		registry: registry,
		baseDir:  "graph_run",
	}, nil
}

type NewRunRequest struct {
	ID    string                  `json:"ID"`
	Graph *falcon.GraphDefinition `json:"graph"`
	State *falcon.State           `json:"state"`
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

	var graph *falcon.Graph
	var err error
	if request.Graph != nil {
		graph, err = a.registry.BuildGraph(*request.Graph, a.buildCtx)
		if err != nil {
			return err
		}
		err = graph.WriteToFile(graphPath)
	} else {
		graph, err = falcon.LoadGraphFromFile(a.buildCtx, graphPath)
	}
	if err != nil {
		return err
	}

	var runner *fruntime.GraphRunner

	if a.graphs[instanceId] != nil {
		runner = a.graphs[instanceId]
		record, err := runner.GetResumableRun(ctx)
		if err != nil {
			return err
		}
		if record == nil {
			return errors.New("no resumable run found")
		}
		_, _, err = runner.Resume(ctx, record.RunID)
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
	sink := fruntime.NewLoggerEventSink(log)

	executionStore := fruntime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	checkpointStore := fruntime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	stateCodec := fruntime.NewJSONStateCodec(fruntime.DefaultStateVersion)
	artifactStore := fruntime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))

	runner = fruntime.NewGraphRunner(falcon.NewRunnerGraph(graph), executionStore, checkpointStore, stateCodec, sink)
	runner.ArtifactStore = artifactStore
	runner.GraphID = "graph-runner"
	runner.GraphVersion = "v1.0.0"

	record, _, err := runner.Start(context.Background(), initState)
	if err != nil {
		return err
	}

	return responseSuccess(ctx, record)

}
