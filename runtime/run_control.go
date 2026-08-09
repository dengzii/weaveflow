package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunControlService applies control transitions to persisted runs when no
// in-process GraphRunner owns the execution.
type RunControlService struct {
	executionStore ExecutionStore
	eventSink      EventSink
	runDeleter     RunDeleter
	now            func() time.Time
}

func NewRunControlService(executionStore ExecutionStore, eventSink EventSink, runDeleter RunDeleter) (*RunControlService, error) {
	if executionStore == nil {
		return nil, fmt.Errorf("execution store is required")
	}
	if eventSink == nil {
		eventSink = NoopEventSink{}
	}
	return &RunControlService{
		executionStore: executionStore,
		eventSink:      eventSink,
		runDeleter:     runDeleter,
		now:            time.Now,
	}, nil
}

// WithNow returns a copy of the service using the supplied clock.
func (s *RunControlService) WithNow(now func() time.Time) (*RunControlService, error) {
	if s == nil {
		return nil, errors.New("run control service is nil")
	}
	if now == nil {
		return nil, errors.New("now function is required")
	}
	clone := *s
	clone.now = now
	return &clone, nil
}

func (s *RunControlService) MarkRunExecutionLost(ctx context.Context, runID string) (RunRecord, error) {
	if err := s.validate(); err != nil {
		return RunRecord{}, err
	}
	ctx = normalizeRunnerContext(ctx)
	runID = strings.TrimSpace(runID)
	run, err := s.executionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if run.Status != RunStatusPending && run.Status != RunStatusRunning {
		return run, nil
	}
	now := s.now()
	run.Status = RunStatusFailed
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = "run_execution_lost"
	run.ErrorMessage = "run execution is no longer active in this server process"
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := s.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	if err := s.publish(ctx, run, EventRunFailed, map[string]any{
		"error_code":    run.ErrorCode,
		"error_message": run.ErrorMessage,
	}); err != nil {
		return run, err
	}
	return run, nil
}

func (s *RunControlService) CancelPausedRun(ctx context.Context, runID string) (RunRecord, error) {
	if err := s.validate(); err != nil {
		return RunRecord{}, err
	}
	ctx = normalizeRunnerContext(ctx)
	runID = strings.TrimSpace(runID)
	run, err := s.executionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if run.Status == RunStatusCanceled {
		return run, nil
	}
	if run.Status != RunStatusPaused {
		return RunRecord{}, fmt.Errorf("%w: run %q status %q cannot be canceled without an active runner", ErrRunControlNotAllowed, runID, run.Status)
	}
	now := s.now()
	run.PauseRequested = false
	run.CancelRequested = true
	run.UpdatedAt = now
	if err := s.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	if err := s.publish(ctx, run, EventRunCancelRequested, nil); err != nil {
		return RunRecord{}, err
	}
	run.Status = RunStatusCanceled
	run.CancelRequested = false
	run.UpdatedAt = now
	run.FinishedAt = &now
	if err := s.executionStore.UpdateRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	if err := s.publish(ctx, run, EventRunCanceled, nil); err != nil {
		return run, err
	}
	return run, nil
}

func (s *RunControlService) DeleteRun(ctx context.Context, runID string) (RunRecord, error) {
	if err := s.validate(); err != nil {
		return RunRecord{}, err
	}
	ctx = normalizeRunnerContext(ctx)
	runID = strings.TrimSpace(runID)
	run, err := s.executionStore.GetRun(ctx, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if isActiveDeleteRunStatus(run.Status) {
		return RunRecord{}, fmt.Errorf("%w: run %q status %q must be stopped before deletion", ErrRunControlNotAllowed, runID, run.Status)
	}
	if s.runDeleter == nil {
		return RunRecord{}, fmt.Errorf("run deletion is not configured")
	}
	if err := s.runDeleter.DeleteRun(ctx, runID); err != nil {
		return RunRecord{}, err
	}
	return run, nil
}

func (s *RunControlService) validate() error {
	if s == nil {
		return errors.New("run control service is nil")
	}
	if s.executionStore == nil {
		return errors.New("run control execution store is nil")
	}
	if s.now == nil {
		return errors.New("run control now function is nil")
	}
	return nil
}

func (s *RunControlService) publish(ctx context.Context, run RunRecord, eventType EventType, payload any) error {
	event := Event{
		ID:             newRunnerID(),
		GraphID:        run.GraphID,
		GraphSessionID: run.GraphSessionID,
		RunID:          run.RunID,
		NodeID:         run.CurrentNodeID,
		Type:           eventType,
		Timestamp:      s.now(),
	}
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event.Payload = encoded
	}
	return s.eventSink.Publish(ctx, event)
}
