package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type leaseHeartbeat struct {
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	err      error
}

func (heartbeat *leaseHeartbeat) finish() error {
	if heartbeat == nil {
		return nil
	}
	heartbeat.stopOnce.Do(func() { close(heartbeat.stop) })
	<-heartbeat.done
	heartbeat.mu.Lock()
	defer heartbeat.mu.Unlock()
	return heartbeat.err
}

func (r *GraphRunner) newExecutionLease(previous *ExecutionLease, now time.Time) *ExecutionLease {
	epoch := uint64(1)
	if previous != nil {
		epoch = previous.Epoch + 1
	}
	now = now.UTC()
	return &ExecutionLease{
		OwnerID: r.executionLeaseOwnerID(), Token: newRunnerID(), Epoch: epoch, Status: ExecutionLeaseActive,
		AcquiredAt: now, HeartbeatAt: now, ExpiresAt: now.Add(r.executionLeaseTTL()),
	}
}

func (r *GraphRunner) acquireExecutionLease(ctx context.Context, runID string) (RunRecord, ExecutionLeaseGuard, error) {
	ctx = normalizeRunnerContext(ctx)
	revisionConflicts := 0
	for {
		run, err := r.executionStore.GetRun(ctx, runID)
		if err != nil {
			return RunRecord{}, ExecutionLeaseGuard{}, err
		}
		if run.Deletion != nil {
			return run, ExecutionLeaseGuard{}, fmt.Errorf("%w: run %q is reserved for deletion", ErrRunControlNotAllowed, runID)
		}
		if isTerminalRunStatus(run.Status) {
			return run, ExecutionLeaseGuard{}, nil
		}
		now := r.currentTime()
		if run.ExecutionLease != nil && run.ExecutionLease.Status == ExecutionLeaseActive && run.ExecutionLease.ExpiresAt.After(now) {
			return run, ExecutionLeaseGuard{}, fmt.Errorf("%w by owner %q until %s", ErrExecutionLeaseHeld, run.ExecutionLease.OwnerID, run.ExecutionLease.ExpiresAt.Format(time.RFC3339Nano))
		}
		run.ExecutionLease = r.newExecutionLease(run.ExecutionLease, now)
		updated, err := r.executionStore.CompareAndSwapRun(withExecutionLeaseMutation(ctx, executionLeaseAcquire, now), run.Revision, run)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return run, ExecutionLeaseGuard{}, runRevisionRetriesExceeded("acquire execution lease")
			}
			continue
		}
		if err != nil {
			return run, ExecutionLeaseGuard{}, err
		}
		guard, _ := executionLeaseGuard(updated)
		return updated, guard, nil
	}
}

func (r *GraphRunner) ensureExecutionLease(ctx context.Context, runID string) (RunRecord, ExecutionLeaseGuard, bool, error) {
	ctx = normalizeRunnerContext(ctx)
	if guard, ok := executionLeaseGuardFromContext(ctx); ok && guard.RunID == runID {
		run, err := r.executionStore.GetRun(ctx, runID)
		if err != nil {
			return RunRecord{}, ExecutionLeaseGuard{}, false, err
		}
		if err := validateExecutionLeaseGuard(run, guard); err != nil {
			return run, ExecutionLeaseGuard{}, false, err
		}
		return run, guard, false, nil
	}
	run, guard, err := r.acquireExecutionLease(ctx, runID)
	return run, guard, guard.RunID != "", err
}

func (r *GraphRunner) heartbeatExecutionLease(ctx context.Context, guard ExecutionLeaseGuard) error {
	ctx = normalizeRunnerContext(ctx)
	revisionConflicts := 0
	for {
		run, err := r.executionStore.GetRun(ctx, guard.RunID)
		if err != nil {
			return err
		}
		if run.Deletion != nil {
			return fmt.Errorf("%w: run %q is reserved for deletion", ErrExecutionLeaseLost, guard.RunID)
		}
		if err := validateExecutionLeaseGuard(run, guard); err != nil {
			return err
		}
		now := r.currentTime()
		lease := *run.ExecutionLease
		lease.HeartbeatAt = now
		lease.ExpiresAt = now.Add(r.executionLeaseTTL())
		run.ExecutionLease = &lease
		_, err = r.executionStore.CompareAndSwapRun(withExecutionLeaseMutation(ctx, executionLeaseHeartbeat, now), run.Revision, run)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("heartbeat execution lease")
			}
			continue
		}
		return err
	}
}

func (r *GraphRunner) releaseExecutionLease(ctx context.Context, guard ExecutionLeaseGuard) error {
	ctx = normalizeRunnerContext(ctx)
	revisionConflicts := 0
	for {
		run, err := r.executionStore.GetRun(ctx, guard.RunID)
		if err != nil {
			return err
		}
		if run.Deletion != nil {
			return fmt.Errorf("%w: run %q is reserved for deletion", ErrExecutionLeaseLost, guard.RunID)
		}
		if run.ExecutionLease != nil && run.ExecutionLease.Status == ExecutionLeaseReleased && run.ExecutionLease.Token == guard.Token && run.ExecutionLease.Epoch == guard.Epoch {
			return nil
		}
		if err := validateExecutionLeaseGuard(run, guard); err != nil {
			return err
		}
		now := r.currentTime()
		lease := *run.ExecutionLease
		lease.Status = ExecutionLeaseReleased
		lease.HeartbeatAt = now
		lease.ExpiresAt = now
		run.ExecutionLease = &lease
		_, err = r.executionStore.CompareAndSwapRun(withExecutionLeaseMutation(ctx, executionLeaseRelease, now), run.Revision, run)
		if errors.Is(err, ErrRunRevisionConflict) {
			revisionConflicts++
			if revisionConflicts >= runRevisionRetryLimit {
				return runRevisionRetriesExceeded("release execution lease")
			}
			continue
		}
		return err
	}
}

func (r *GraphRunner) startLeaseHeartbeat(ctx context.Context, guard ExecutionLeaseGuard) (context.Context, *leaseHeartbeat) {
	guardedCtx := withExecutionLeaseGuard(normalizeRunnerContext(ctx), guard)
	heartbeatCtx, cancel := context.WithCancelCause(guardedCtx)
	heartbeat := &leaseHeartbeat{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(heartbeat.done)
		ticker := time.NewTicker(r.executionLeaseHeartbeatInterval())
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat.stop:
				return
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := r.heartbeatExecutionLease(context.WithoutCancel(heartbeatCtx), guard); err != nil {
					heartbeat.mu.Lock()
					heartbeat.err = err
					heartbeat.mu.Unlock()
					cancel(err)
					return
				}
			}
		}
	}()
	return heartbeatCtx, heartbeat
}

func (r *GraphRunner) executionLeaseOwnerID() string {
	if r != nil && strings.TrimSpace(r.leaseOwnerID) != "" {
		return r.leaseOwnerID
	}
	return "local-runner"
}

func (r *GraphRunner) executionLeaseTTL() time.Duration {
	if r != nil && r.leaseTTL > 0 {
		return r.leaseTTL
	}
	return 30 * time.Second
}

func (r *GraphRunner) executionLeaseHeartbeatInterval() time.Duration {
	if r != nil && r.leaseHeartbeat > 0 {
		return r.leaseHeartbeat
	}
	return 10 * time.Second
}

func (r *GraphRunner) finishExecutionLease(ctx context.Context, guard ExecutionLeaseGuard, heartbeat *leaseHeartbeat) error {
	heartbeatErr := heartbeat.finish()
	releaseErr := r.releaseExecutionLease(context.WithoutCancel(normalizeRunnerContext(ctx)), guard)
	return errors.Join(heartbeatErr, releaseErr)
}

func (r *GraphRunner) refreshRunAfterLease(ctx context.Context, run RunRecord, leaseErr error) (RunRecord, error) {
	if strings.TrimSpace(run.RunID) == "" {
		return run, leaseErr
	}
	latest, err := r.executionStore.GetRun(context.WithoutCancel(normalizeRunnerContext(ctx)), run.RunID)
	if err != nil {
		return run, errors.Join(leaseErr, err)
	}
	return latest, leaseErr
}
