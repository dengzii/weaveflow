package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrExecutionLeaseHeld = errors.New("execution lease is held")
	ErrExecutionLeaseLost = errors.New("execution lease is lost")
)

type ExecutionLeaseStatus string

const (
	ExecutionLeaseActive   ExecutionLeaseStatus = "active"
	ExecutionLeaseReleased ExecutionLeaseStatus = "released"
)

type ExecutionLease struct {
	OwnerID     string               `json:"owner_id"`
	Token       string               `json:"token"`
	Epoch       uint64               `json:"epoch"`
	Status      ExecutionLeaseStatus `json:"status"`
	AcquiredAt  time.Time            `json:"acquired_at"`
	HeartbeatAt time.Time            `json:"heartbeat_at"`
	ExpiresAt   time.Time            `json:"expires_at"`
}

type ExecutionLeaseGuard struct {
	RunID string `json:"run_id"`
	Token string `json:"token"`
	Epoch uint64 `json:"epoch"`
}

type executionLeaseMutationKind string

const (
	executionLeaseAcquire   executionLeaseMutationKind = "acquire"
	executionLeaseHeartbeat executionLeaseMutationKind = "heartbeat"
	executionLeaseRelease   executionLeaseMutationKind = "release"
)

type executionLeaseMutation struct {
	kind executionLeaseMutationKind
	now  time.Time
}

type executionLeaseMutationKey struct{}
type executionLeaseGuardKey struct{}

func withExecutionLeaseMutation(ctx context.Context, kind executionLeaseMutationKind, now time.Time) context.Context {
	ctx = normalizeRunnerContext(ctx)
	return context.WithValue(ctx, executionLeaseMutationKey{}, executionLeaseMutation{kind: kind, now: now.UTC()})
}

func withExecutionLeaseGuard(ctx context.Context, guard ExecutionLeaseGuard) context.Context {
	ctx = normalizeRunnerContext(ctx)
	return context.WithValue(ctx, executionLeaseGuardKey{}, guard)
}

func executionLeaseGuardFromContext(ctx context.Context) (ExecutionLeaseGuard, bool) {
	if ctx == nil {
		return ExecutionLeaseGuard{}, false
	}
	guard, ok := ctx.Value(executionLeaseGuardKey{}).(ExecutionLeaseGuard)
	return guard, ok && guard.RunID != "" && guard.Token != "" && guard.Epoch > 0
}

func executionLeaseGuard(run RunRecord) (ExecutionLeaseGuard, bool) {
	if run.ExecutionLease == nil || run.ExecutionLease.Status != ExecutionLeaseActive {
		return ExecutionLeaseGuard{}, false
	}
	return ExecutionLeaseGuard{RunID: run.RunID, Token: run.ExecutionLease.Token, Epoch: run.ExecutionLease.Epoch}, true
}

func validateExecutionLease(lease *ExecutionLease) error {
	if lease == nil {
		return nil
	}
	if err := validateRunnerStorageID("execution lease owner ID", strings.TrimSpace(lease.OwnerID)); err != nil {
		return err
	}
	if err := validateRunnerStorageID("execution lease token", strings.TrimSpace(lease.Token)); err != nil {
		return err
	}
	if lease.Epoch == 0 {
		return errors.New("execution lease epoch must be greater than zero")
	}
	switch lease.Status {
	case ExecutionLeaseActive, ExecutionLeaseReleased:
	default:
		return fmt.Errorf("unsupported execution lease status %q", lease.Status)
	}
	if lease.AcquiredAt.IsZero() || lease.HeartbeatAt.IsZero() || lease.ExpiresAt.IsZero() {
		return errors.New("execution lease timestamps are required")
	}
	if lease.HeartbeatAt.Before(lease.AcquiredAt) {
		return errors.New("execution lease heartbeat precedes acquisition")
	}
	if lease.Status == ExecutionLeaseActive && !lease.ExpiresAt.After(lease.HeartbeatAt) {
		return errors.New("active execution lease expiry must follow heartbeat")
	}
	return nil
}

func validateExecutionLeaseGuard(run RunRecord, guard ExecutionLeaseGuard) error {
	if err := validateRunnerStorageID("execution lease guard run ID", guard.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("execution lease guard token", guard.Token); err != nil {
		return err
	}
	if guard.Epoch == 0 {
		return errors.New("execution lease guard epoch must be greater than zero")
	}
	lease := run.ExecutionLease
	if run.RunID != guard.RunID || lease == nil || lease.Status != ExecutionLeaseActive || lease.Token != guard.Token || lease.Epoch != guard.Epoch {
		return fmt.Errorf("%w for run %q", ErrExecutionLeaseLost, guard.RunID)
	}
	return nil
}

func validateCommitExecutionLease(run RunRecord, commit Commit) error {
	if commit.Run != nil && commit.Run.Mode == RunWriteCreate && commit.Run.Run.RunID == run.RunID {
		return nil
	}
	if commit.Lease != nil {
		return validateExecutionLeaseGuard(run, *commit.Lease)
	}
	if run.ExecutionLease != nil && run.ExecutionLease.Status == ExecutionLeaseActive {
		return fmt.Errorf("%w: runtime commit for run %q has no guard", ErrExecutionLeaseLost, run.RunID)
	}
	return nil
}

func validateRunExecutionLeaseTransition(ctx context.Context, existing, next RunRecord) error {
	if err := validateExecutionLease(existing.ExecutionLease); err != nil {
		return fmt.Errorf("existing run %q execution lease: %w", existing.RunID, err)
	}
	if err := validateExecutionLease(next.ExecutionLease); err != nil {
		return fmt.Errorf("run %q execution lease: %w", next.RunID, err)
	}
	if _, deleting := normalizeRunnerContext(ctx).Value(runDeletionMutationKey{}).(string); deleting {
		if !executionLeasesEqual(existing.ExecutionLease, next.ExecutionLease) {
			return fmt.Errorf("%w: deletion cannot change run %q execution lease", ErrRunControlNotAllowed, existing.RunID)
		}
		return nil
	}
	mutation, mutating := normalizeRunnerContext(ctx).Value(executionLeaseMutationKey{}).(executionLeaseMutation)
	if !mutating {
		if !executionLeasesEqual(existing.ExecutionLease, next.ExecutionLease) {
			return fmt.Errorf("%w: run %q execution lease cannot change outside a lease mutation", ErrRunControlNotAllowed, existing.RunID)
		}
		if existing.ExecutionLease != nil && existing.ExecutionLease.Status == ExecutionLeaseActive {
			guard, ok := executionLeaseGuardFromContext(ctx)
			if !ok {
				return fmt.Errorf("%w: run %q requires an execution lease guard", ErrExecutionLeaseLost, existing.RunID)
			}
			return validateExecutionLeaseGuard(existing, guard)
		}
		return nil
	}
	return validateExecutionLeaseMutation(existing, next, mutation)
}

func validateExecutionLeaseMutation(existing, next RunRecord, mutation executionLeaseMutation) error {
	previous := existing.ExecutionLease
	lease := next.ExecutionLease
	switch mutation.kind {
	case executionLeaseAcquire:
		if lease == nil || lease.Status != ExecutionLeaseActive {
			return errors.New("execution lease acquisition requires an active lease")
		}
		if previous == nil {
			if lease.Epoch != 1 {
				return errors.New("initial execution lease epoch must be one")
			}
			return nil
		}
		if previous.Status == ExecutionLeaseActive && previous.ExpiresAt.After(mutation.now) {
			return fmt.Errorf("%w by owner %q until %s", ErrExecutionLeaseHeld, previous.OwnerID, previous.ExpiresAt.Format(time.RFC3339Nano))
		}
		if lease.Epoch != previous.Epoch+1 {
			return errors.New("execution lease takeover must increment epoch")
		}
		if lease.Token == previous.Token {
			return errors.New("execution lease takeover must rotate token")
		}
		return nil
	case executionLeaseHeartbeat:
		if previous == nil || lease == nil || previous.Status != ExecutionLeaseActive || lease.Status != ExecutionLeaseActive {
			return fmt.Errorf("%w: heartbeat requires an active lease", ErrExecutionLeaseLost)
		}
		if previous.OwnerID != lease.OwnerID || previous.Token != lease.Token || previous.Epoch != lease.Epoch || !previous.AcquiredAt.Equal(lease.AcquiredAt) {
			return fmt.Errorf("%w: heartbeat identity changed", ErrExecutionLeaseLost)
		}
		if lease.HeartbeatAt.Before(previous.HeartbeatAt) || lease.ExpiresAt.Before(previous.ExpiresAt) {
			return errors.New("execution lease heartbeat cannot move backward")
		}
		return nil
	case executionLeaseRelease:
		if previous == nil || lease == nil || previous.Status != ExecutionLeaseActive || lease.Status != ExecutionLeaseReleased {
			return fmt.Errorf("%w: release requires an active lease", ErrExecutionLeaseLost)
		}
		if previous.OwnerID != lease.OwnerID || previous.Token != lease.Token || previous.Epoch != lease.Epoch || !previous.AcquiredAt.Equal(lease.AcquiredAt) {
			return fmt.Errorf("%w: release identity changed", ErrExecutionLeaseLost)
		}
		return nil
	default:
		return fmt.Errorf("unsupported execution lease mutation %q", mutation.kind)
	}
}

func executionLeasesEqual(left, right *ExecutionLease) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.OwnerID == right.OwnerID && left.Token == right.Token && left.Epoch == right.Epoch && left.Status == right.Status &&
		left.AcquiredAt.Equal(right.AcquiredAt) && left.HeartbeatAt.Equal(right.HeartbeatAt) && left.ExpiresAt.Equal(right.ExpiresAt)
}

func executionLeaseIdentitiesEqual(left, right *ExecutionLease) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.OwnerID == right.OwnerID && left.Token == right.Token && left.Epoch == right.Epoch && left.Status == right.Status
}

func IsExecutionLeaseActive(run RunRecord, now time.Time) bool {
	lease := run.ExecutionLease
	return lease != nil && lease.Status == ExecutionLeaseActive && lease.ExpiresAt.After(now)
}
