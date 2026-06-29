package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
)

type runResult struct {
	Run       runtime.RunRecord `json:"run"`
	State     *state.State      `json:"state,omitempty"`
	Interrupt *runInterrupt     `json:"interrupt,omitempty"`
}

type runDetail struct {
	Run         runtime.RunRecord          `json:"run"`
	Steps       []runtime.StepRecord       `json:"steps"`
	Checkpoints []runtime.CheckpointRecord `json:"checkpoints"`
	Events      []runtime.Event            `json:"events"`
	Artifacts   []state.ArtifactRef        `json:"artifacts"`
	Interrupt   *runInterrupt              `json:"interrupt,omitempty"`
}

type runInterrupt struct {
	RunID                  string               `json:"run_id"`
	CheckpointID           string               `json:"checkpoint_id"`
	StepID                 string               `json:"step_id,omitempty"`
	NodeID                 string               `json:"node_id,omitempty"`
	Stage                  string               `json:"stage,omitempty"`
	Message                string               `json:"message,omitempty"`
	ResumeFromRunID        string               `json:"resume_from_run_id,omitempty"`
	ResumeFromCheckpointID string               `json:"resume_from_checkpoint_id,omitempty"`
	BreakpointHit          *state.BreakpointHit `json:"breakpoint_hit,omitempty"`
	Runtime                *state.RuntimeState  `json:"runtime,omitempty"`
}

func (s *Server) handleStartRun(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	initialState, err := bindStatePayload(c, "initial_state", "state", "input")
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	run, finalState, err := runner.Start(ctx, initialState)
	result := s.makeRunResult(ctx, runner, run, finalState)
	if err != nil {
		writeErrorData(c, statusForError(err), err, result)
		return
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleResumeRun(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	input, err := bindStatePayload(c, "input", "state", "initial_state")
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	run, finalState, err := runner.Resume(ctx, runID, input)
	result := s.makeRunResult(ctx, runner, run, finalState)
	if err != nil {
		writeErrorData(c, statusForError(err), err, result)
		return
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handleResumeCheckpoint(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	checkpointID := strings.TrimSpace(c.Param("checkpoint_id"))
	if checkpointID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("checkpoint_id is required"))
		return
	}
	input, err := bindStatePayload(c, "input", "state", "initial_state")
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := s.deriveRunContext(c)
	defer cancel()

	run, finalState, err := runner.ResumeFromCheckpoint(ctx, checkpointID, input)
	result := s.makeRunResult(ctx, runner, run, finalState)
	if err != nil {
		writeErrorData(c, statusForError(err), err, result)
		return
	}
	writeData(c, http.StatusOK, result)
}

func (s *Server) handlePauseRun(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	if err := runner.Pause(c.Request.Context(), runID); err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	run, err := runner.GetRun(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func (s *Server) handleCancelRun(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	if err := runner.Cancel(c.Request.Context(), runID); err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	run, err := runner.GetRun(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func (s *Server) handleListRuns(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runs, err := runner.ListRuns(c.Request.Context(), runtime.RunFilter{Statuses: parseRunStatuses(c)})
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, runs)
}

func (s *Server) handleGetRun(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	run, err := runner.GetRun(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func (s *Server) handleGetRunDetail(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}

	run, err := runner.GetRun(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	steps, err := runner.ListSteps(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	checkpoints, err := runner.ListCheckpoints(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	events, err := runner.ListEvents(runID)
	if err != nil {
		writeError(c, statusForListEventsError(err), err)
		return
	}
	artifacts, err := runner.ListArtifacts(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}

	writeData(c, http.StatusOK, runDetail{
		Run:         run,
		Steps:       steps,
		Checkpoints: checkpoints,
		Events:      events,
		Artifacts:   artifacts,
		Interrupt:   s.buildRunInterrupt(c.Request.Context(), runner, run),
	})
}

func (s *Server) handleListSteps(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	steps, err := runner.ListSteps(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, steps)
}

func (s *Server) handleListCheckpoints(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	checkpoints, err := runner.ListCheckpoints(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, checkpoints)
}

func (s *Server) handleGetCheckpoint(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	checkpointID := strings.TrimSpace(c.Param("checkpoint_id"))
	if checkpointID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("checkpoint_id is required"))
		return
	}
	checkpoint, err := runner.LoadCheckpointState(c.Request.Context(), checkpointID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, checkpoint)
}

func (s *Server) handleListEvents(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	events, err := runner.ListEvents(runID)
	if err != nil {
		writeError(c, statusForListEventsError(err), err)
		return
	}
	writeData(c, http.StatusOK, events)
}

func (s *Server) requireRunner(c *gin.Context) *runtime.GraphRunner {
	runner := s.currentRunner()
	if runner == nil {
		writeError(c, http.StatusServiceUnavailable, errRunnerNotConfigured)
		return nil
	}
	return runner
}

func (s *Server) makeRunResult(ctx context.Context, runner *runtime.GraphRunner, run runtime.RunRecord, finalState *state.State) runResult {
	return runResult{
		Run:       run,
		State:     finalState,
		Interrupt: s.buildRunInterrupt(ctx, runner, run),
	}
}

func (s *Server) buildRunInterrupt(ctx context.Context, runner *runtime.GraphRunner, run runtime.RunRecord) *runInterrupt {
	if runner == nil || run.Status != runtime.RunStatusPaused || strings.TrimSpace(run.LastCheckpointID) == "" {
		return nil
	}
	interrupt := &runInterrupt{
		RunID:                  run.RunID,
		CheckpointID:           run.LastCheckpointID,
		NodeID:                 run.CurrentNodeID,
		ResumeFromRunID:        run.RunID,
		ResumeFromCheckpointID: run.LastCheckpointID,
		Message:                "run paused",
	}
	checkpoint, err := runner.LoadCheckpointState(ctx, run.LastCheckpointID)
	if err != nil {
		interrupt.Message = fmt.Sprintf("run paused; checkpoint details unavailable: %v", err)
		return interrupt
	}

	interrupt.StepID = checkpoint.Record.StepID
	interrupt.NodeID = firstNonEmpty(checkpoint.Record.NodeID, checkpoint.Runtime.CurrentNodeID, run.CurrentNodeID)
	interrupt.Stage = string(checkpoint.Record.Stage)
	interrupt.BreakpointHit = checkpoint.Runtime.BreakpointHit
	runtimeState := checkpoint.Runtime
	interrupt.Runtime = &runtimeState
	interrupt.Message = interruptMessage(checkpoint)
	return interrupt
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
	base := context.Background()
	if s != nil && s.baseCtx != nil {
		base = s.baseCtx
	}
	ctx, cancel := context.WithCancel(base)
	requestDone := c.Request.Context().Done()
	go func() {
		select {
		case <-requestDone:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func bindStatePayload(c *gin.Context, keys ...string) (*state.State, error) {
	body, err := io.ReadAll(c.Request.Body)
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
