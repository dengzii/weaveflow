package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

type FileRetentionAuditSink struct {
	path string
	mu   fileStoreMutex
}

func NewFileRetentionAuditSink(path string) *FileRetentionAuditSink {
	path = filepath.Clean(strings.TrimSpace(path))
	return &FileRetentionAuditSink{path: path, mu: fileStoreMutex{baseDir: path}}
}

func (sink *FileRetentionAuditSink) RecordRetention(ctx context.Context, record RetentionAuditRecord) error {
	if sink == nil || strings.TrimSpace(sink.path) == "" || sink.path == "." {
		return fmt.Errorf("retention audit path is required")
	}
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(sink.path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(sink.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (sink *FileRetentionAuditSink) List() ([]RetentionAuditRecord, error) {
	if sink == nil || strings.TrimSpace(sink.path) == "" || sink.path == "." {
		return nil, nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	data, err := os.ReadFile(sink.path)
	if os.IsNotExist(err) {
		return []RetentionAuditRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]RetentionAuditRecord, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record RetentionAuditRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
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
