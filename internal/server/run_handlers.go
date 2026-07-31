package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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

const maxRunStateBodyBytes int64 = 8 << 20

func (s *Server) handleStartRun(c *gin.Context) {
	runner := s.requireRunner(c)
	if runner == nil {
		return
	}
	initialState, err := bindStatePayload(c, "initial_state", "state", "input")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
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
		writeError(c, statusForRequestError(err), err)
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
		writeError(c, statusForRequestError(err), err)
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
	run, err := waitForRunStatus(c.Request.Context(), runner, runID, runtime.RunStatusPaused)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func (s *Server) handleCancelRun(c *gin.Context) {
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	graphID := strings.TrimSpace(c.Query("graph_id"))
	runner := s.currentRunner()
	if runner != nil && (graphID == "" || graphID == effectiveRunnerGraphID(runner)) {
		err := runner.Cancel(c.Request.Context(), runID)
		if err == nil {
			run, err := waitForRunStatus(c.Request.Context(), runner, runID, runtime.RunStatusCanceled)
			if err != nil {
				writeError(c, statusForError(err), err)
				return
			}
			writeData(c, http.StatusOK, run)
			return
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			writeError(c, statusForError(err), err)
			return
		}
	}

	run, err := s.cancelCachedPausedRun(c.Request.Context(), graphID, runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func (s *Server) handleDeleteRun(c *gin.Context) {
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	graphID := strings.TrimSpace(c.Query("graph_id"))
	runner := s.currentRunner()
	if runner != nil && (graphID == "" || graphID == effectiveRunnerGraphID(runner)) {
		run, err := runner.DeleteRun(c.Request.Context(), runID)
		if err == nil {
			writeData(c, http.StatusOK, run)
			return
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			writeError(c, statusForError(err), err)
			return
		}
	}

	run, err := s.deleteCachedRun(c.Request.Context(), graphID, runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func waitForRunStatus(ctx context.Context, runner *runtime.GraphRunner, runID string, target runtime.RunStatus) (runtime.RunRecord, error) {
	if runner == nil {
		return runtime.RunRecord{}, errRunnerNotConfigured
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		run, err := runner.GetRun(ctx, runID)
		if err != nil {
			return runtime.RunRecord{}, err
		}
		if run.Status == target {
			return run, nil
		}
		if isTerminalRunStatus(run.Status) {
			return run, fmt.Errorf("%w: run %q reached status %q before %q", runtime.ErrRunControlNotAllowed, runID, run.Status, target)
		}

		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isTerminalRunStatus(status runtime.RunStatus) bool {
	switch status {
	case runtime.RunStatusCompleted, runtime.RunStatusFailed, runtime.RunStatusCanceled:
		return true
	default:
		return false
	}
}

func (s *Server) handleListRuns(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runs, err := reader.ListRuns(c.Request.Context(), runtime.RunFilter{Statuses: parseRunStatuses(c)})
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, runs)
}

func (s *Server) handleGetRun(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	run, err := reader.GetRun(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, run)
}

func (s *Server) handleGetRunDetail(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}

	run, err := reader.GetRun(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	steps, err := reader.ListSteps(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	checkpoints, err := reader.ListCheckpoints(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	events, err := reader.ListEvents(runID)
	if err != nil {
		writeError(c, statusForListEventsError(err), err)
		return
	}
	artifacts, err := reader.ListArtifacts(c.Request.Context(), runID)
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
		Interrupt:   s.buildRunInterrupt(c.Request.Context(), reader, run),
	})
}

func (s *Server) handleListSteps(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	steps, err := reader.ListSteps(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, steps)
}

func (s *Server) handleListCheckpoints(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	checkpoints, err := reader.ListCheckpoints(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, checkpoints)
}

func (s *Server) handleGetCheckpoint(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	checkpointID := strings.TrimSpace(c.Param("checkpoint_id"))
	if checkpointID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("checkpoint_id is required"))
		return
	}
	checkpoint, err := reader.LoadCheckpointState(c.Request.Context(), checkpointID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, checkpoint)
}

func (s *Server) handleListEvents(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	events, err := reader.ListEvents(runID)
	if err != nil {
		writeError(c, statusForListEventsError(err), err)
		return
	}
	writeData(c, http.StatusOK, events)
}
