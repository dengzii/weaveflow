package graph

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestGraphRunnerValidatesStateSchemaAtEntry(t *testing.T) {
	t.Parallel()

	workflow := singleResultNodeGraph(t, "entry", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.Success(), nil
	})
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore,
		fruntime.WithStateSchemas(nestedPayloadSchemas()))

	run, _, err := runner.Start(context.Background(), invalidNestedPayloadState())
	if run.RunID != "" {
		t.Fatalf("entry validation created run %#v", run)
	}
	assertStateValidationError(t, err, "entry", "shared.payload.items.0.name")
	runs, listErr := runtimeStore.ListRuns(context.Background(), fruntime.RunFilter{})
	if listErr != nil {
		t.Fatalf("ListRuns(): %v", listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("entry validation persisted runs: %#v", runs)
	}
}

func TestGraphRunnerValidatesStateSchemaAtResumeInput(t *testing.T) {
	t.Parallel()

	workflow := singleResultNodeGraph(t, "approval", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{Command: core.Command{Suspend: &core.SuspendRequest{Value: "approval"}}}, nil
	})
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore,
		fruntime.WithStateSchemas(nestedPayloadSchemas()))
	pausedRun, _, err := runner.Start(context.Background(), validNestedPayloadState())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if pausedRun.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", pausedRun.Status)
	}

	_, _, err = runner.Resume(context.Background(), pausedRun.RunID, invalidNestedPayloadState())
	assertStateValidationError(t, err, "resume input", "shared.payload.items.0.name")
	persisted, getErr := runner.GetRun(context.Background(), pausedRun.RunID)
	if getErr != nil {
		t.Fatalf("GetRun(): %v", getErr)
	}
	if persisted.Status != fruntime.RunStatusPaused || persisted.Revision != pausedRun.Revision {
		t.Fatalf("resume validation changed run: before=%#v after=%#v", pausedRun, persisted)
	}
}

func TestGraphRunnerValidatesStateSchemaAtOutput(t *testing.T) {
	t.Parallel()

	workflow := singleResultNodeGraph(t, "write", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("payload", "items"), []any{map[string]any{"name": 42}})
	})
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore,
		fruntime.WithStateSchemas(nestedPayloadSchemas()))
	run, _, err := runner.Start(context.Background(), validNestedPayloadState())
	if err == nil || !strings.Contains(err.Error(), `output state validation failed at "shared.payload.items.0.name"`) {
		t.Fatalf("Start() error = %v, want output path diagnostic", err)
	}
	if run.Status != fruntime.RunStatusFailed || run.ErrorCode != "output_schema_validation_failed" || !strings.Contains(run.ErrorMessage, "shared.payload.items.0.name") {
		t.Fatalf("failed run = %#v", run)
	}
	persisted, getErr := runner.GetRun(context.Background(), run.RunID)
	if getErr != nil {
		t.Fatalf("GetRun(): %v", getErr)
	}
	if persisted.ErrorCode != run.ErrorCode || persisted.ErrorMessage != run.ErrorMessage {
		t.Fatalf("persisted failure = %#v, want %#v", persisted, run)
	}
}

func TestGraphRunnerValidatesStateSchemaAtRestore(t *testing.T) {
	t.Parallel()

	workflow := singleResultNodeGraph(t, "restore", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.Success(), nil
	})
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	codec := state.NewJSONStateCodec("")
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, codec, runtimeStore,
		fruntime.WithStateSchemas(nestedPayloadSchemas()))
	snapshot, err := state.SnapshotFromStateWithRuntime(invalidNestedPayloadState(), state.RuntimeState{RunID: "run-restore"}, nil)
	if err != nil {
		t.Fatalf("SnapshotFromStateWithRuntime(): %v", err)
	}
	snapshot.Version = codec.Version()
	payload, err := codec.Encode(snapshot)
	if err != nil {
		t.Fatalf("Encode(): %v", err)
	}
	record := fruntime.CheckpointRecord{
		CheckpointID: "checkpoint-restore", RunID: "run-restore", Stage: fruntime.CheckpointAfterWave,
		StateCodec: codec.Name(), StateVersion: codec.Version(),
	}
	if err := runtimeStore.Save(context.Background(), record, payload); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	_, err = runner.LoadCheckpointState(context.Background(), record.CheckpointID)
	assertStateValidationError(t, err, "restore", "shared.payload.items.0.name")
}

func TestGraphRunnerValidatesDynamicSendInputSchema(t *testing.T) {
	t.Parallel()

	var workerCalls atomic.Int32
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "mapper", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{Command: core.Command{Send: []core.Send{{
			Target: "worker",
			Input:  state.NewPatch(state.PatchOp{Kind: state.OpSet, Path: state.Shared("payload"), Value: invalidNestedPayload()}),
		}}}}, nil
	})
	mustAddResultNode(t, workflow, "worker", func(core.Context, *state.Access) (core.NodeResult, error) {
		workerCalls.Add(1)
		return core.Success(), nil
	})
	if err := workflow.SetEntryPoint("mapper"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("worker"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("mapper", "worker"); err != nil {
		t.Fatal(err)
	}
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	workerContract := state.NewContract(state.FieldAccess{
		Path: state.Shared("payload"), Mode: state.AccessRead, Required: true, Merge: state.MergeReplace,
		Schema: nestedPayloadSchema(),
	})
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore,
		fruntime.WithNodeContracts(map[string]state.Contract{"worker": workerContract}),
		fruntime.WithContractValidation(core.ContractValidationStrict))

	run, _, err := runner.Start(context.Background(), validNestedPayloadState())
	if err == nil || !strings.Contains(err.Error(), `send input state validation failed at "shared.payload.items.0.name"`) {
		t.Fatalf("Start() error = %v, want send input path diagnostic", err)
	}
	if run.Status != fruntime.RunStatusFailed || !strings.Contains(run.ErrorMessage, "shared.payload.items.0.name") {
		t.Fatalf("failed run = %#v", run)
	}
	if workerCalls.Load() != 0 {
		t.Fatalf("worker executed %d times with invalid Send input", workerCalls.Load())
	}
}

func TestGraphRunnerValidatesDynamicSendReducerInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation state.PatchOp
		wantErr   string
	}{
		{
			name: "registered reducer preserves large integer",
			operation: state.PatchOp{
				Kind: state.OpReduce, Path: state.Shared("total"), Reducer: "sum.v1", Value: int64(2),
			},
		},
		{
			name: "declared reducer cannot be bypassed",
			operation: state.PatchOp{
				Kind: state.OpSet, Path: state.Shared("total"), Value: int64(2),
			},
			wantErr: `must use reducer "sum.v1"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reg := builtin.NewDefaultRegistry()
			if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
				NodeTypeSchema: dsl.NodeTypeSchema{Type: "test"},
				Build: func(*registry.BuildContext, registry.ResolvedNodeSpec) (core.Node, error) {
					return nil, nil
				},
			}); err != nil {
				t.Fatalf("register test node type: %v", err)
			}
			var workerCalls atomic.Int32
			var observedTotal atomic.Int64
			workflow := NewGraph(reg)
			mustAddResultNode(t, workflow, "mapper", func(core.Context, *state.Access) (core.NodeResult, error) {
				return core.NodeResult{Command: core.Command{Send: []core.Send{{
					Target: "worker", Input: state.NewPatch(test.operation),
				}}}}, nil
			})
			mustAddResultNode(t, workflow, "worker", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
				workerCalls.Add(1)
				value, ok := access.ReadAny(state.Shared("total"))
				if !ok {
					return core.NodeResult{}, errors.New("worker total is missing")
				}
				switch total := value.(type) {
				case int:
					observedTotal.Store(int64(total))
				case int64:
					observedTotal.Store(total)
				default:
					return core.NodeResult{}, errors.New("worker total has unexpected type")
				}
				return core.Success(), nil
			})
			if err := workflow.SetEntryPoint("mapper"); err != nil {
				t.Fatal(err)
			}
			if err := workflow.SetFinishPoint("worker"); err != nil {
				t.Fatal(err)
			}
			if err := workflow.AddEdge("mapper", "worker"); err != nil {
				t.Fatal(err)
			}
			workflow.setNodeContracts(map[string]state.Contract{
				"worker": state.NewContract(state.FieldAccess{
					Path: state.Shared("total"), Mode: state.AccessReadWrite,
					Merge: state.MergeReplace, Reducer: "sum.v1", Schema: state.JSONSchema{"type": "integer"},
				}),
			})
			const initialTotal int64 = 9007199254740993
			_, directErr := workflow.Run(context.Background(), state.FromShared(map[string]any{"total": initialTotal}))
			if test.wantErr != "" {
				if directErr == nil || !strings.Contains(directErr.Error(), test.wantErr) {
					t.Fatalf("Graph.Run() error = %v, want substring %q", directErr, test.wantErr)
				}
				if workerCalls.Load() != 0 {
					t.Fatalf("Graph.Run() worker calls = %d", workerCalls.Load())
				}
			} else {
				if directErr != nil {
					t.Fatalf("Graph.Run(): %v", directErr)
				}
				if workerCalls.Load() != 1 || observedTotal.Load() != initialTotal+2 {
					t.Fatalf("Graph.Run() worker calls = %d, total = %d", workerCalls.Load(), observedTotal.Load())
				}
			}
			workerCalls.Store(0)
			observedTotal.Store(0)

			runtimeStore := fruntime.NewMemoryRuntimeStore()
			runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
			run, _, err := runner.Start(context.Background(), state.FromShared(map[string]any{"total": initialTotal}))
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Start() error = %v, want substring %q", err, test.wantErr)
				}
				if run.Status != fruntime.RunStatusFailed || workerCalls.Load() != 0 {
					t.Fatalf("run = %#v, worker calls = %d", run, workerCalls.Load())
				}
				return
			}
			if err != nil {
				t.Fatalf("Start(): %v", err)
			}
			if workerCalls.Load() != 1 || observedTotal.Load() != initialTotal+2 {
				t.Fatalf("worker calls = %d, total = %d", workerCalls.Load(), observedTotal.Load())
			}
		})
	}
}

func TestGraphRunnerValidatesNodePatchResultSchema(t *testing.T) {
	t.Parallel()

	workflow := singleResultNodeGraph(t, "writer", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{Patch: state.NewPatch(state.PatchOp{
			Kind: state.OpSet, Path: state.Shared("payload"), Value: invalidNestedPayload(),
		})}, nil
	})
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	writerContract := state.NewContract(state.FieldAccess{
		Path: state.Shared("payload"), Mode: state.AccessWrite, Required: true, Merge: state.MergeReplace,
		Schema: nestedPayloadSchema(),
	})
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore,
		fruntime.WithNodeContracts(map[string]state.Contract{"writer": writerContract}),
		fruntime.WithContractValidation(core.ContractValidationStrict))

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), `node output state validation failed at "shared.payload.items.0.name"`) {
		t.Fatalf("Start() error = %v, want node output path diagnostic", err)
	}
	if run.Status != fruntime.RunStatusFailed || !strings.Contains(run.ErrorMessage, "shared.payload.items.0.name") {
		t.Fatalf("failed run = %#v", run)
	}
	events, listErr := runner.ListEvents(run.RunID)
	if listErr != nil {
		t.Fatalf("ListEvents(): %v", listErr)
	}
	foundViolation := false
	for _, event := range events {
		if event.Type == fruntime.EventContractViolation && strings.Contains(string(event.Payload), "shared.payload.items.0.name") {
			foundViolation = true
		}
	}
	if !foundViolation {
		t.Fatalf("contract events lack nested output diagnostic: %#v", events)
	}
}

func singleResultNodeGraph(t *testing.T, nodeID string, execute node.ExecuteFunc) *Graph {
	t.Helper()
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, nodeID, execute)
	if err := workflow.SetEntryPoint(nodeID); err != nil {
		t.Fatalf("SetEntryPoint(%q): %v", nodeID, err)
	}
	if err := workflow.SetFinishPoint(nodeID); err != nil {
		t.Fatalf("SetFinishPoint(%q): %v", nodeID, err)
	}
	return workflow
}

func nestedPayloadSchemas() map[string]state.JSONSchema {
	return map[string]state.JSONSchema{"shared.payload": nestedPayloadSchema()}
}

func nestedPayloadSchema() state.JSONSchema {
	return state.JSONSchema{
		"type":     "object",
		"required": []string{"items"},
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"name"},
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func validNestedPayloadState() *state.State {
	return state.FromShared(map[string]any{"payload": map[string]any{
		"items": []any{map[string]any{"name": "valid"}},
	}})
}

func invalidNestedPayloadState() *state.State {
	return state.FromShared(map[string]any{"payload": invalidNestedPayload()})
}

func invalidNestedPayload() map[string]any {
	return map[string]any{"items": []any{map[string]any{"name": 42}}}
}

func assertStateValidationError(t *testing.T, err error, boundary, path string) {
	t.Helper()
	var validationErr *state.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want *state.ValidationError", err)
	}
	if validationErr.Boundary != boundary {
		t.Fatalf("validation boundary = %q, want %q", validationErr.Boundary, boundary)
	}
	for _, issue := range validationErr.Issues {
		if issue.Path == path {
			return
		}
	}
	t.Fatalf("validation issues = %#v, want path %q", validationErr.Issues, path)
}
