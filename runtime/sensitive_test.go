package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestRedactJSONBytesRemovesContextSecretsAndSensitiveFields(t *testing.T) {
	ctx := core.WithEnvironment(context.Background(), map[string]string{
		"SERVICE_TOKEN": "service-token-value",
	})
	ctx = core.WithModelConfigs(ctx, map[string]core.ModelConfig{
		"default": {APIKey: "model-api-key-value"},
	})
	payload := map[string]any{
		"api_key":       "model-api-key-value",
		"accessToken":   "unknown-access-token-value",
		"arguments":     "call with service-token-value",
		"prompt_tokens": 7,
		"nested": map[string]any{
			"password": "another-secret-value",
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	redacted := string(redactJSONBytes(ctx, encoded))
	for _, secret := range []string{"model-api-key-value", "unknown-access-token-value", "service-token-value", "another-secret-value"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted payload contains %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `"prompt_tokens":7`) || strings.Count(redacted, redactedSensitiveValue) < 3 {
		t.Fatalf("redacted payload = %s", redacted)
	}
}

func TestSanitizeEventsRedactsPayloadsBeforePersistence(t *testing.T) {
	ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
		"default": {APIKey: "event-secret-value"},
	})
	events := sanitizeEvents(ctx, []Event{{
		RunID:   "run-1",
		Type:    EventToolReturned,
		Payload: json.RawMessage(`{"value":"event-secret-value"}`),
	}})
	if len(events) != 1 || strings.Contains(string(events[0].Payload), "event-secret-value") {
		t.Fatalf("sanitized events = %#v", events)
	}
}

func TestSanitizeCommitRedactsRunAndStepErrors(t *testing.T) {
	ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
		"default": {APIKey: "persisted-error-secret"},
	})
	run := RunRecord{
		RunID:        "run-1",
		ErrorMessage: "provider failed with persisted-error-secret",
		ReturnValue: map[string]any{
			"api_key": "persisted-error-secret",
			"nested":  map[string]any{"password": "unknown-return-secret"},
		},
	}
	step := StepRecord{RunID: "run-1", StepID: "step-1", ErrorMessage: "step failed with persisted-error-secret"}
	commit := sanitizeCommit(ctx, Commit{
		Run:   &RunWrite{Mode: RunWriteUpdate, Run: run},
		Steps: []StepWrite{{Mode: StepWriteUpdate, Step: step}},
	})
	if commit.Run == nil || strings.Contains(commit.Run.Run.ErrorMessage, "persisted-error-secret") {
		t.Fatalf("sanitized run error = %#v", commit.Run)
	}
	encodedReturn, err := json.Marshal(commit.Run.Run.ReturnValue)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedReturn), "persisted-error-secret") || strings.Contains(string(encodedReturn), "unknown-return-secret") {
		t.Fatalf("sanitized return value = %s", encodedReturn)
	}
	if len(commit.Steps) != 1 || strings.Contains(commit.Steps[0].Step.ErrorMessage, "persisted-error-secret") {
		t.Fatalf("sanitized step error = %#v", commit.Steps)
	}
	if run.ErrorMessage == commit.Run.Run.ErrorMessage || step.ErrorMessage == commit.Steps[0].Step.ErrorMessage {
		t.Fatal("sanitizeCommit mutated the caller's records")
	}
	if run.ReturnValue.(map[string]any)["api_key"] != "persisted-error-secret" {
		t.Fatal("sanitizeCommit mutated the caller's return value")
	}
}

func TestRuntimeStoreCommitRedactsDirectErrorRecords(t *testing.T) {
	type runtimeRecordStore interface {
		TransactionStore
		ExecutionStore
	}
	stores := []runtimeRecordStore{NewMemoryRuntimeStore()}
	for _, store := range stores {
		ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
			"default": {APIKey: "direct-store-secret"},
		})
		run := RunRecord{RunID: "run-direct", Status: RunStatusRunning, ErrorMessage: "run direct-store-secret"}
		step := StepRecord{RunID: run.RunID, StepID: "step-direct", Status: StepStatusFailed, ErrorMessage: "step direct-store-secret"}
		if _, err := store.Commit(ctx, Commit{
			Run:   &RunWrite{Mode: RunWriteCreate, Run: run},
			Steps: []StepWrite{{Mode: StepWriteAppend, Step: step}},
		}); err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
		persistedRun, err := store.GetRun(context.Background(), run.RunID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}
		persistedStep, err := store.GetStep(context.Background(), step.StepID)
		if err != nil {
			t.Fatalf("GetStep() error = %v", err)
		}
		if strings.Contains(persistedRun.ErrorMessage, "direct-store-secret") || strings.Contains(persistedStep.ErrorMessage, "direct-store-secret") {
			t.Fatalf("persisted errors leaked secret: run=%q step=%q", persistedRun.ErrorMessage, persistedStep.ErrorMessage)
		}
	}
}

func TestExecutionStoresRedactDirectWrites(t *testing.T) {
	tests := []struct {
		name     string
		newStore func(*testing.T) ExecutionStore
	}{
		{name: "memory", newStore: func(*testing.T) ExecutionStore { return NewMemoryExecutionStore() }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := test.newStore(t)
			secret := test.name + "-direct-store-secret"
			ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
				"default": {APIKey: secret},
			})
			run := RunRecord{
				RunID: "run-direct", RootRunID: "run-direct", Status: RunStatusRunning, ErrorMessage: "run " + secret,
				ReturnValue: map[string]any{"credential": secret},
			}
			if err := store.CreateRun(ctx, run); err != nil {
				t.Fatalf("CreateRun() error = %v", err)
			}
			persistedRun, err := store.GetRun(context.Background(), run.RunID)
			if err != nil {
				t.Fatalf("GetRun() error = %v", err)
			}
			if strings.Contains(persistedRun.ErrorMessage, secret) {
				t.Fatalf("CreateRun() persisted secret: %q", persistedRun.ErrorMessage)
			}
			encodedReturn, err := json.Marshal(persistedRun.ReturnValue)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encodedReturn), secret) {
				t.Fatalf("CreateRun() persisted return secret: %s", encodedReturn)
			}
			persistedRun.ErrorMessage = "updated run " + secret
			updatedRun, err := store.CompareAndSwapRun(ctx, persistedRun.Revision, persistedRun)
			if err != nil {
				t.Fatalf("CompareAndSwapRun() error = %v", err)
			}
			if strings.Contains(updatedRun.ErrorMessage, secret) {
				t.Fatalf("CompareAndSwapRun() returned secret: %q", updatedRun.ErrorMessage)
			}

			step := StepRecord{RunID: run.RunID, StepID: "step-direct", Status: StepStatusFailed, ErrorMessage: "step " + secret}
			if err := store.AppendStep(ctx, step); err != nil {
				t.Fatalf("AppendStep() error = %v", err)
			}
			persistedStep, err := store.GetStep(context.Background(), step.StepID)
			if err != nil {
				t.Fatalf("GetStep() error = %v", err)
			}
			if strings.Contains(persistedStep.ErrorMessage, secret) {
				t.Fatalf("AppendStep() persisted secret: %q", persistedStep.ErrorMessage)
			}
			persistedStep.ErrorMessage = "updated step " + secret
			if err := store.UpdateStep(ctx, persistedStep); err != nil {
				t.Fatalf("UpdateStep() error = %v", err)
			}
			persistedStep, err = store.GetStep(context.Background(), step.StepID)
			if err != nil {
				t.Fatalf("GetStep() after update error = %v", err)
			}
			if strings.Contains(persistedStep.ErrorMessage, secret) {
				t.Fatalf("UpdateStep() persisted secret: %q", persistedStep.ErrorMessage)
			}
		})
	}
}

func TestArtifactStoresRedactDirectJSONWrites(t *testing.T) {
	tests := []struct {
		name     string
		newStore func(*testing.T) ArtifactStore
	}{
		{name: "memory", newStore: func(*testing.T) ArtifactStore { return NewMemoryArtifactStore() }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := test.newStore(t)
			ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
				"default": {APIKey: "artifact-context-secret"},
			})
			ref, err := stageAndFinalizeTestArtifact(ctx, store, "artifact-direct-transaction", Artifact{
				ID:       "artifact-direct",
				RunID:    "run-direct",
				MIMEType: "application/octet-stream",
				Data:     []byte(`{"api_key":"unknown-artifact-secret","message":"artifact-context-secret"}`),
			})
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			artifact, err := store.Load(context.Background(), ref)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			for _, secret := range []string{"unknown-artifact-secret", "artifact-context-secret"} {
				if strings.Contains(string(artifact.Data), secret) {
					t.Fatalf("persisted JSON artifact contains %q: %s", secret, artifact.Data)
				}
			}
			textRef, err := stageAndFinalizeTestArtifact(ctx, store, "artifact-text-transaction", Artifact{
				ID:       "artifact-text",
				RunID:    "run-direct",
				MIMEType: "text/plain",
				Data:     []byte("provider failed with artifact-context-secret"),
			})
			if err != nil {
				t.Fatalf("Save(text) error = %v", err)
			}
			textArtifact, err := store.Load(context.Background(), textRef)
			if err != nil {
				t.Fatalf("Load(text) error = %v", err)
			}
			if strings.Contains(string(textArtifact.Data), "artifact-context-secret") {
				t.Fatalf("persisted text artifact contains context secret: %s", textArtifact.Data)
			}
		})
	}
}

func TestRecordArtifactBindsRunnerLineage(t *testing.T) {
	store := NewMemoryArtifactStore()
	executionStore := NewMemoryRuntimeStore()
	runner := &GraphRunner{artifactStore: store, executionStore: executionStore}
	metadata := RunnerMetadata{
		RunID: "run-1", StepID: "step-1", TaskID: "task-1", NodeID: "node-1",
		ParentRunID: "parent-1", ParentStepID: "parent-step-1", ParentTaskID: "parent-task-1",
		RootRunID: "root-1", RunPath: []string{"root-1", "run-1"}, Namespace: "root/child",
	}
	if err := executionStore.CreateRun(context.Background(), RunRecord{RunID: metadata.RunID, Status: RunStatusRunning}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	ctx := WithRunnerMetadata(context.Background(), metadata)
	if _, err := runner.recordArtifact(ctx, "spoofed-artifact-transaction", Artifact{RunID: "other-run", Type: "test", Data: []byte("value")}); err == nil || !strings.Contains(err.Error(), "does not match runner metadata") {
		t.Fatalf("recordArtifact() spoofed lineage error = %v", err)
	}
	if refs, err := store.List(context.Background(), metadata.RunID); err != nil || len(refs) != 0 {
		t.Fatalf("spoofed artifact was persisted: refs=%#v error=%v", refs, err)
	}

	stage, err := runner.recordArtifact(ctx, "bound-artifact-transaction", Artifact{Type: "test", Data: []byte("value")})
	if err != nil {
		t.Fatalf("recordArtifact() error = %v", err)
	}
	ref := stage.Ref
	if ref.RunID != metadata.RunID || ref.StepID != metadata.StepID || ref.NodeID != metadata.NodeID ||
		ref.ParentRunID != metadata.ParentRunID || ref.RootRunID != metadata.RootRunID ||
		ref.Namespace != metadata.Namespace || len(ref.RunPath) != len(metadata.RunPath) {
		t.Fatalf("artifact lineage = %#v, want metadata %#v", ref, metadata)
	}
}

func TestBestEffortEventFailureRedactsDiagnostics(t *testing.T) {
	ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
		"default": {APIKey: "diagnostic-secret-value"},
	})
	runner := &GraphRunner{}
	runner.recordBestEffortEventFailure(ctx, EventArtifactCreated, errors.New("observer diagnostic-secret-value"))
	failure := runner.EventPublicationDiagnostics().BestEffortFailures[EventArtifactCreated]
	if strings.Contains(failure.LastError, "diagnostic-secret-value") || !strings.Contains(failure.LastError, redactedSensitiveValue) {
		t.Fatalf("diagnostic error was not redacted: %q", failure.LastError)
	}
}

func TestValidateRestoredCheckpointRequiresMatchingSnapshotIdentity(t *testing.T) {
	codec := state.NewJSONStateCodec("")
	runner := &GraphRunner{codec: codec}
	checkpoint := RestoredCheckpoint{
		Record: CheckpointRecord{
			CheckpointID: "checkpoint-1",
			RunID:        "run-1",
			StepID:       "step-1",
			TaskID:       "task-1",
			NodeID:       "node-1",
			StateCodec:   codec.Name(),
			StateVersion: codec.Version(),
		},
		Snapshot: state.Snapshot{Version: codec.Version()},
		Runtime: state.RuntimeState{
			RunID:         "run-1",
			CurrentStepID: "step-1",
			CurrentTaskID: "task-1",
			CurrentNodeID: "node-1",
		},
	}

	tests := []struct {
		name   string
		mutate func(*RestoredCheckpoint)
		want   string
	}{
		{name: "missing run", mutate: func(item *RestoredCheckpoint) { item.Runtime.RunID = "" }, want: "snapshot identity"},
		{name: "missing step", mutate: func(item *RestoredCheckpoint) { item.Runtime.CurrentStepID = "" }, want: "step mismatch"},
		{name: "missing task", mutate: func(item *RestoredCheckpoint) { item.Runtime.CurrentTaskID = "" }, want: "task mismatch"},
		{name: "missing node", mutate: func(item *RestoredCheckpoint) { item.Runtime.CurrentNodeID = "" }, want: "nodes mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := checkpoint
			test.mutate(&candidate)
			if err := runner.validateRestoredCheckpoint(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateRestoredCheckpoint() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLogFieldsRedactErrorMessages(t *testing.T) {
	ctx := core.WithModelConfigs(context.Background(), map[string]core.ModelConfig{
		"default": {APIKey: "log-error-secret"},
	})
	for _, field := range runLogFields(ctx, RunRecord{ErrorMessage: "run log-error-secret"}) {
		if field.Key == "error_message" && strings.Contains(field.String, "log-error-secret") {
			t.Fatalf("run log field leaked secret: %#v", field)
		}
	}
	for _, field := range stepLogFields(ctx, StepRecord{ErrorMessage: "step log-error-secret"}) {
		if field.Key == "error_message" && strings.Contains(field.String, "log-error-secret") {
			t.Fatalf("step log field leaked secret: %#v", field)
		}
	}
}

func TestValidateCheckpointRunRejectsCrossRunCheckpoint(t *testing.T) {
	t.Parallel()
	checkpoint := RestoredCheckpoint{Record: CheckpointRecord{CheckpointID: "checkpoint-b", RunID: "run-b"}}
	if err := validateCheckpointRun(RunRecord{RunID: "run-a"}, checkpoint); err == nil || !strings.Contains(err.Error(), "belongs to run") {
		t.Fatalf("validateCheckpointRun() error = %v, want cross-run rejection", err)
	}
}

func TestProjectContractObservationStateExcludesUndeclaredPaths(t *testing.T) {
	current := state.FromShared(map[string]any{
		"public": "visible",
		"secret": "hidden",
	})
	contract := state.NewContract(state.NewRef[string](state.Shared("public")).ReadField())
	projected := projectContractObservationState(current, contractInputViewArtifactType, contract)
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hidden") || !strings.Contains(string(encoded), "visible") {
		t.Fatalf("projected state = %s", encoded)
	}
}

func TestProjectContractObservationStateKeepsWildcardWriteBusinessOnly(t *testing.T) {
	current := state.FromMap(map[string]any{
		state.SectionShared:   map[string]any{"value": "visible"},
		state.SectionInternal: map[string]any{"secret": "hidden"},
		state.SectionRuntime:  map[string]any{"checkpoint": "hidden"},
	})
	projected := projectContractObservationState(current, contractOutputPatchArtifactType, state.Contract{WildcardWrite: true})
	if _, ok := state.ReadPath(projected, "internal.secret"); ok {
		t.Fatal("wildcard write observation exposed internal state")
	}
	if _, ok := state.ReadPath(projected, "runtime.checkpoint"); ok {
		t.Fatal("wildcard write observation exposed runtime state")
	}
	if value, ok := state.ReadPath(projected, "shared.value"); !ok || value != "visible" {
		t.Fatalf("wildcard write observation lost business state: %#v, %v", value, ok)
	}
}
