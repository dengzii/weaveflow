package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

func TestStoreCommitRejectsStepMetadataIdentityMismatch(t *testing.T) {
	store := openTestStore(t, t.TempDir())
	run := RunRecord{RunID: "run-step-identity", Status: fruntime.RunStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	step := StepRecord{RunID: run.RunID, StepID: "step-identity", Status: fruntime.StepStatusRunning}
	if err := store.AppendStep(context.Background(), step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	corrupted := step
	corrupted.RunID = "different-run"
	if err := writeRunnerJSONFile(store.execution.stepPath(run.RunID, step.StepID), corrupted); err != nil {
		t.Fatalf("write corrupted step: %v", err)
	}

	step.Status = fruntime.StepStatusSucceeded
	_, err := store.Commit(context.Background(), Commit{
		Steps: []fruntime.StepWrite{{Mode: fruntime.StepWriteUpdate, Step: step}},
	})
	if err == nil || !strings.Contains(err.Error(), "metadata identity mismatch") {
		t.Fatalf("Commit() error = %v, want metadata identity mismatch", err)
	}
}

func TestStoreRecoveryUsesJournalPhase(t *testing.T) {
	t.Run("prepared rolls back", func(t *testing.T) {
		directory := t.TempDir()
		store, original, commit := prepareTransaction(t, directory)
		journal, _, err := store.prepareJournalLocked(context.Background(), commit)
		if err != nil {
			t.Fatalf("prepareJournalLocked() error = %v", err)
		}
		journalPath := persistJournal(t, store, journal)
		if err := applyTransactionMutations(journal.Mutations, true); err != nil {
			t.Fatalf("apply after-state: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		recovered := openTestStore(t, directory)
		persisted, err := recovered.GetRun(context.Background(), original.RunID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}
		if persisted.Revision != original.Revision || persisted.Status != original.Status {
			t.Fatalf("prepared recovery retained mutation: %#v", persisted)
		}
		assertTransactionRecords(t, recovered, commit, false)
		if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
			t.Fatalf("prepared journal still exists: %v", err)
		}
	})

	t.Run("committed replays", func(t *testing.T) {
		directory := t.TempDir()
		store, _, commit := prepareTransaction(t, directory)
		journal, result, err := store.prepareJournalLocked(context.Background(), commit)
		if err != nil {
			t.Fatalf("prepareJournalLocked() error = %v", err)
		}
		journal.Phase = transactionCommitted
		journalPath := persistJournal(t, store, journal)
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		recovered := openTestStore(t, directory)
		persisted, err := recovered.GetRun(context.Background(), commit.Run.Run.RunID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}
		if result.Run == nil || persisted.Revision != result.Run.Revision || persisted.Status != result.Run.Status {
			t.Fatalf("committed recovery run = %#v, want %#v", persisted, result.Run)
		}
		assertTransactionRecords(t, recovered, commit, true)
		if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
			t.Fatalf("committed journal still exists: %v", err)
		}
	})
}

func TestStoreRecoveryFailsClosedForInvalidJournal(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(string, *transactionJournal)
	}{
		{name: "unknown phase", mutate: func(_ string, journal *transactionJournal) {
			journal.Phase = transactionPhase("unknown")
		}},
		{name: "root escape", mutate: func(directory string, journal *transactionJournal) {
			journal.Mutations[0].Path = filepath.Join(filepath.Dir(directory), "outside.json")
		}},
		{name: "duplicate mutation", mutate: func(_ string, journal *transactionJournal) {
			journal.Mutations = append(journal.Mutations, journal.Mutations[0])
		}},
		{name: "record identity", mutate: func(_ string, journal *transactionJournal) {
			journal.Mutations[0].After = []byte(`{"run_id":"different-run"}`)
		}},
		{name: "unknown namespace", mutate: func(directory string, journal *transactionJournal) {
			journal.Mutations[0].Path = filepath.Join(directory, ".writer.lock")
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			store, _, commit := prepareTransaction(t, directory)
			journal, _, err := store.prepareJournalLocked(context.Background(), commit)
			if err != nil {
				t.Fatalf("prepareJournalLocked() error = %v", err)
			}
			testCase.mutate(directory, &journal)
			persistJournalUnchecked(t, store, journal)
			if err := store.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			recovered, err := Open(directory)
			if err == nil {
				_ = recovered.Close()
				t.Fatal("Open() error = nil, want invalid journal rejection")
			}
		})
	}
}

func TestStoreCrashInjectionRecoversWholeTransaction(t *testing.T) {
	testCases := []struct {
		name      string
		point     transactionFailurePoint
		wantAfter bool
	}{
		{name: "prepared journal", point: failureAfterPreparedJournal},
		{name: "before target replacement", point: failureBeforeMutation},
		{name: "after partial target replacement", point: failureAfterMutation},
		{name: "committed journal", point: failureAfterCommittedJournal, wantAfter: true},
		{name: "before journal removal", point: failureBeforeJournalRemoval, wantAfter: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			store, original, commit := prepareTransaction(t, directory)
			store.failure = testCase.point
			store.failureAt = 0
			if _, err := store.Commit(context.Background(), commit); !errors.Is(err, errInjectedTransactionFailure) {
				t.Fatalf("Commit() error = %v, want injected failure", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			recovered := openTestStore(t, directory)
			persisted, err := recovered.GetRun(context.Background(), original.RunID)
			if err != nil {
				t.Fatalf("GetRun() error = %v", err)
			}
			if testCase.wantAfter {
				if persisted.Revision != original.Revision+1 || persisted.Status != fruntime.RunStatusRunning {
					t.Fatalf("recovered run = %#v, want committed after-state", persisted)
				}
			} else if persisted.Revision != original.Revision || persisted.Status != original.Status {
				t.Fatalf("recovered run = %#v, want complete before-state", persisted)
			}
			assertTransactionRecords(t, recovered, commit, testCase.wantAfter)
		})
	}
}

func TestStoreCommitRejectsInvalidIDsWithoutWrites(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*fruntime.Commit)
	}{
		{name: "run", mutate: func(commit *fruntime.Commit) { commit.Run.Run.RunID = "../invalid-run" }},
		{name: "step", mutate: func(commit *fruntime.Commit) { commit.Steps[0].Step.StepID = "../invalid-step" }},
		{name: "checkpoint", mutate: func(commit *fruntime.Commit) { commit.Checkpoints[0].Record.CheckpointID = "../invalid-checkpoint" }},
		{name: "event", mutate: func(commit *fruntime.Commit) { commit.Events[0].ID = "../invalid-event" }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			store, original, commit := prepareTransaction(t, directory)
			defer func() { _ = store.Close() }()
			before := snapshotFiles(t, directory)
			testCase.mutate(&commit)
			if _, err := store.Commit(context.Background(), commit); err == nil {
				t.Fatal("Commit() error = nil")
			}
			after := snapshotFiles(t, directory)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("runtime files changed after invalid commit:\nbefore=%#v\nafter=%#v", before, after)
			}
			persisted, err := store.GetRun(context.Background(), original.RunID)
			if err != nil {
				t.Fatalf("GetRun() error = %v", err)
			}
			if persisted.Revision != original.Revision || persisted.Status != original.Status {
				t.Fatalf("run changed after invalid commit: %#v", persisted)
			}
		})
	}
}

func prepareTransaction(t *testing.T, directory string) (*Store, RunRecord, fruntime.Commit) {
	t.Helper()
	store, err := Open(directory)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	original := RunRecord{RunID: "run-file-transaction", Status: fruntime.RunStatusPending}
	if err := store.CreateRun(context.Background(), original); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	updated := original
	updated.Status = fruntime.RunStatusRunning
	step := StepRecord{RunID: original.RunID, StepID: "step-file-transaction", Status: fruntime.StepStatusSucceeded}
	checkpoint := CheckpointRecord{
		RunID: original.RunID, CheckpointID: "checkpoint-file-transaction", StepID: step.StepID,
		Stage: fruntime.CheckpointAfterNode,
	}
	commit := fruntime.Commit{
		Run:         &fruntime.RunWrite{Mode: fruntime.RunWriteUpdate, Run: updated},
		Steps:       []fruntime.StepWrite{{Mode: fruntime.StepWriteAppend, Step: step}},
		Checkpoints: []fruntime.CheckpointWrite{{Record: checkpoint, Payload: []byte("checkpoint payload")}},
		Events:      []fruntime.Event{{ID: "event-file-transaction", RunID: original.RunID, Type: fruntime.EventNodeFinished}},
	}
	return store, original, commit
}

func persistJournal(t *testing.T, store *Store, journal transactionJournal) string {
	t.Helper()
	path := filepath.Join(store.journalDir, journal.ID+".json")
	if err := store.validateJournal(path, journal); err != nil {
		t.Fatalf("validateJournal() error = %v", err)
	}
	persistJournalUnchecked(t, store, journal)
	return path
}

func persistJournalUnchecked(t *testing.T, store *Store, journal transactionJournal) {
	t.Helper()
	if err := os.MkdirAll(store.journalDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(journal) error = %v", err)
	}
	path := filepath.Join(store.journalDir, journal.ID+".json")
	if err := writeRunnerJSONFile(path, journal); err != nil {
		t.Fatalf("write journal: %v", err)
	}
}

func assertTransactionRecords(t *testing.T, store *Store, commit fruntime.Commit, wantPresent bool) {
	t.Helper()
	ctx := context.Background()
	step := commit.Steps[0].Step
	checkpoint := commit.Checkpoints[0]
	if wantPresent {
		if _, err := store.GetStep(ctx, step.StepID); err != nil {
			t.Fatalf("GetStep() error = %v", err)
		}
		_, payload, err := store.Load(ctx, checkpoint.Record.CheckpointID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if string(payload) != string(checkpoint.Payload) {
			t.Fatalf("checkpoint payload = %q, want %q", payload, checkpoint.Payload)
		}
	} else {
		if _, err := store.GetStep(ctx, step.StepID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("GetStep() error = %v, want record not found", err)
		}
		if _, _, err := store.Load(ctx, checkpoint.Record.CheckpointID); !errors.Is(err, ErrRunnerRecordNotFound) {
			t.Fatalf("Load() error = %v, want record not found", err)
		}
	}
	events, err := store.ListEvents(commit.Run.Run.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	wantEvents := 0
	if wantPresent {
		wantEvents = len(commit.Events)
	}
	if len(events) != wantEvents {
		t.Fatalf("events = %#v, want %d", events, wantEvents)
	}
}

func openTestStore(t *testing.T, directory string) *Store {
	t.Helper()
	store, err := Open(directory)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func snapshotFiles(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	paths := make([]string, 0)
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() != ".writer.lock" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
	sort.Strings(paths)
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			t.Fatalf("Rel(%q) error = %v", path, err)
		}
		files[relative] = content
	}
	return files
}
