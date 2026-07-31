package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
)

func (s *Server) requireRunner(c *gin.Context) *runtime.GraphRunner {
	runner := s.currentRunner()
	if runner == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return nil
	}
	return runner
}

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
	case runtime.CheckpointAfterParallelWave:
		return "run paused after parallel wave"
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
	if parent == nil {
		parent = context.Background()
	}
	base := context.Background()
	if s != nil {
		s.mu.RLock()
		if s.baseCtx != nil {
			base = s.baseCtx
		}
		s.mu.RUnlock()
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

func bindStatePayload(c *gin.Context, keys ...string) (*state.State, error) {
	body, err := readRequestBody(c.Request.Body, maxRunStateBodyBytes)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return state.NewState(), nil
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	for _, key := range keys {
		raw, ok := root[key]
		if !ok || raw == nil {
			continue
		}
		values, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", key)
		}
		return state.FromMap(values), nil
	}
	return state.FromMap(root), nil
}

func parseRunStatuses(c *gin.Context) []runtime.RunStatus {
	values := append([]string{}, c.QueryArray("status")...)
	values = append(values, c.QueryArray("statuses")...)
	statuses := make([]runtime.RunStatus, 0, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				statuses = append(statuses, runtime.RunStatus(item))
			}
		}
	}
	return statuses
}
