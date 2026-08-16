package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingRetentionAudit struct {
	records []RetentionAuditRecord
	err     error
}

func (audit *recordingRetentionAudit) RecordRetention(_ context.Context, record RetentionAuditRecord) error {
	audit.records = append(audit.records, record)
	return audit.err
}

type recordingRetentionDeleter struct {
	runIDs []string
}

func (deleter *recordingRetentionDeleter) DeleteRun(_ context.Context, runID string) error {
	deleter.runIDs = append(deleter.runIDs, runID)
	return nil
}

func TestRunRetentionDeletesOnlyOldTerminalRunsAndAudits(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	executionStore := NewFileExecutionStore(directory)
	checkpointStore := NewFileCheckpointStore(directory)
	eventStore := NewFileEventSink(directory)
	artifactStore := NewFileArtifactStore(directory)
	deleter := NewRunDeletionCoordinator(executionStore, checkpointStore, eventStore, artifactStore)
	audit := &recordingRetentionAudit{}
	runner := &GraphRunner{
		executionStore:   executionStore,
		runDeleter:       deleter,
		retentionPolicy:  RunRetentionPolicy{MaxRuns: 2},
		retentionAudit:   audit,
		now:              func() time.Time { return time.Unix(100, 0) },
		activeExecutions: map[string]*graphRunnerExecution{},
	}
	for index, status := range []RunStatus{RunStatusCompleted, RunStatusFailed, RunStatusPaused} {
		run := RunRecord{
			RunID:     []string{"completed", "failed", "paused"}[index],
			GraphID:   "graph",
			Status:    status,
			StartedAt: time.Unix(int64(index+1), 0),
			UpdatedAt: time.Unix(int64(index+1), 0),
		}
		run.RootRunID = run.RunID
		if err := executionStore.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	newest := RunRecord{RunID: "newest", RootRunID: "newest", GraphID: "graph", Status: RunStatusCanceled, StartedAt: time.Unix(4, 0), UpdatedAt: time.Unix(4, 0)}
	if err := executionStore.CreateRun(ctx, newest); err != nil {
		t.Fatal(err)
	}
	if err := runner.applyRunRetention(ctx, newest.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := executionStore.GetRun(ctx, "completed"); err != ErrRunnerRecordNotFound {
		t.Fatalf("old terminal run was not removed: %v", err)
	}
	if _, err := executionStore.GetRun(ctx, "paused"); err != nil {
		t.Fatalf("paused run was removed: %v", err)
	}
	if len(audit.records) != 1 || audit.records[0].RunID != "completed" || audit.records[0].Reason != "max_runs" || audit.records[0].Action != "delete_intent" {
		t.Fatalf("audit records = %#v", audit.records)
	}
}

func TestRunRetentionAuditsIntentBeforeDeletion(t *testing.T) {
	ctx := context.Background()
	executionStore := NewMemoryExecutionStore()
	if err := executionStore.CreateRun(ctx, RunRecord{
		RunID: "old", Status: RunStatusCompleted, StartedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := executionStore.CreateRun(ctx, RunRecord{
		RunID: "new", Status: RunStatusCompleted, StartedAt: time.Unix(2, 0), UpdatedAt: time.Unix(2, 0),
	}); err != nil {
		t.Fatal(err)
	}
	auditFailure := errors.New("audit unavailable")
	audit := &recordingRetentionAudit{err: auditFailure}
	deleter := &recordingRetentionDeleter{}
	runner := &GraphRunner{
		executionStore: executionStore, runDeleter: deleter,
		retentionPolicy: RunRetentionPolicy{MaxRuns: 1}, retentionAudit: audit,
		now: func() time.Time { return time.Unix(3, 0) }, activeExecutions: map[string]*graphRunnerExecution{},
	}
	if err := runner.applyRunRetention(ctx, "new"); !errors.Is(err, auditFailure) {
		t.Fatalf("retention error = %v", err)
	}
	if len(deleter.runIDs) != 0 {
		t.Fatalf("deletion ran before durable audit: %#v", deleter.runIDs)
	}
}
