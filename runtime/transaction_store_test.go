package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type runtimeCommitReader interface {
	RuntimeTransactionStore
	GetRun(context.Context, string) (RunRecord, error)
	ListEvents(string) ([]Event, error)
}

type recordingRuntimeTransactionStore struct {
	commits []RuntimeCommit
}

func (store *recordingRuntimeTransactionStore) Commit(_ context.Context, commit RuntimeCommit) (RuntimeCommitResult, error) {
	store.commits = append(store.commits, commit)
	if commit.Run == nil {
		return RuntimeCommitResult{}, nil
	}
	run := commit.Run.Run
	run.Revision++
	return RuntimeCommitResult{Run: &run}, nil
}

func TestMemoryRuntimeStoreCommitRejectsStaleRunRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	original := RunRecord{RunID: "run-memory-cas", Status: RunStatusPending}
	if err := runtimeStore.CreateRun(ctx, original); err != nil {
		t.Fatalf("CreateRun(): %v", err)
	}

	firstUpdate := original
	firstUpdate.Status = RunStatusRunning
	firstResult, err := runtimeStore.Commit(ctx, RuntimeCommit{Run: &RunWrite{Mode: RunWriteUpdate, Run: firstUpdate}})
	if err != nil {
		t.Fatalf("first Commit(): %v", err)
	}
	if firstResult.Run == nil || firstResult.Run.Revision != 1 {
		t.Fatalf("first commit result = %#v", firstResult.Run)
	}

	staleUpdate := original
	staleUpdate.Status = RunStatusCompleted
	if _, err := runtimeStore.Commit(ctx, RuntimeCommit{Run: &RunWrite{Mode: RunWriteUpdate, Run: staleUpdate}}); !errors.Is(err, ErrRunRevisionConflict) {
		t.Fatalf("stale Commit() error = %v, want revision conflict", err)
	}
	persisted, err := runtimeStore.GetRun(ctx, original.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if persisted.Revision != 1 || persisted.Status != RunStatusRunning {
		t.Fatalf("persisted run = %#v", persisted)
	}
}

func TestRuntimeStoreCommitCreatesRunWithEventsAtomically(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) runtimeCommitReader
	}{
		{name: "memory", new: func(*testing.T) runtimeCommitReader { return NewMemoryRuntimeStore() }},
		{name: "file", new: func(t *testing.T) runtimeCommitReader {
			store, err := NewFileRuntimeStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileRuntimeStore(): %v", err)
			}
			return store
		}},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := testCase.new(t)
			run := RunRecord{RunID: "run-created-atomically", Status: RunStatusRunning}
			events := []Event{
				{ID: "event-run-created", RunID: run.RunID, Type: EventRunCreated},
				{ID: "event-run-started", RunID: run.RunID, Type: EventRunStarted},
			}
			result, err := store.Commit(context.Background(), RuntimeCommit{
				Run:    &RunWrite{Mode: RunWriteCreate, Run: run},
				Events: events,
			})
			if err != nil {
				t.Fatalf("Commit(create): %v", err)
			}
			if result.Run == nil || result.Run.Revision != 0 || result.Run.Status != RunStatusRunning {
				t.Fatalf("create result = %#v", result.Run)
			}
			persisted, err := store.GetRun(context.Background(), run.RunID)
			if err != nil {
				t.Fatalf("GetRun(): %v", err)
			}
			if persisted.Status != run.Status || persisted.Revision != 0 {
				t.Fatalf("persisted run = %#v", persisted)
			}
			persistedEvents, err := store.ListEvents(run.RunID)
			if err != nil {
				t.Fatalf("ListEvents(): %v", err)
			}
			if len(persistedEvents) != 2 || persistedEvents[0].Type != EventRunCreated || persistedEvents[1].Type != EventRunStarted {
				t.Fatalf("persisted events = %#v", persistedEvents)
			}

			duplicate := RuntimeCommit{
				Run:    &RunWrite{Mode: RunWriteCreate, Run: run},
				Events: []Event{{ID: "event-duplicate", RunID: run.RunID, Type: EventRunFailed}},
			}
			if _, err := store.Commit(context.Background(), duplicate); err == nil {
				t.Fatal("duplicate Commit(create) error = nil")
			}
			persistedEvents, err = store.ListEvents(run.RunID)
			if err != nil {
				t.Fatalf("ListEvents() after duplicate: %v", err)
			}
			if len(persistedEvents) != 2 {
				t.Fatalf("duplicate create appended events: %#v", persistedEvents)
			}
		})
	}
}

func TestGraphRunnerCommitsTerminalRunAndStepTransitionAtomically(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		stepStatus StepStatus
		nodeEvent  EventType
		runStatus  RunStatus
		runEvent   EventType
		commit     func(*GraphRunner, RunRecord, runnerStepTransition) error
	}{
		{
			name:       "failed",
			stepStatus: StepStatusFailed,
			nodeEvent:  EventNodeFailed,
			runStatus:  RunStatusFailed,
			runEvent:   EventRunFailed,
			commit: func(runner *GraphRunner, run RunRecord, transition runnerStepTransition) error {
				_, err := runner.persistRunFailureWithTransition(context.Background(), run, nil, "node_failed", "node failed", transition)
				return err
			},
		},
		{
			name:       "canceled",
			stepStatus: StepStatusCanceled,
			nodeEvent:  EventNodeCanceled,
			runStatus:  RunStatusCanceled,
			runEvent:   EventRunCanceled,
			commit: func(runner *GraphRunner, run RunRecord, transition runnerStepTransition) error {
				_, _, err := runner.cancelRunWithTransition(context.Background(), run, nil, transition)
				return err
			},
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingRuntimeTransactionStore{}
			runner := &GraphRunner{transactionStore: store}
			run := RunRecord{RunID: "terminal-run", Revision: 3, Status: RunStatusRunning, CurrentNodeID: "work"}
			step := StepRecord{RunID: run.RunID, StepID: "terminal-step", NodeID: "work", Status: testCase.stepStatus}
			transition := runnerStepTransition{
				writes: []StepWrite{{Mode: StepWriteUpdate, Step: step}},
				events: []Event{{ID: "terminal-node-event", RunID: run.RunID, StepID: step.StepID, NodeID: step.NodeID, Type: testCase.nodeEvent}},
				steps:  []StepRecord{step},
			}
			if err := testCase.commit(runner, run, transition); err != nil {
				t.Fatalf("terminal commit: %v", err)
			}
			if len(store.commits) != 1 {
				t.Fatalf("commit count = %d, want 1", len(store.commits))
			}
			commit := store.commits[0]
			if commit.Run == nil || commit.Run.Mode != RunWriteUpdate || commit.Run.Run.Status != testCase.runStatus {
				t.Fatalf("run write = %#v, want status %q", commit.Run, testCase.runStatus)
			}
			if len(commit.Steps) != 1 || commit.Steps[0].Mode != StepWriteUpdate || commit.Steps[0].Step.Status != testCase.stepStatus {
				t.Fatalf("step writes = %#v, want status %q", commit.Steps, testCase.stepStatus)
			}
			if len(commit.Events) != 2 || commit.Events[0].Type != testCase.nodeEvent || commit.Events[1].Type != testCase.runEvent {
				t.Fatalf("events = %#v, want %q before %q", commit.Events, testCase.nodeEvent, testCase.runEvent)
			}
		})
	}
}

func TestRuntimeStoreCommitRejectsStepWithoutRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		new  func(*testing.T) runtimeCommitReader
	}{
		{name: "memory", new: func(*testing.T) runtimeCommitReader { return NewMemoryRuntimeStore() }},
		{name: "file", new: func(t *testing.T) runtimeCommitReader {
			store, err := NewFileRuntimeStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileRuntimeStore(): %v", err)
			}
			return store
		}},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := testCase.new(t)
			step := StepRecord{RunID: "missing-run", StepID: "orphan-step", Status: StepStatusRunning}
			_, err := store.Commit(context.Background(), RuntimeCommit{
				Steps: []StepWrite{{Mode: StepWriteAppend, Step: step}},
			})
			if !errors.Is(err, ErrRunnerRecordNotFound) {
				t.Fatalf("Commit() error = %v, want record not found", err)
			}
		})
	}
}

func TestFileRuntimeStoreCommitRejectsStepMetadataIdentityMismatch(t *testing.T) {
	t.Parallel()

	runtimeStore, err := NewFileRuntimeStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileRuntimeStore(): %v", err)
	}
	run := RunRecord{RunID: "run-step-identity", Status: RunStatusRunning}
	if err := runtimeStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun(): %v", err)
	}
	step := StepRecord{RunID: run.RunID, StepID: "step-identity", Status: StepStatusRunning}
	if err := runtimeStore.AppendStep(context.Background(), step); err != nil {
		t.Fatalf("AppendStep(): %v", err)
	}
	corrupted := step
	corrupted.RunID = "different-run"
	if err := writeRunnerJSONFile(runtimeStore.execution.stepPath(run.RunID, step.StepID), corrupted); err != nil {
		t.Fatalf("write corrupted step: %v", err)
	}

	step.Status = StepStatusSucceeded
	_, err = runtimeStore.Commit(context.Background(), RuntimeCommit{
		Steps: []StepWrite{{Mode: StepWriteUpdate, Step: step}},
	})
	if err == nil || !strings.Contains(err.Error(), "metadata identity mismatch") {
		t.Fatalf("Commit() error = %v, want metadata identity mismatch", err)
	}
}

func TestMemoryRuntimeStoreCommitRollsBackEveryRecordOnLateFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runtimeStore := NewMemoryRuntimeStore()
	original := RunRecord{RunID: "run-memory-atomic", Status: RunStatusPending}
	if err := runtimeStore.CreateRun(ctx, original); err != nil {
		t.Fatalf("CreateRun(): %v", err)
	}

	updated := original
	updated.Status = RunStatusRunning
	step := StepRecord{RunID: original.RunID, StepID: "step-memory-atomic", Status: StepStatusSucceeded}
	checkpoint := CheckpointRecord{
		RunID: original.RunID, CheckpointID: "checkpoint-memory-atomic", StepID: step.StepID,
		Stage: CheckpointAfterNode,
	}
	commit := RuntimeCommit{
		Run:         &RunWrite{Mode: RunWriteUpdate, Run: updated},
		Steps:       []StepWrite{{Mode: StepWriteAppend, Step: step}},
		Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("checkpoint")}},
		Events: []Event{
			{ID: "event-memory-valid", RunID: original.RunID, Type: EventNodeFinished},
			{ID: "event-memory-invalid", RunID: "../invalid", Type: EventRunFinished},
		},
	}
	if _, err := runtimeStore.Commit(ctx, commit); err == nil {
		t.Fatal("Commit() error = nil")
	}

	persisted, err := runtimeStore.GetRun(ctx, original.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if persisted.Revision != original.Revision || persisted.Status != original.Status {
		t.Fatalf("run changed after rollback: %#v", persisted)
	}
	if _, err := runtimeStore.GetStep(ctx, step.StepID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetStep() error = %v, want record not found", err)
	}
	if _, _, err := runtimeStore.Load(ctx, checkpoint.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load() error = %v, want record not found", err)
	}
	events, err := runtimeStore.ListEvents(original.RunID)
	if err != nil {
		t.Fatalf("ListEvents(): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events survived rollback: %#v", events)
	}
}

func TestFileRuntimeStoreRecoveryRollsBackPreparedTransaction(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	runtimeStore, original, commit := prepareFileRuntimeTransaction(t, baseDir)
	journal, _, err := runtimeStore.prepareJournalLocked(commit)
	if err != nil {
		t.Fatalf("prepareJournalLocked(): %v", err)
	}
	journalPath := persistFileRuntimeJournal(t, runtimeStore, journal)
	if err := applyFileTransactionState(journal.Mutations, true); err != nil {
		t.Fatalf("apply transaction after-state: %v", err)
	}

	recovered, err := NewFileRuntimeStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileRuntimeStore() recovery: %v", err)
	}
	persisted, err := recovered.GetRun(context.Background(), original.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if persisted.Revision != original.Revision || persisted.Status != original.Status {
		t.Fatalf("prepared recovery retained run mutation: %#v", persisted)
	}
	assertFileRuntimeTransactionRecords(t, recovered, commit, false)
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("prepared journal still exists: %v", err)
	}
}

func TestFileRuntimeStoreRecoveryReplaysCommittedTransaction(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	runtimeStore, _, commit := prepareFileRuntimeTransaction(t, baseDir)
	journal, result, err := runtimeStore.prepareJournalLocked(commit)
	if err != nil {
		t.Fatalf("prepareJournalLocked(): %v", err)
	}
	journal.Phase = fileTransactionCommitted
	journalPath := persistFileRuntimeJournal(t, runtimeStore, journal)

	recovered, err := NewFileRuntimeStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileRuntimeStore() recovery: %v", err)
	}
	persisted, err := recovered.GetRun(context.Background(), commit.Run.Run.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if result.Run == nil || persisted.Revision != result.Run.Revision || persisted.Status != result.Run.Status {
		t.Fatalf("committed recovery run = %#v, want %#v", persisted, result.Run)
	}
	assertFileRuntimeTransactionRecords(t, recovered, commit, true)
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("committed journal still exists: %v", err)
	}
}

func prepareFileRuntimeTransaction(t *testing.T, baseDir string) (*FileRuntimeStore, RunRecord, RuntimeCommit) {
	t.Helper()

	runtimeStore, err := NewFileRuntimeStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileRuntimeStore(): %v", err)
	}
	original := RunRecord{RunID: "run-file-transaction", Status: RunStatusPending}
	if err := runtimeStore.CreateRun(context.Background(), original); err != nil {
		t.Fatalf("CreateRun(): %v", err)
	}
	updated := original
	updated.Status = RunStatusRunning
	step := StepRecord{RunID: original.RunID, StepID: "step-file-transaction", Status: StepStatusSucceeded}
	checkpoint := CheckpointRecord{
		RunID: original.RunID, CheckpointID: "checkpoint-file-transaction", StepID: step.StepID,
		Stage: CheckpointAfterNode,
	}
	commit := RuntimeCommit{
		Run:         &RunWrite{Mode: RunWriteUpdate, Run: updated},
		Steps:       []StepWrite{{Mode: StepWriteAppend, Step: step}},
		Checkpoints: []CheckpointWrite{{Record: checkpoint, Payload: []byte("checkpoint payload")}},
		Events:      []Event{{ID: "event-file-transaction", RunID: original.RunID, Type: EventNodeFinished}},
	}
	return runtimeStore, original, commit
}

func persistFileRuntimeJournal(t *testing.T, runtimeStore *FileRuntimeStore, journal fileTransactionJournal) string {
	t.Helper()
	if err := os.MkdirAll(runtimeStore.journalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(journal): %v", err)
	}
	journalPath := filepath.Join(runtimeStore.journalDir, journal.ID+".json")
	if err := writeRunnerJSONFile(journalPath, journal); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	return journalPath
}

func assertFileRuntimeTransactionRecords(t *testing.T, runtimeStore *FileRuntimeStore, commit RuntimeCommit, wantPresent bool) {
	t.Helper()
	ctx := context.Background()
	step := commit.Steps[0].Step
	checkpoint := commit.Checkpoints[0]
	if wantPresent {
		if _, err := runtimeStore.GetStep(ctx, step.StepID); err != nil {
			t.Fatalf("GetStep(): %v", err)
		}
		_, payload, err := runtimeStore.Load(ctx, checkpoint.Record.CheckpointID)
		if err != nil {
			t.Fatalf("Load(): %v", err)
		}
		if string(payload) != string(checkpoint.Payload) {
			t.Fatalf("checkpoint payload = %q, want %q", payload, checkpoint.Payload)
		}
	} else {
		if _, err := runtimeStore.GetStep(ctx, step.StepID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("GetStep() error = %v, want record not found", err)
		}
		if _, _, err := runtimeStore.Load(ctx, checkpoint.Record.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("Load() error = %v, want record not found", err)
		}
	}
	events, err := runtimeStore.ListEvents(commit.Run.Run.RunID)
	if err != nil {
		t.Fatalf("ListEvents(): %v", err)
	}
	wantEvents := 0
	if wantPresent {
		wantEvents = len(commit.Events)
	}
	if len(events) != wantEvents {
		t.Fatalf("events = %#v, want %d", events, wantEvents)
	}
}

func TestFileRuntimeStoreCommitRejectsInvalidIDsWithoutWrites(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		mutate func(*RuntimeCommit)
	}{
		{name: "run", mutate: func(commit *RuntimeCommit) { commit.Run.Run.RunID = "../invalid-run" }},
		{name: "step", mutate: func(commit *RuntimeCommit) { commit.Steps[0].Step.StepID = "../invalid-step" }},
		{name: "checkpoint", mutate: func(commit *RuntimeCommit) { commit.Checkpoints[0].Record.CheckpointID = "../invalid-checkpoint" }},
		{name: "event", mutate: func(commit *RuntimeCommit) { commit.Events[0].ID = "../invalid-event" }},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			baseDir := t.TempDir()
			runtimeStore, original, commit := prepareFileRuntimeTransaction(t, baseDir)
			before := snapshotRuntimeStoreFiles(t, baseDir)
			testCase.mutate(&commit)
			if _, err := runtimeStore.Commit(context.Background(), commit); err == nil {
				t.Fatal("Commit() error = nil")
			}
			after := snapshotRuntimeStoreFiles(t, baseDir)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("runtime files changed after invalid commit:\nbefore=%#v\nafter=%#v", before, after)
			}
			persisted, err := runtimeStore.GetRun(context.Background(), original.RunID)
			if err != nil {
				t.Fatalf("GetRun(): %v", err)
			}
			if persisted.Revision != original.Revision || persisted.Status != original.Status {
				t.Fatalf("run changed after invalid commit: %#v", persisted)
			}
		})
	}
}

func snapshotRuntimeStoreFiles(t *testing.T, baseDir string) map[string][]byte {
	t.Helper()
	paths := make([]string, 0)
	if err := filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir(): %v", err)
	}
	sort.Strings(paths)
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		relative, err := filepath.Rel(baseDir, path)
		if err != nil {
			t.Fatalf("Rel(%q): %v", path, err)
		}
		files[relative] = content
	}
	return files
}
