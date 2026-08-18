package file

import (
	"context"
	"errors"
	"os"
	"testing"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestArtifactReconciliationDiscardsPayloadWithoutStageMetadata(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory)
	run := RunRecord{RunID: "run-artifact-payload-only", Status: fruntime.RunStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	transactionID := "artifact-payload-only-transaction"
	artifactID := "artifact-payload-only"
	stagePayloadPath := store.artifacts.stagePayloadPath(run.RunID, transactionID, artifactID)
	if err := writeRunnerBinaryFile(stagePayloadPath, []byte("payload")); err != nil {
		t.Fatalf("write staged payload: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openTestStore(t, directory)
	if _, err := os.Stat(store.artifacts.stageTransactionDir(run.RunID, transactionID)); !os.IsNotExist(err) {
		t.Fatalf("orphan stage directory still exists: %v", err)
	}
	if _, err := reopened.ArtifactStore().Load(context.Background(), state.ArtifactRef{RunID: run.RunID, ID: artifactID}); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load() error = %v, want not found", err)
	}
}

func TestArtifactReconciliationDiscardsCompleteStageWithoutRuntimeCommit(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory)
	run := RunRecord{RunID: "run-artifact-uncommitted", Status: fruntime.RunStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	transactionID := "artifact-uncommitted-transaction"
	stage, err := store.ArtifactStore().Stage(context.Background(), transactionID, Artifact{
		RunID: run.RunID, ID: "artifact-uncommitted", Data: []byte("payload"),
	})
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openTestStore(t, directory)
	if _, err := reopened.ArtifactStore().Load(context.Background(), stage.Ref); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load() error = %v, want not found", err)
	}
	if _, err := os.Stat(reopened.artifacts.stageTransactionDir(run.RunID, transactionID)); !os.IsNotExist(err) {
		t.Fatalf("uncommitted stage directory still exists: %v", err)
	}
}

func TestArtifactReconciliationFinalizesCommittedStage(t *testing.T) {
	testCases := []struct {
		name              string
		writeFinalPayload bool
	}{
		{name: "stage only"},
		{name: "final payload without metadata", writeFinalPayload: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			store := openTestStore(t, directory)
			run := RunRecord{RunID: "run-artifact-committed", Status: fruntime.RunStatusRunning}
			if err := store.CreateRun(context.Background(), run); err != nil {
				t.Fatalf("CreateRun() error = %v", err)
			}
			transactionID := "artifact-committed-transaction"
			stage, err := store.ArtifactStore().Stage(context.Background(), transactionID, Artifact{
				RunID: run.RunID, ID: "artifact-committed", Data: []byte("payload"),
			})
			if err != nil {
				t.Fatalf("Stage() error = %v", err)
			}
			result, err := store.Commit(context.Background(), Commit{
				TransactionID: transactionID,
				Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteCheck, Run: run},
				Artifacts:     []ArtifactStage{stage},
			})
			if err != nil || result.Outcome != fruntime.TransactionCommitted {
				t.Fatalf("Commit() = %#v, %v", result, err)
			}
			if testCase.writeFinalPayload {
				if err := writeRunnerBinaryFile(store.artifacts.payloadPath(run.RunID, stage.Ref.ID), []byte("payload")); err != nil {
					t.Fatalf("write final payload: %v", err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			reopened := openTestStore(t, directory)
			artifact, err := reopened.ArtifactStore().Load(context.Background(), stage.Ref)
			if err != nil || string(artifact.Data) != "payload" {
				t.Fatalf("Load() artifact = %#v, %v", artifact, err)
			}
			resolved, err := reopened.ResolveCommit(context.Background(), transactionID)
			if err != nil || resolved.Outcome != fruntime.TransactionCommitted || len(resolved.Artifacts) != 1 {
				t.Fatalf("ResolveCommit() = %#v, %v", resolved, err)
			}
			if _, err := os.Stat(reopened.artifacts.stageTransactionDir(run.RunID, transactionID)); !os.IsNotExist(err) {
				t.Fatalf("committed stage directory still exists: %v", err)
			}
		})
	}
}

func TestArtifactReconciliationFinalizesAfterCommitResponseLoss(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory)
	run := RunRecord{RunID: "run-artifact-response-loss", Status: fruntime.RunStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	transactionID := "artifact-response-loss-transaction"
	stage, err := store.ArtifactStore().Stage(context.Background(), transactionID, Artifact{
		RunID: run.RunID, ID: "artifact-response-loss", Data: []byte("payload"),
	})
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	updated := run
	updated.Status = fruntime.RunStatusCompleted
	store.failure = failureAfterCommittedJournal
	result, err := store.Commit(context.Background(), Commit{
		TransactionID: transactionID,
		Run:           &fruntime.RunWrite{Mode: fruntime.RunWriteUpdate, Run: updated},
		Artifacts:     []ArtifactStage{stage},
	})
	if !errors.Is(err, errInjectedTransactionFailure) || result.Outcome != fruntime.TransactionOutcomeUnknown {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openTestStore(t, directory)
	artifact, err := reopened.ArtifactStore().Load(context.Background(), stage.Ref)
	if err != nil || string(artifact.Data) != "payload" {
		t.Fatalf("Load() artifact = %#v, %v", artifact, err)
	}
	resolved, err := reopened.ResolveCommit(context.Background(), transactionID)
	if err != nil || resolved.Outcome != fruntime.TransactionCommitted {
		t.Fatalf("ResolveCommit() = %#v, %v", resolved, err)
	}
	persisted, err := reopened.GetRun(context.Background(), run.RunID)
	if err != nil || persisted.Status != fruntime.RunStatusCompleted {
		t.Fatalf("GetRun() = %#v, %v", persisted, err)
	}
}
