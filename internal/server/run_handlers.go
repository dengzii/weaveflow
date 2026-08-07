package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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

type runListPage struct {
	Items      []runtime.RunRecord `json:"items"`
	NextCursor string              `json:"next_cursor"`
}

type runInspectionResponse struct {
	Run         runtime.RunRecord          `json:"run"`
	Steps       []runtime.StepRecord       `json:"steps"`
	Checkpoints []runtime.CheckpointRecord `json:"checkpoints"`
	Events      runtime.EventPage          `json:"events"`
	Interrupt   *runInterrupt              `json:"interrupt,omitempty"`
}

const (
	defaultEventPageLimit = 500
	maximumEventPageLimit = 2000
)

const maxRunStateBodyBytes int64 = 8 << 20

func (s *Server) handleStartRun(c *gin.Context) {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	sessionID, ok := requirePathParam(c, "session_id")
	if !ok {
		return
	}
	initialState, err := decodeStartRunRequest(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	session, err := s.loadGraphSession(graphID, sessionID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	ctx, cancel := deriveRunContextFromBase(context.Background(), session.baseContext)
	run, done, err := session.runner.StartAsync(ctx, initialState)
	if err != nil {
		cancel()
		writeError(c, statusForError(err), err)
		return
	}
	go func() {
		if done != nil {
			<-done
		}
		cancel()
	}()
	writeData(c, http.StatusAccepted, run)
}

func (s *Server) handleResumeRun(c *gin.Context) {
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	input, err := decodeResumeRunRequest(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	runner := s.requireRunControlRunner(c, runID)
	if runner == nil {
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

func (s *Server) handlePauseRun(c *gin.Context) {
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	runner := s.requireRunControlRunner(c, runID)
	if runner == nil {
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
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	runner, err := s.runControlRunner(c.Request.Context(), graphID, runID)
	if err == nil && runner != nil {
		if err := runner.Cancel(c.Request.Context(), runID); err != nil {
			writeError(c, statusForError(err), err)
			return
		}
		run, err := waitForRunStatus(c.Request.Context(), runner, runID, runtime.RunStatusCanceled)
		if err != nil {
			writeError(c, statusForError(err), err)
			return
		}
		writeData(c, http.StatusOK, run)
		return
	}
	if graphID != "" {
		if cachedRun, cachedErr := s.cancelCachedPausedRun(c.Request.Context(), graphID, runID); cachedErr == nil {
			writeData(c, http.StatusOK, cachedRun)
			return
		} else if !errors.Is(cachedErr, runtime.ErrRunnerRecordNotFound) {
			writeError(c, statusForError(cachedErr), cachedErr)
			return
		}
	}
	if err == nil {
		err = runtime.ErrRunnerRecordNotFound
	}
	writeError(c, statusForError(err), err)
}

func (s *Server) runControlRunner(ctx context.Context, graphID string, runID string) (*runtime.GraphRunner, error) {
	graphID = strings.TrimSpace(graphID)
	runner, run, err := s.runtime.runnerForRun(ctx, graphID, runID)
	if err != nil {
		return nil, err
	}
	if runner != nil {
		if (run.Status == runtime.RunStatusPending || run.Status == runtime.RunStatusRunning) && !runner.IsRunActive(runID) && isRunPastRegistrationGrace(run) {
			if _, err := runner.MarkRunExecutionLost(ctx, runID); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%w: run %q execution is no longer active", runtime.ErrRunControlNotAllowed, runID)
		}
		return runner, nil
	}
	if graphID == "" {
		return nil, runtime.ErrRunnerRecordNotFound
	}
	cache, err := s.openGraphCache(graphID)
	if err != nil {
		return nil, err
	}
	index, run, err := cache.locateRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	switch run.Status {
	case runtime.RunStatusPending, runtime.RunStatusRunning:
		if !isRunPastRegistrationGrace(run) {
			return nil, fmt.Errorf("%w: run %q execution is not registered yet", runtime.ErrRunControlNotAllowed, runID)
		}
		if _, err := s.markCachedRunExecutionLost(ctx, cache, index, runID); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: run %q execution is no longer active", runtime.ErrRunControlNotAllowed, runID)
	case runtime.RunStatusPaused:
		session, err := s.loadGraphSession(graphID, run.GraphSessionID)
		if err != nil {
			return nil, err
		}
		return session.runner, nil
	default:
		return nil, fmt.Errorf("%w: run %q status %q cannot be controlled", runtime.ErrRunControlNotAllowed, runID, run.Status)
	}
}

func isRunPastRegistrationGrace(run runtime.RunRecord) bool {
	return run.StartedAt.IsZero() || !run.StartedAt.After(time.Now().Add(-runExecutionRegistrationGracePeriod))
}

func (s *Server) requireRunControlRunner(c *gin.Context, runID string) *runtime.GraphRunner {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return nil
	}
	runner, err := s.runControlRunner(c.Request.Context(), graphID, runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return nil
	}
	return runner
}

func (s *Server) handleDeleteRun(c *gin.Context) {
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	runner, _, err := s.runtime.runnerForRun(c.Request.Context(), graphID, runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	if runner != nil {
		_, err = runner.DeleteRun(c.Request.Context(), runID)
		if err == nil {
			c.Status(http.StatusNoContent)
			return
		}
		if !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
			writeError(c, statusForError(err), err)
			return
		}
	}

	_, err = s.deleteCachedRun(c.Request.Context(), graphID, runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	c.Status(http.StatusNoContent)
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
	statuses, err := parseRunStatuses(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runs, err := reader.ListRuns(c.Request.Context(), runtime.RunFilter{Statuses: statuses})
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	limit, err := positiveIntQuery(c, "limit", 100, 500)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	cursor, err := optionalStringQuery(c, "cursor")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	end := len(runs)
	if cursor != "" {
		end, err = strconv.Atoi(cursor)
		if err != nil || end < 0 || end > len(runs) {
			writeError(c, http.StatusBadRequest, invalidRequestf("cursor is invalid"))
			return
		}
	}
	start := max(0, end-limit)
	nextCursor := ""
	if start > 0 {
		nextCursor = strconv.Itoa(start)
	}
	writeData(c, http.StatusOK, runListPage{Items: runs[start:end], NextCursor: nextCursor})
}

func (s *Server) handleGetRunInspection(c *gin.Context) {
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	limit, err := positiveIntQuery(c, "event_limit", defaultEventPageLimit, maximumEventPageLimit)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	eventCursor, err := optionalStringQuery(c, "event_cursor")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	reader := s.resolveRunReader(c)
	if reader == nil {
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
	events, err := reader.ListEventPage(runID, eventCursor, limit)
	if err != nil {
		writeError(c, statusForListEventsError(err), err)
		return
	}
	writeData(c, http.StatusOK, runInspectionResponse{
		Run:         run,
		Steps:       steps,
		Checkpoints: checkpoints,
		Events:      events,
		Interrupt:   s.buildRunInterrupt(c.Request.Context(), reader, run),
	})
}

func (s *Server) handleGetCheckpoint(c *gin.Context) {
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	checkpointID, ok := requireRecordIDPathParam(c, "checkpoint_id")
	if !ok {
		return
	}
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	checkpoint, err := reader.LoadCheckpointState(c.Request.Context(), checkpointID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	if checkpoint.Record.RunID != runID {
		writeError(c, http.StatusNotFound, runtime.ErrRunnerRecordNotFound)
		return
	}
	writeData(c, http.StatusOK, checkpoint)
}

func (s *Server) handleListEvents(c *gin.Context) {
	runID, ok := requireRecordIDPathParam(c, "run_id")
	if !ok {
		return
	}
	limit, err := positiveIntQuery(c, "limit", defaultEventPageLimit, maximumEventPageLimit)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	cursor, err := optionalStringQuery(c, "cursor")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	page, err := reader.ListEventPage(runID, cursor, limit)
	if err != nil {
		writeError(c, statusForListEventsError(err), err)
		return
	}
	writeData(c, http.StatusOK, page)
}
