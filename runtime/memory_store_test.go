package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoresRoundTripCloneAndDeleteRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	executionStore := NewMemoryExecutionStore()
	checkpointStore := NewMemoryCheckpointStore()
	eventSink := NewMemoryEventSink()
	artifactStore := NewMemoryArtifactStore()
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	run := RunRecord{RunID: "run-1", Status: RunStatusRunning, CurrentNodeIDs: []string{"start"}, StartedAt: startedAt, UpdatedAt: startedAt}
	if err := executionStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	run.CurrentNodeIDs[0] = "changed"
	storedRun, err := executionStore.GetRun(ctx, "run-1")
	if err != nil || storedRun.CurrentNodeIDs[0] != "start" {
		t.Fatalf("GetRun() = %#v, %v", storedRun, err)
	}

	step := StepRecord{StepID: "step-1", RunID: "run-1", NodeID: "start", Status: StepStatusRunning, StartedAt: startedAt, UpdatedAt: startedAt}
	if err := executionStore.AppendStep(ctx, step); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	payload := []byte("checkpoint")
	checkpoint := CheckpointRecord{CheckpointID: "checkpoint-1", RunID: "run-1", StepID: "step-1", NodeID: "start", Stage: CheckpointAfterNode, CreatedAt: startedAt}
	if err := checkpointStore.Save(ctx, checkpoint, payload); err != nil {
		t.Fatalf("Save() checkpoint error = %v", err)
	}
	payload[0] = 'X'
	_, storedPayload, err := checkpointStore.Load(ctx, "checkpoint-1")
	if err != nil || string(storedPayload) != "checkpoint" {
		t.Fatalf("Load() checkpoint payload = %q, %v", storedPayload, err)
	}

	event := Event{ID: "event-1", RunID: "run-1", Type: EventRunStarted, Timestamp: startedAt, Payload: []byte(`{"status":"running"}`)}
	if err := eventSink.Publish(ctx, event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	event.Payload[0] = 'X'
	events, err := eventSink.ListEvents("run-1")
	if err != nil || len(events) != 1 || string(events[0].Payload) != `{"status":"running"}` {
		t.Fatalf("ListEvents() = %#v, %v", events, err)
	}

	artifactData := []byte("artifact")
	ref, err := artifactStore.Save(ctx, Artifact{ID: "artifact-1", RunID: "run-1", Type: "text", Data: artifactData})
	if err != nil {
		t.Fatalf("Save() artifact error = %v", err)
	}
	artifactData[0] = 'X'
	storedArtifact, err := artifactStore.Load(ctx, ref)
	if err != nil || string(storedArtifact.Data) != "artifact" {
		t.Fatalf("Load() artifact = %#v, %v", storedArtifact, err)
	}

	deleter := NewRunDeletionCoordinator(executionStore, checkpointStore, eventSink, artifactStore)
	if err := deleter.DeleteRun(ctx, "run-1"); err != nil {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	if _, err := executionStore.GetRun(ctx, "run-1"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() after delete error = %v", err)
	}
	if checkpoints, err := checkpointStore.List(ctx, "run-1"); err != nil || len(checkpoints) != 0 {
		t.Fatalf("List() checkpoints after delete = %#v, %v", checkpoints, err)
	}
	if events, err := eventSink.ListEvents("run-1"); err != nil || len(events) != 0 {
		t.Fatalf("ListEvents() after delete = %#v, %v", events, err)
	}
	if artifacts, err := artifactStore.List(ctx, "run-1"); err != nil || len(artifacts) != 0 {
		t.Fatalf("List() artifacts after delete = %#v, %v", artifacts, err)
	}
}
