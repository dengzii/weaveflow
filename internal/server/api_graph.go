package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"weaveflow"
	wdsl "weaveflow/dsl"
	"weaveflow/internal/redact"
	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"go.uber.org/zap"
)

const defaultGraphSystemPrompt = "You are a helpful ReAct agent. Use tools when they improve correctness, and return the final answer in plain text."

type apiGraph struct {
	graphs   map[string]*fruntime.GraphRunner
	buildCtx *weaveflow.BuildContext
	registry *weaveflow.Registry
	baseDir  string
}

func newRunnerApi() (*apiGraph, error) {
	toolSets := map[string]tools.Tool{
		"current_time": tools.NewCurrentTime(),
		"calculator":   tools.NewCalculator(),
	}

	model, err := openai.New()
	if err != nil {
		return nil, err
	}

	buildContext := weaveflow.BuildContext{
		Model: model,
		Tools: toolSets,
	}

	return &apiGraph{
		graphs:   map[string]*fruntime.GraphRunner{},
		buildCtx: &buildContext,
		registry: weaveflow.DefaultRegistry(),
		baseDir:  "graph_run",
	}, nil
}

type NewRunRequest struct {
	ID       string                     `json:"id,omitempty"`
	Graph    *weaveflow.GraphDefinition `json:"graph,omitempty"`
	Instance *wdsl.GraphInstanceConfig  `json:"instance,omitempty"`
	Run      *wdsl.RunRequest           `json:"run,omitempty"`
	State    *weaveflow.State           `json:"state,omitempty"`
}

type graphRunResponse struct {
	InstanceID   string                   `json:"instance_id"`
	GraphRef     string                   `json:"graph_ref,omitempty"`
	GraphVersion string                   `json:"graph_version,omitempty"`
	Run          fruntime.RunRecord       `json:"run"`
	State        *fruntime.StateSnapshot  `json:"state,omitempty"`
	StateDiffs   []graphResponseEvent     `json:"state_diffs,omitempty"`
	Artifacts    []graphResponseArtifact  `json:"artifacts,omitempty"`
	Snapshots    []graphCheckpointPayload `json:"snapshots,omitempty"`
}

type graphResponseEvent struct {
	ID        string             `json:"id"`
	RunID     string             `json:"run_id"`
	StepID    string             `json:"step_id,omitempty"`
	NodeID    string             `json:"node_id,omitempty"`
	Type      fruntime.EventType `json:"type"`
	Timestamp time.Time          `json:"timestamp"`
	Payload   any                `json:"payload,omitempty"`
}

type graphResponseArtifact struct {
	Ref      fruntime.ArtifactRef `json:"ref"`
	Bytes    int                  `json:"bytes"`
	Encoding string               `json:"encoding,omitempty"`
	Payload  any                  `json:"payload,omitempty"`
}

type graphCheckpointPayload struct {
	Record   fruntime.CheckpointRecord `json:"record"`
	Snapshot fruntime.StateSnapshot    `json:"snapshot"`
}

func (a *apiGraph) NewRun(ctx *gin.Context, request *NewRunRequest) error {
	instanceID := strings.TrimSpace(request.instanceID())
	if instanceID == "" {
		instanceID = uuid.NewString()
	}

	baseDir := filepath.Join(a.baseDir, instanceID)
	graphPath := filepath.Join(baseDir, "graph.json")
	instancePath := filepath.Join(baseDir, "instance.json")

	instance, err := a.resolveInstanceConfig(request, instancePath, instanceID)
	if err != nil {
		return err
	}
	runRequest, err := request.resolveRunRequest(instance.ID)
	if err != nil {
		return err
	}
	if runRequest.Stream {
		return errors.New("run.stream is not supported by /graph/run")
	}

	graphDef, err := a.resolveGraphDefinition(request, graphPath)
	if err != nil {
		return err
	}

	if err := writeGraphDefinitionFile(graphPath, graphDef); err != nil {
		return err
	}
	if err := writeGraphInstanceConfigFile(instancePath, instance); err != nil {
		return err
	}

	buildContext := *a.buildCtx
	buildContext.InstanceConfig = &instance
	graph, err := a.registry.BuildGraphInstance(graphDef, instance, &buildContext)
	if err != nil {
		return err
	}

	runner, err := a.newRunner(baseDir, graph, instance)
	if err != nil {
		return err
	}
	appliedDef, err := graph.Definition()
	if err != nil {
		return err
	}
	runner.Breakpoints = effectiveBreakpoints(runRequest.Debug, appliedDef)
	a.graphs[instance.ID] = runner

	var run fruntime.RunRecord
	var state fruntime.State
	switch {
	case runRequest.ResumeFromCheckpointID != "":
		run, state, err = runner.ResumeFromCheckpoint(ctx, runRequest.ResumeFromCheckpointID, runRequest.Input)
	case runRequest.ResumeFromRunID != "":
		run, state, err = runner.Resume(ctx, runRequest.ResumeFromRunID, runRequest.Input)
	default:
		initialState, stateErr := initialRunState(instance, runRequest)
		if stateErr != nil {
			return stateErr
		}
		run, state, err = runner.Start(ctx, initialState)
	}
	if err != nil {
		return err
	}

	response, err := a.buildRunResponse(ctx, runner, instance, runRequest, run, state)
	if err != nil {
		return err
	}
	sanitized, err := sanitizeResponseValue(response, effectiveRedactionMode(runRequest.Debug))
	if err != nil {
		return err
	}
	return responseSuccess(ctx, sanitized)
}

func (a *apiGraph) resolveInstanceConfig(request *NewRunRequest, path string, fallbackID string) (wdsl.GraphInstanceConfig, error) {
	var instance wdsl.GraphInstanceConfig
	switch {
	case request.Instance != nil:
		instance = *request.Instance
	default:
		loaded, err := readGraphInstanceConfigFile(path)
		if err == nil {
			instance = loaded
		} else if !os.IsNotExist(err) {
			return wdsl.GraphInstanceConfig{}, err
		}
	}

	if request.Run != nil && strings.TrimSpace(request.Run.InstanceID) != "" {
		if strings.TrimSpace(instance.ID) != "" && instance.ID != request.Run.InstanceID {
			return wdsl.GraphInstanceConfig{}, fmt.Errorf("instance id %q does not match run.instance_id %q", instance.ID, request.Run.InstanceID)
		}
		instance.ID = strings.TrimSpace(request.Run.InstanceID)
	}
	if legacyID := strings.TrimSpace(request.ID); legacyID != "" {
		if strings.TrimSpace(instance.ID) != "" && instance.ID != legacyID {
			return wdsl.GraphInstanceConfig{}, fmt.Errorf("instance id %q does not match request id %q", instance.ID, legacyID)
		}
		instance.ID = legacyID
	}
	if strings.TrimSpace(instance.ID) == "" {
		instance.ID = fallbackID
	}
	if strings.TrimSpace(instance.GraphRef) == "" {
		instance.GraphRef = instance.ID
	}
	return instance, instance.Validate()
}

func (a *apiGraph) resolveGraphDefinition(request *NewRunRequest, path string) (weaveflow.GraphDefinition, error) {
	if request.Graph != nil {
		def := *request.Graph
		def = wdsl.NormalizeGraphDefinition(def)
		return def, def.Validate()
	}
	return readGraphDefinitionFile(path)
}

func (r *NewRunRequest) resolveRunRequest(instanceID string) (wdsl.RunRequest, error) {
	var request wdsl.RunRequest
	if r.Run != nil {
		request = *r.Run
	}
	if strings.TrimSpace(request.InstanceID) != "" && request.InstanceID != instanceID {
		return wdsl.RunRequest{}, fmt.Errorf("run.instance_id %q does not match instance id %q", request.InstanceID, instanceID)
	}
	request.InstanceID = instanceID
	if r.State != nil {
		if len(request.Input) > 0 {
			return wdsl.RunRequest{}, errors.New("legacy state and run.input cannot be provided together")
		}
		request.Input = *r.State
	}
	return request, request.Validate()
}

func (r *NewRunRequest) instanceID() string {
	if r == nil {
		return ""
	}
	if r.Run != nil && strings.TrimSpace(r.Run.InstanceID) != "" {
		return strings.TrimSpace(r.Run.InstanceID)
	}
	if r.Instance != nil && strings.TrimSpace(r.Instance.ID) != "" {
		return strings.TrimSpace(r.Instance.ID)
	}
	return strings.TrimSpace(r.ID)
}

func (a *apiGraph) newRunner(baseDir string, graph *weaveflow.Graph, instance wdsl.GraphInstanceConfig) (*fruntime.GraphRunner, error) {
	log, err := zap.NewDevelopment()
	if err != nil {
		return nil, err
	}
	fruntime.SetLogger(log)

	sink := fruntime.NewCombineEventSink(
		fruntime.NewLoggerEventSink(log),
		fruntime.NewFileEventSink(filepath.Join(baseDir, "events")),
	)

	executionStore := fruntime.NewFileExecutionStore(filepath.Join(baseDir, "execution"))
	checkpointStore := fruntime.NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints"))
	stateCodec := fruntime.NewJSONStateCodec(fruntime.DefaultStateVersion)
	artifactStore := fruntime.NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))

	runner := weaveflow.NewGraphRunner(graph, executionStore, checkpointStore, stateCodec, sink)
	runner.ArtifactStore = artifactStore
	runner.GraphID = strings.TrimSpace(instance.GraphRef)
	runner.GraphVersion = strings.TrimSpace(instance.GraphVersion)
	return runner, nil
}

func initialRunState(instance wdsl.GraphInstanceConfig, request wdsl.RunRequest) (fruntime.State, error) {
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, defaultGraphSystemPrompt),
	}
	state := fruntime.NewBaseState(messages, 10)

	var err error
	if len(instance.Memory) > 0 {
		state, err = fruntime.MergeInputState(state, fruntime.State(instance.Memory))
		if err != nil {
			return nil, fmt.Errorf("merge instance memory: %w", err)
		}
	}
	if len(request.Input) > 0 {
		state, err = fruntime.MergeInputState(state, request.Input)
		if err != nil {
			return nil, fmt.Errorf("merge run input: %w", err)
		}
	}
	return state, nil
}

func (a *apiGraph) buildRunResponse(ctx context.Context, runner *fruntime.GraphRunner, instance wdsl.GraphInstanceConfig, request wdsl.RunRequest, run fruntime.RunRecord, state fruntime.State) (graphRunResponse, error) {
	response := graphRunResponse{
		InstanceID:   instance.ID,
		GraphRef:     instance.GraphRef,
		GraphVersion: instance.GraphVersion,
		Run:          run,
	}
	if request.Debug == nil {
		return response, nil
	}

	if request.Debug.IncludeState {
		snapshot, err := fruntime.SnapshotFromState(state)
		if err != nil {
			return graphRunResponse{}, err
		}
		response.State = &snapshot
	}

	if request.Debug.IncludeStateDiff {
		events, err := runner.ListEvents(run.RunID)
		if err != nil {
			return graphRunResponse{}, err
		}
		response.StateDiffs, err = stateDiffEvents(events)
		if err != nil {
			return graphRunResponse{}, err
		}
	}

	if request.Debug.IncludeArtifacts {
		refs, err := runner.ListArtifacts(ctx, run.RunID)
		if err != nil {
			return graphRunResponse{}, err
		}
		response.Artifacts = make([]graphResponseArtifact, 0, len(refs))
		for _, ref := range refs {
			artifact, err := runner.LoadArtifact(ctx, ref)
			if err != nil {
				return graphRunResponse{}, err
			}
			payload, encoding := decodeArtifactPayload(artifact)
			response.Artifacts = append(response.Artifacts, graphResponseArtifact{
				Ref:      ref,
				Bytes:    len(artifact.Data),
				Encoding: encoding,
				Payload:  payload,
			})
		}
	}

	if request.Debug.IncludeSnapshots {
		records, err := runner.ListCheckpoints(ctx, run.RunID)
		if err != nil {
			return graphRunResponse{}, err
		}
		response.Snapshots = make([]graphCheckpointPayload, 0, len(records))
		for _, record := range records {
			restored, err := runner.LoadCheckpointState(ctx, record.CheckpointID)
			if err != nil {
				return graphRunResponse{}, err
			}
			response.Snapshots = append(response.Snapshots, graphCheckpointPayload{
				Record:   record,
				Snapshot: restored.Snapshot,
			})
		}
	}

	return response, nil
}

func stateDiffEvents(events []fruntime.Event) ([]graphResponseEvent, error) {
	filtered := make([]graphResponseEvent, 0, len(events))
	for _, event := range events {
		if event.Type != fruntime.EventStateChanged {
			continue
		}
		payload, err := decodeJSONPayload(event.Payload)
		if err != nil {
			return nil, err
		}
		filtered = append(filtered, graphResponseEvent{
			ID:        event.ID,
			RunID:     event.RunID,
			StepID:    event.StepID,
			NodeID:    event.NodeID,
			Type:      event.Type,
			Timestamp: event.Timestamp,
			Payload:   payload,
		})
	}
	return filtered, nil
}

func decodeArtifactPayload(artifact fruntime.Artifact) (any, string) {
	switch {
	case strings.Contains(strings.ToLower(artifact.MIMEType), "json"):
		payload, err := decodeJSONPayload(artifact.Data)
		if err == nil {
			return payload, "json"
		}
		return string(artifact.Data), "text"
	case strings.HasPrefix(strings.ToLower(artifact.MIMEType), "text/"):
		return string(artifact.Data), "text"
	default:
		return base64.StdEncoding.EncodeToString(artifact.Data), "base64"
	}
}

func decodeJSONPayload(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func effectiveBreakpoints(debug *wdsl.RunDebugOptions, def weaveflow.GraphDefinition) []fruntime.Breakpoint {
	if debug == nil {
		return nil
	}

	seen := make(map[string]struct{})
	breakpoints := make([]fruntime.Breakpoint, 0, len(debug.Breakpoints)+len(debug.PauseBefore)+len(debug.PauseAfter)+len(def.Nodes))
	appendBreakpoint := func(breakpoint fruntime.Breakpoint) {
		key := breakpoint.NodeID + "|" + breakpoint.Stage
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		breakpoints = append(breakpoints, breakpoint)
	}

	for _, breakpoint := range debug.EffectiveBreakpoints() {
		appendBreakpoint(breakpoint)
	}
	if debug.StepMode {
		for _, node := range def.Nodes {
			appendBreakpoint(fruntime.Breakpoint{
				ID:      fmt.Sprintf("%s:%s", fruntime.CheckpointBeforeNode, node.ID),
				NodeID:  node.ID,
				Stage:   string(fruntime.CheckpointBeforeNode),
				Enabled: true,
			})
		}
	}
	return breakpoints
}

func effectiveRedactionMode(debug *wdsl.RunDebugOptions) string {
	if debug == nil {
		return wdsl.RunRedactionModeSafe
	}
	return debug.EffectiveRedactionMode()
}

func sanitizeResponseValue(value any, mode string) (any, error) {
	if value == nil || mode == wdsl.RunRedactionModeRaw {
		return value, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return redact.Any(payload), nil
}

func writeGraphDefinitionFile(path string, def weaveflow.GraphDefinition) error {
	data, err := def.Serialize()
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

func readGraphDefinitionFile(path string) (weaveflow.GraphDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return weaveflow.GraphDefinition{}, err
	}
	return wdsl.DeserializeGraphDefinition(data)
}

func writeGraphInstanceConfigFile(path string, cfg wdsl.GraphInstanceConfig) error {
	data, err := wdsl.SerializeGraphInstanceConfig(cfg)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

func readGraphInstanceConfigFile(path string) (wdsl.GraphInstanceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return wdsl.GraphInstanceConfig{}, err
	}
	return wdsl.DeserializeGraphInstanceConfig(data)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
