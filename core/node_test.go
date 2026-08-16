package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/state"
)

type isolationTestNode struct {
	execute func(*state.Access) error
	result  NodeResult
}

func (isolationTestNode) ID() string          { return "isolation" }
func (isolationTestNode) Name() string        { return "isolation" }
func (isolationTestNode) Description() string { return "" }

func (node isolationTestNode) Execute(_ Context, access *state.Access) (NodeResult, error) {
	if node.execute != nil {
		if err := node.execute(access); err != nil {
			return NodeResult{}, err
		}
	}
	return node.result, nil
}

type nodeOpaqueValue struct {
	values []string
}

func TestExecuteNodeRejectsOpaqueMutableState(t *testing.T) {
	t.Run("input", func(t *testing.T) {
		invoked := false
		_, err := ExecuteNodeWithOptions(context.Background(), state.FromShared(map[string]any{
			"opaque": &nodeOpaqueValue{values: []string{"source"}},
		}), isolationTestNode{execute: func(*state.Access) error {
			invoked = true
			return nil
		}}, NodeExecutionOptions{})
		if err == nil || !strings.Contains(err.Error(), "node input state cannot be safely cloned") {
			t.Fatalf("ExecuteNodeWithOptions() error = %v", err)
		}
		if invoked {
			t.Fatal("node executed with an opaque mutable input")
		}
	})

	t.Run("output", func(t *testing.T) {
		_, err := ExecuteNodeWithOptions(context.Background(), state.NewState(), isolationTestNode{execute: func(access *state.Access) error {
			return access.SetAny(state.Shared("opaque"), &nodeOpaqueValue{values: []string{"result"}})
		}}, NodeExecutionOptions{})
		if err == nil || !strings.Contains(err.Error(), "node result cannot be safely cloned") {
			t.Fatalf("ExecuteNodeWithOptions() error = %v", err)
		}
		if class := ClassifyError(err); class != ErrorNonRetryable {
			t.Fatalf("ClassifyError() = %q, want %q", class, ErrorNonRetryable)
		}
	})
}

func TestExecuteNodeIsolatesMutableNodeResult(t *testing.T) {
	patch := state.NewPatch(state.PatchOp{
		Kind:  state.OpSet,
		Path:  state.Shared("patch"),
		Value: map[string]any{"items": []any{"patch"}},
	})
	sendInput := state.NewPatch(state.PatchOp{
		Kind:  state.OpSet,
		Path:  state.Shared("send"),
		Value: map[string]any{"items": []any{"send"}},
	})
	gotoTargets := []NodeRef{"first", "second"}
	sends := []Send{{Target: "worker", Input: sendInput, CorrelationKey: "correlation", OrderKey: "order"}}
	suspendValue := map[string]any{"items": []any{"suspend"}}
	returnValue := map[string]any{"items": []any{"return"}}
	eventPayload := map[string]any{"items": []any{"event"}}
	events := []EventDraft{{Type: "result.event", Payload: eventPayload}}
	artifactData := []byte("artifact")
	artifacts := []ArtifactDraft{{Type: "result.artifact", MIMEType: "text/plain", Data: artifactData}}
	source := NodeResult{
		Patch: patch,
		Command: Command{
			Goto:    gotoTargets,
			Send:    sends,
			Suspend: &SuspendRequest{Value: suspendValue},
			Return:  &ReturnCommand{Value: returnValue},
		},
		Events:    events,
		Artifacts: artifacts,
	}

	execution, err := ExecuteNodeWithOptions(context.Background(), state.NewState(), isolationTestNode{result: source}, NodeExecutionOptions{})
	if err != nil {
		t.Fatalf("ExecuteNodeWithOptions() error = %v", err)
	}

	gotoTargets[0] = "source-goto"
	sends[0].Target = "source-send"
	sends[0].Input = state.NewPatch(state.PatchOp{Kind: state.OpSet, Path: state.Shared("send"), Value: "source-send"})
	suspendValue["items"].([]any)[0] = "source-suspend"
	returnValue["items"].([]any)[0] = "source-return"
	events[0].Type = "source.event"
	eventPayload["items"].([]any)[0] = "source-event"
	artifacts[0].Type = "source.artifact"
	artifactData[0] = 'S'

	if got := execution.Node.Command.Goto[0]; got != "first" {
		t.Fatalf("Node.Command.Goto[0] = %q, want first", got)
	}
	if got := execution.Node.Command.Send[0].Target; got != "worker" {
		t.Fatalf("Node.Command.Send[0].Target = %q, want worker", got)
	}
	assertPatchItem(t, execution.Patch, "patch")
	assertPatchItem(t, execution.Node.Patch, "patch")
	assertPatchItem(t, execution.Node.Command.Send[0].Input, "send")
	assertNodeResultValue(t, execution.Node.Command.Suspend.Value, "suspend")
	assertNodeResultValue(t, execution.Node.Command.Return.Value, "return")
	if got := execution.Node.Events[0].Type; got != "result.event" {
		t.Fatalf("Node.Events[0].Type = %q, want result.event", got)
	}
	assertNodeResultValue(t, execution.Node.Events[0].Payload, "event")
	if got := execution.Node.Artifacts[0].Type; got != "result.artifact" {
		t.Fatalf("Node.Artifacts[0].Type = %q, want result.artifact", got)
	}
	if got := string(execution.Node.Artifacts[0].Data); got != "artifact" {
		t.Fatalf("Node.Artifacts[0].Data = %q, want artifact", got)
	}

	execution.Node.Command.Goto[0] = "returned-goto"
	execution.Node.Command.Send[0].Target = "returned-send"
	execution.Node.Command.Suspend.Value.(map[string]any)["items"].([]any)[0] = "returned-suspend"
	execution.Node.Command.Return.Value.(map[string]any)["items"].([]any)[0] = "returned-return"
	execution.Node.Events[0].Payload.(map[string]any)["items"].([]any)[0] = "returned-event"
	execution.Node.Artifacts[0].Data[0] = 'R'
	if gotoTargets[0] != "source-goto" || sends[0].Target != "source-send" {
		t.Fatal("mutating ExecutionResult.Node changed source command slices")
	}
	assertNodeResultValue(t, suspendValue, "source-suspend")
	assertNodeResultValue(t, returnValue, "source-return")
	assertNodeResultValue(t, eventPayload, "source-event")
	if got := string(artifactData); got != "Srtifact" {
		t.Fatalf("source artifact data = %q, want Srtifact", got)
	}

	execution.Node.Patch = state.NewPatch(state.PatchOp{Kind: state.OpSet, Path: state.Shared("patch"), Value: "node-only"})
	assertPatchItem(t, execution.Patch, "patch")
}

func TestExecuteNodeRejectsOpaqueMutableNodeResult(t *testing.T) {
	tests := []struct {
		name       string
		result     NodeResult
		wantDetail string
	}{
		{
			name: "patch",
			result: NodeResult{Patch: state.NewPatch(state.PatchOp{
				Kind: state.OpSet, Path: state.Shared("opaque"), Value: &nodeOpaqueValue{values: []string{"patch"}},
			})},
			wantDetail: "patch: operation 0 value",
		},
		{
			name: "send input",
			result: NodeResult{Command: Command{Send: []Send{{Target: "worker", Input: state.NewPatch(state.PatchOp{
				Kind: state.OpSet, Path: state.Shared("opaque"), Value: &nodeOpaqueValue{values: []string{"send"}},
			})}}}},
			wantDetail: "command: send 0 input: operation 0 value",
		},
		{
			name:       "suspend value",
			result:     NodeResult{Command: Command{Suspend: &SuspendRequest{Value: &nodeOpaqueValue{values: []string{"suspend"}}}}},
			wantDetail: "command: suspend value",
		},
		{
			name:       "return value",
			result:     NodeResult{Command: Command{Return: &ReturnCommand{Value: &nodeOpaqueValue{values: []string{"return"}}}}},
			wantDetail: "command: return value",
		},
		{
			name:       "event payload",
			result:     NodeResult{Events: []EventDraft{{Type: "opaque", Payload: &nodeOpaqueValue{values: []string{"event"}}}}},
			wantDetail: "event 0 payload",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExecuteNodeWithOptions(context.Background(), state.NewState(), isolationTestNode{result: test.result}, NodeExecutionOptions{})
			if err == nil {
				t.Fatal("ExecuteNodeWithOptions() error = nil")
			}
			if !strings.Contains(err.Error(), "node result cannot be safely cloned") || !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("ExecuteNodeWithOptions() error = %v", err)
			}
			if class := ClassifyError(err); class != ErrorNonRetryable {
				t.Fatalf("ClassifyError() = %q, want %q", class, ErrorNonRetryable)
			}
		})
	}
}

func TestExecuteNodeRunsReducerOnceWithWriteValidation(t *testing.T) {
	reducer := &nodeCountingReducer{}
	contract := state.NewContract(state.FieldAccess{
		Path:    state.Shared("total"),
		Mode:    state.AccessReadWrite,
		Reducer: "count.v1",
		Schema:  state.JSONSchema{"type": "integer"},
	})
	node := isolationTestNode{result: NodeResult{Patch: state.NewPatch(state.PatchOp{
		Kind:    state.OpReduce,
		Path:    state.Shared("total"),
		Value:   2,
		Reducer: "count.v1",
	})}}
	execution, err := ExecuteNodeWithOptions(context.Background(), state.FromShared(map[string]any{"total": 1}), node, NodeExecutionOptions{
		Contract:       &contract,
		ValidateWrites: true,
		Reducers:       map[string]state.Reducer{"count.v1": reducer},
	})
	if err != nil {
		t.Fatalf("ExecuteNodeWithOptions() error = %v", err)
	}
	if reducer.calls != 1 {
		t.Fatalf("Reducer calls = %d, want 1", reducer.calls)
	}
	value, ok := state.ReadPath(execution.State, "shared.total")
	if !ok || value != 2 {
		t.Fatalf("result total = %#v, want 2", value)
	}
}

func TestExecuteNodeRejectsReservedWritesWithoutContract(t *testing.T) {
	for _, path := range []state.Path{state.Internal("graph_scheduler"), state.Runtime("run_id")} {
		t.Run(path.String(), func(t *testing.T) {
			node := isolationTestNode{result: NodeResult{Patch: state.NewPatch(state.PatchOp{
				Kind:  state.OpSet,
				Path:  path,
				Value: "forged",
			})}}
			_, err := ExecuteNodeWithOptions(context.Background(), state.NewState(), node, NodeExecutionOptions{})
			if err == nil || !strings.Contains(err.Error(), "reserved path") {
				t.Fatalf("ExecuteNodeWithOptions() error = %v, want reserved path", err)
			}
		})
	}
}

func assertPatchItem(t *testing.T, patch state.Patch, want string) {
	t.Helper()
	operations := patch.Ops()
	if len(operations) != 1 {
		t.Fatalf("Patch.Ops() length = %d, want 1", len(operations))
	}
	assertNodeResultValue(t, operations[0].Value, want)
}

func assertNodeResultValue(t *testing.T, value any, want string) {
	t.Helper()
	valueMap, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value type = %T, want map[string]any", value)
	}
	items, ok := valueMap["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("value items = %#v, want one-item []any", valueMap["items"])
	}
	if got, ok := items[0].(string); !ok || got != want {
		t.Fatalf("value item = %#v, want %q", items[0], want)
	}
}

type nodeCountingReducer struct {
	calls int
}

func (reducer *nodeCountingReducer) Reduce(_, incoming any) (any, error) {
	reducer.calls++
	return incoming, nil
}
