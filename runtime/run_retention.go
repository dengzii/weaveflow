package runtime

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type RunRetentionPolicy struct {
	MaxRuns int           `json:"max_runs,omitempty"`
	MaxAge  time.Duration `json:"max_age,omitempty"`
}

type RetentionAuditRecord struct {
	RunID          string             `json:"run_id"`
	GraphID        string             `json:"graph_id,omitempty"`
	GraphSessionID string             `json:"graph_session_id,omitempty"`
	Action         string             `json:"action"`
	Reason         string             `json:"reason"`
	Policy         RunRetentionPolicy `json:"policy"`
	RecordedAt     time.Time          `json:"recorded_at"`
}

type RetentionAuditSink interface {
	RecordRetention(context.Context, RetentionAuditRecord) error
}

func validateRunRetentionPolicy(policy RunRetentionPolicy) error {
	if policy.MaxRuns < 0 {
		return fmt.Errorf("run retention max runs cannot be negative")
	}
	if policy.MaxAge < 0 {
		return fmt.Errorf("run retention max age cannot be negative")
	}
	return nil
}

func retentionCandidates(runs []RunRecord, policy RunRetentionPolicy, now time.Time) map[string]string {
	terminal := make([]RunRecord, 0, len(runs))
	for _, run := range runs {
		if isTerminalRunStatus(run.Status) {
			terminal = append(terminal, run)
		}
	}
	sort.Slice(terminal, func(left, right int) bool {
		if terminal[left].UpdatedAt.Equal(terminal[right].UpdatedAt) {
			return terminal[left].RunID < terminal[right].RunID
		}
		return terminal[left].UpdatedAt.Before(terminal[right].UpdatedAt)
	})
	candidates := make(map[string]string)
	if policy.MaxAge > 0 {
		cutoff := now.Add(-policy.MaxAge)
		for _, run := range terminal {
			if run.UpdatedAt.Before(cutoff) {
				candidates[run.RunID] = "max_age"
			}
		}
	}
	if policy.MaxRuns > 0 && len(terminal) > policy.MaxRuns {
		for _, run := range terminal[:len(terminal)-policy.MaxRuns] {
			if candidates[run.RunID] == "" {
				candidates[run.RunID] = "max_runs"
			}
		}
	}
	return candidates
}

func isTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusCanceled:
		return true
	default:
		return false
	}
}
