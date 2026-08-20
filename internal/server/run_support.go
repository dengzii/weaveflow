package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
)

func (s *Server) makeRunResult(
	ctx context.Context,
	runner *runtime.GraphRunner,
	run runtime.RunRecord,
	finalState *state.State,
) runResult {
	return runResult{
		Run:       run,
		State:     finalState,
		Interrupt: s.buildRunInterrupt(ctx, runReader(runner), run),
	}
}

func (s *Server) buildRunInterrupt(ctx context.Context, reader runReader, run runtime.RunRecord) *runInterrupt {
	if reader == nil || run.Status != runtime.RunStatusPaused || strings.TrimSpace(run.LastCheckpointID) == "" {
		return nil
	}
	eventMessage := pausedRunEventMessage(reader, run)
	interrupt := &runInterrupt{
		RunID:                  run.RunID,
		CheckpointID:           run.LastCheckpointID,
		NodeID:                 run.CurrentNodeID,
		ResumeFromRunID:        run.RunID,
		ResumeFromCheckpointID: run.LastCheckpointID,
		Message:                firstNonEmpty(eventMessage, "run paused"),
	}
	checkpoint, err := reader.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		if eventMessage == "" {
			interrupt.Message = fmt.Sprintf("run paused; checkpoint details unavailable: %v", err)
		}
		return interrupt
	}

	interrupt.StepID = checkpoint.Record.StepID
	interrupt.NodeID = firstNonEmpty(checkpoint.Record.NodeID, checkpoint.Runtime.CurrentNodeID, run.CurrentNodeID)
	interrupt.Stage = string(checkpoint.Record.Stage)
	interrupt.BreakpointHit = checkpoint.Runtime.BreakpointHit
	runtimeState := checkpoint.Runtime
	interrupt.Runtime = &runtimeState
	interrupt.Message = firstNonEmpty(eventMessage, interruptMessage(checkpoint))
	return interrupt
}

func pausedRunEventMessage(reader runReader, run runtime.RunRecord) string {
	if reader == nil || strings.TrimSpace(run.RunID) == "" {
		return ""
	}
	events, err := reader.ListEvents(run.RunID)
	if err != nil {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type != runtime.EventRunPaused {
			continue
		}
		checkpointID := eventPayloadString(event.Payload, "checkpoint_id")
		if checkpointID != "" && checkpointID != run.LastCheckpointID {
			continue
		}
		if message := eventPayloadString(event.Payload, "message"); message != "" {
			return message
		}
	}
	return ""
}

func eventPayloadString(payload json.RawMessage, key string) string {
	if len(payload) == 0 || strings.TrimSpace(key) == "" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func interruptMessage(checkpoint runtime.RestoredCheckpoint) string {
	if checkpoint.Runtime.BreakpointHit != nil {
		hit := checkpoint.Runtime.BreakpointHit
		return fmt.Sprintf("breakpoint %q paused before resume at %s %q", hit.BreakpointID, hit.Stage, hit.NodeID)
	}
	nodeID := firstNonEmpty(checkpoint.Record.NodeID, checkpoint.Runtime.CurrentNodeID)
	switch checkpoint.Record.Stage {
	case runtime.CheckpointBeforeNode:
		return fmt.Sprintf("run paused before node %q", nodeID)
	case runtime.CheckpointAfterNode:
		return fmt.Sprintf("run paused after node %q", nodeID)
	case runtime.CheckpointAfterWave:
		return "run paused after wave"
	default:
		return "run paused"
	}
}

func (s *Server) deriveRunContext(c *gin.Context) (context.Context, context.CancelFunc) {
	requestCtx := context.Background()
	if c != nil && c.Request != nil {
		requestCtx = c.Request.Context()
	}
	return s.deriveRunContextFrom(requestCtx)
}

func (s *Server) deriveRunContextFrom(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if s != nil && s.runtime != nil {
		base = s.runtime.runtimeContext()
	}
	return deriveRunContextFromBase(parent, base)
}

func deriveRunContextFromBase(parent context.Context, base context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if base == nil {
		base = context.Background()
	}

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if deadline, ok := parent.Deadline(); ok {
		ctx, cancel = context.WithDeadline(base, deadline)
	} else {
		ctx, cancel = context.WithCancel(base)
	}
	stop := context.AfterFunc(parent, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

type startRunRequest struct {
	InitialState map[string]any `json:"initial_state,omitempty"`
}

type resumeRunRequest struct {
	Input map[string]any `json:"input,omitempty"`
}

type forkRunRequest struct {
	CheckpointID string         `json:"checkpoint_id"`
	RequestKey   string         `json:"request_key"`
	Input        map[string]any `json:"input,omitempty"`
}

func decodeStartRunRequest(c *gin.Context) (*state.State, error) {
	var request startRunRequest
	if err := decodeRunRequest(c, &request); err != nil {
		return nil, err
	}
	if request.InitialState == nil {
		return state.NewState(), nil
	}
	if err := validateExternalRunState(request.InitialState); err != nil {
		return nil, err
	}
	return state.FromMap(request.InitialState), nil
}

func decodeResumeRunRequest(c *gin.Context) (*state.State, error) {
	var request resumeRunRequest
	if err := decodeRunRequest(c, &request); err != nil {
		return nil, err
	}
	if request.Input == nil {
		return state.NewState(), nil
	}
	if err := validateExternalRunState(request.Input); err != nil {
		return nil, err
	}
	return state.FromMap(request.Input), nil
}

func decodeForkRunRequest(c *gin.Context) (forkRunRequest, *state.State, error) {
	var request forkRunRequest
	if err := decodeRunRequest(c, &request); err != nil {
		return forkRunRequest{}, nil, err
	}
	request.CheckpointID = strings.TrimSpace(request.CheckpointID)
	request.RequestKey = strings.TrimSpace(request.RequestKey)
	if request.CheckpointID == "" || request.RequestKey == "" {
		return forkRunRequest{}, nil, invalidRequestf("checkpoint_id and request_key are required")
	}
	if request.Input == nil {
		return request, state.NewState(), nil
	}
	if err := validateExternalRunState(request.Input); err != nil {
		return forkRunRequest{}, nil, err
	}
	return request, state.FromMap(request.Input), nil
}

func validateExternalRunState(values map[string]any) error {
	for section, value := range values {
		switch section {
		case state.SectionShared, state.SectionScopes:
			if _, ok := value.(map[string]any); !ok {
				return invalidRequestf("state section %q must be an object", section)
			}
		case state.SectionInternal, state.SectionRuntime:
			return invalidRequestf("state section %q is reserved", section)
		default:
			return invalidRequestf("state section %q is unknown", section)
		}
	}
	return nil
}

func decodeRunRequest(c *gin.Context, target any) error {
	body, err := readRequestBody(c.Request.Body, maxRunStateBodyBytes)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := decodeStrictJSON(body, target); err != nil {
		return invalidRequestf("invalid JSON body: %v", err)
	}
	return nil
}

func parseRunStatuses(c *gin.Context) ([]runtime.RunStatus, error) {
	values, err := stringListQuery(c, "status")
	if err != nil {
		return nil, err
	}
	statuses := make([]runtime.RunStatus, 0, len(values))
	for _, value := range values {
		status := runtime.RunStatus(value)
		switch status {
		case runtime.RunStatusPending,
			runtime.RunStatusRunning,
			runtime.RunStatusPaused,
			runtime.RunStatusFailed,
			runtime.RunStatusCompleted,
			runtime.RunStatusCanceled:
			statuses = append(statuses, status)
		default:
			return nil, invalidRequestf("unsupported status %q", value)
		}
	}
	return statuses, nil
}
