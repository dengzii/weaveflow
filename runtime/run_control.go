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
	executionStore   ExecutionStore
	transactionStore TransactionStore
	eventSink        EventSink
	runDeleter       RunDeleter
	now              func() time.Time
}

func NewRunControlService(executionStore ExecutionStore, transactionStore TransactionStore, eventSink EventSink, runDeleter RunDeleter) (*RunControlService, error) {
	if executionStore == nil {
		return nil, fmt.Errorf("execution store is required")
	}
	if transactionStore == nil {
		return nil, fmt.Errorf("runtime transaction store is required")
	}
	if eventSink == nil {
		eventSink = NoopEventSink{}
	}
	return &RunControlService{
		executionStore:   executionStore,
		transactionStore: transactionStore,
		eventSink:        eventSink,
		runDeleter:       runDeleter,
		now:              time.Now,
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
	revisionConflicts := 0
	for {
		run, err := s.executionStore.GetRun(ctx, runID)
		if err != nil {
			return RunRecord{}, err
		}
		if run.Status != RunStatusPending && run.Status != RunStatusRunning {
			return run, nil
		}
		now := s.now()
		if IsExecutionLeaseActive(run, now) {
			return run, fmt.Errorf("%w: run %q is owned by execution lease %d until %s", ErrRunControlNotAllowed, run.RunID, run.ExecutionLease.Epoch, run.ExecutionLease.ExpiresAt.Format(time.RFC3339Nano))
		}
		if run.ExecutionLease != nil && run.ExecutionLease.Status == ExecutionLeaseActive {
			lease := *run.ExecutionLease
			lease.Status = ExecutionLeaseReleased
			lease.HeartbeatAt = now
			lease.ExpiresAt = now
			run.ExecutionLease = &lease
			if _, err := s.executionStore.CompareAndSwapRun(withExecutionLeaseMutation(ctx, executionLeaseRelease, now), run.Revision, run); errors.Is(err, ErrRunRevisionConflict) {
				revisionConflicts++
				if revisionConflicts >= runRevisionRetryLimit {
					return RunRecord{}, runRevisionRetriesExceeded("release expired execution lease")
				}
				continue
			} else if err != nil {
				return RunRecord{}, err
			}
			continue
		}
		run.Status = RunStatusFailed
		run.PauseRequested = false
		run.CancelRequested = false
		run.ErrorCode = "run_execution_lost"
		run.ErrorMessage = "run execution is no longer active in this server process"
		run.UpdatedAt = now
		run.FinishedAt = &now
		failedEvent, err := s.buildEvent(run, EventRunFailed, map[string]any{
			"error_code":    run.ErrorCode,
			"error_message": run.ErrorMessage,
		})
		if err != nil {
			return RunRecord{}, err
		}
		commitResult, err := s.commit(ctx, Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
			Events: []Event{failedEvent},
		})
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunRecord{}, runRevisionRetriesExceeded("mark run execution lost")
			}
			continue
		}
		if err != nil {
			return RunRecord{}, err
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		return run, nil
	}
}

func (s *RunControlService) CancelPausedRun(ctx context.Context, runID string) (RunRecord, error) {
	if err := s.validate(); err != nil {
		return RunRecord{}, err
	}
	ctx = normalizeRunnerContext(ctx)
	runID = strings.TrimSpace(runID)
	return s.cancelPausedRun(ctx, runID, "", true, make(map[string]runCancelVisit))
}

type runCancelVisit struct {
	run      RunRecord
	visiting bool
	done     bool
}

func (s *RunControlService) cancelPausedRun(ctx context.Context, runID, expectedParentRunID string, root bool, visits map[string]runCancelVisit) (RunRecord, error) {
	if visit := visits[runID]; visit.visiting {
		return RunRecord{}, fmt.Errorf("child run lineage cycle includes %q", runID)
	} else if visit.done {
		return visit.run, nil
	}
	revisionConflicts := 0
	for {
		run, err := s.executionStore.GetRun(ctx, runID)
		if err != nil {
			return RunRecord{}, err
		}
		if expectedParentRunID != "" && run.ParentRunID != expectedParentRunID {
			return RunRecord{}, fmt.Errorf("child run %q parent is %q, want %q", run.RunID, run.ParentRunID, expectedParentRunID)
		}
		visits[runID] = runCancelVisit{run: run, visiting: true}
		for _, childRunID := range run.ChildRunIDs {
			childRunID = strings.TrimSpace(childRunID)
			if childRunID == "" {
				return RunRecord{}, fmt.Errorf("run %q has an empty child run ID", run.RunID)
			}
			if _, err := s.cancelPausedRun(ctx, childRunID, run.RunID, false, visits); err != nil {
				return RunRecord{}, fmt.Errorf("cancel child run %q of %q: %w", childRunID, run.RunID, err)
			}
		}
		if run.Status == RunStatusCanceled {
			visits[runID] = runCancelVisit{run: run, done: true}
			return run, nil
		}
		if run.Status != RunStatusPaused {
			if !root && (run.Status == RunStatusCompleted || run.Status == RunStatusFailed) {
				visits[runID] = runCancelVisit{run: run, done: true}
				return run, nil
			}
			return RunRecord{}, fmt.Errorf("%w: run %q status %q cannot be canceled without an active runner", ErrRunControlNotAllowed, runID, run.Status)
		}
		now := s.now()
		run.PauseRequested = false
		run.Status = RunStatusCanceled
		run.CancelRequested = false
		run.UpdatedAt = now
		run.FinishedAt = &now
		requestedEvent, err := s.buildEvent(run, EventRunCancelRequested, nil)
		if err != nil {
			return RunRecord{}, err
		}
		canceledEvent, err := s.buildEvent(run, EventRunCanceled, nil)
		if err != nil {
			return RunRecord{}, err
		}
		commitResult, err := s.commit(ctx, Commit{
			Run:    &RunWrite{Mode: RunWriteUpdate, Run: run},
			Events: []Event{requestedEvent, canceledEvent},
		})
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return RunRecord{}, runRevisionRetriesExceeded("cancel paused run")
			}
			continue
		}
		if err != nil {
			return RunRecord{}, err
		}
		if commitResult.Run != nil {
			run = *commitResult.Run
		}
		visits[runID] = runCancelVisit{run: run, done: true}
		return run, nil
	}
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
	if s.transactionStore == nil {
		return errors.New("run control runtime transaction store is nil")
	}
	if s.now == nil {
		return errors.New("run control now function is nil")
	}
	return nil
}

func (s *RunControlService) buildEvent(run RunRecord, eventType EventType, payload any) (Event, error) {
	event := Event{
		ID:             newRunnerID(),
		GraphID:        run.GraphID,
		GraphSessionID: run.GraphSessionID,
		RunID:          run.RunID,
		ParentRunID:    run.ParentRunID,
		ParentStepID:   run.ParentStepID,
		ParentTaskID:   run.ParentTaskID,
		RootRunID:      run.RootRunID,
		RunPath:        append([]string(nil), run.RunPath...),
		Namespace:      run.Namespace,
		NodeID:         run.CurrentNodeID,
		Type:           eventType,
		Timestamp:      s.now(),
	}
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return Event{}, err
		}
		event.Payload = encoded
	}
	return event, nil
}

func (s *RunControlService) commit(ctx context.Context, commit Commit) (CommitResult, error) {
	commit = sanitizeCommit(ctx, commit)
	if commit.Run != nil && commit.Run.Mode != RunWriteCreate {
		if guard, ok := executionLeaseGuard(commit.Run.Run); ok {
			commit.Lease = &guard
		}
	}
	result, committed, err := commitAndResolve(ctx, s.transactionStore, commit)
	if err != nil {
		return result, err
	}
	observeCommittedEvents(ctx, s.eventSink, s.transactionStore, committed.Events)
	return result, nil
}
