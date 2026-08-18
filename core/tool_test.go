package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/state"
)

type toolObserverOpaqueValue struct {
	values []string
}

func TestToolObserverCannotMutateReturnedError(t *testing.T) {
	originalErr := &mutableObserverError{message: "tool failed"}
	ctx := WithToolExecutionObserver(context.Background(), func(_ context.Context, event ToolExecutionEvent) {
		if event.Stage != ToolExecutionFailed {
			return
		}
		if observedErr, ok := event.Err.(*mutableObserverError); ok {
			observedErr.message = "observer changed error"
		}
	})
	tool := Tool{
		Function: &llms.FunctionDefinition{Name: "fail"},
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			return llms.ToolResult{}, originalErr
		},
	}
	_, err := ExecuteTool(ctx, tool, testToolCall("fail", `{}`))
	if err != originalErr {
		t.Fatalf("ExecuteTool() error = %v, want original error", err)
	}
	if originalErr.message != "tool failed" {
		t.Fatalf("tool error message = %q", originalErr.message)
	}
}

func TestExecuteToolValidatesStructuredInputAndOutput(t *testing.T) {
	handled := 0
	tool := Tool{
		Function: &llms.FunctionDefinition{
			Name: "double",
			Parameters: state.JSONSchema{
				"type": "object",
				"properties": state.JSONSchema{
					"value": state.JSONSchema{"type": "integer"},
				},
				"required":             []string{"value"},
				"additionalProperties": false,
			},
			OutputSchema: state.JSONSchema{
				"type": "object",
				"properties": state.JSONSchema{
					"value": state.JSONSchema{"type": "integer"},
				},
				"required":             []string{"value"},
				"additionalProperties": false,
			},
		},
		Handler: func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			handled++
			var input struct {
				Value int `json:"value"`
			}
			if err := DecodeToolArguments(call, &input); err != nil {
				return llms.ToolResult{}, err
			}
			return llms.ToolResult{Value: map[string]any{"value": input.Value * 2}}, nil
		},
	}

	_, err := ExecuteTool(context.Background(), tool, testToolCall("double", `{"value":"bad"}`))
	if err == nil || ClassifyError(err) != ErrorInvalidInput || handled != 0 {
		t.Fatalf("invalid input error = %v, handled = %d", err, handled)
	}

	result, err := ExecuteTool(context.Background(), tool, testToolCall("double", `{"value":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if handled != 1 || result.ToolCallID != "call-1" || result.Name != "double" {
		t.Fatalf("result = %#v, handled = %d", result, handled)
	}

	tool.Handler = func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
		return llms.ToolResult{Value: map[string]any{"value": "invalid"}}, nil
	}
	_, err = ExecuteTool(context.Background(), tool, testToolCall("double", `{"value":4}`))
	if err == nil || ClassifyError(err) != ErrorNonRetryable || !strings.Contains(err.Error(), "tool output") {
		t.Fatalf("invalid output error = %v", err)
	}
}

func TestExecuteToolEnforcesPermissionsAndApprovalLifecycle(t *testing.T) {
	handled := 0
	tool := Tool{
		Function:    &llms.FunctionDefinition{Name: "write_file"},
		Permissions: []string{"filesystem.write"},
		Approval:    ToolApprovalRequired,
		Handler: func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			handled++
			return llms.ToolResult{ToolCallID: call.ID, Content: "written"}, nil
		},
	}

	tests := []struct {
		name       string
		ctx        context.Context
		wantStages []ToolExecutionStage
		wantError  bool
	}{
		{
			name:       "missing permission",
			ctx:        context.Background(),
			wantStages: []ToolExecutionStage{ToolExecutionRequested, ToolExecutionDenied},
			wantError:  true,
		},
		{
			name:       "missing approval",
			ctx:        WithToolPermissions(context.Background(), "filesystem.write"),
			wantStages: []ToolExecutionStage{ToolExecutionRequested, ToolExecutionApprovalNeeded, ToolExecutionDenied},
			wantError:  true,
		},
		{
			name: "denied approval",
			ctx: WithToolApprover(
				WithToolPermissions(context.Background(), "filesystem.write"),
				ToolApproverFunc(func(context.Context, ToolApprovalRequest) (ToolApprovalDecision, error) {
					return ToolApprovalDecision{ApprovalID: "deny-1", Approved: false, Actor: "reviewer", Reason: "unsafe"}, nil
				}),
			),
			wantStages: []ToolExecutionStage{ToolExecutionRequested, ToolExecutionApprovalNeeded, ToolExecutionDenied},
			wantError:  true,
		},
		{
			name: "approved",
			ctx: WithToolApprover(
				WithToolPermissions(context.Background(), "filesystem.write"),
				ToolApproverFunc(func(_ context.Context, request ToolApprovalRequest) (ToolApprovalDecision, error) {
					if len(request.Permissions) != 1 || request.Permissions[0] != "filesystem.write" {
						t.Fatalf("approval request = %#v", request)
					}
					return ToolApprovalDecision{ApprovalID: "allow-1", Approved: true, Actor: "reviewer"}, nil
				}),
			),
			wantStages: []ToolExecutionStage{
				ToolExecutionRequested,
				ToolExecutionApprovalNeeded,
				ToolExecutionApproved,
				ToolExecutionStarted,
				ToolExecutionReturned,
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stages []ToolExecutionStage
			ctx := WithToolExecutionObserver(testCase.ctx, func(_ context.Context, event ToolExecutionEvent) {
				stages = append(stages, event.Stage)
			})
			before := handled
			_, err := ExecuteTool(ctx, tool, testToolCall("write_file", `{}`))
			if (err != nil) != testCase.wantError {
				t.Fatalf("ExecuteTool() error = %v", err)
			}
			if strings.Join(toolStages(stages), ",") != strings.Join(toolStages(testCase.wantStages), ",") {
				t.Fatalf("stages = %#v, want %#v", stages, testCase.wantStages)
			}
			if testCase.wantError && handled != before {
				t.Fatalf("denied tool executed: before=%d after=%d", before, handled)
			}
		})
	}
	if handled != 1 {
		t.Fatalf("approved handler calls = %d, want 1", handled)
	}
}

func TestToolExecutionClonesObserverAndApprovalInputs(t *testing.T) {
	permissionTool := Tool{
		Function:    &llms.FunctionDefinition{Name: "protected"},
		Permissions: []string{"filesystem.write"},
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			t.Fatal("permission-bypassed tool executed")
			return llms.ToolResult{}, nil
		},
	}
	ctx := WithToolExecutionObserver(context.Background(), func(_ context.Context, event ToolExecutionEvent) {
		if event.Stage == ToolExecutionRequested {
			event.Tool.Permissions[0] = ""
		}
	})
	if _, err := ExecuteTool(ctx, permissionTool, testToolCall("protected", `{}`)); err == nil || ClassifyError(err) != ErrorPermissionDenied {
		t.Fatalf("permission mutation changed authorization: %v", err)
	}

	handled := 0
	argumentTool := Tool{
		Function: &llms.FunctionDefinition{
			Name: "echo_number",
			Parameters: state.JSONSchema{
				"type": "object",
				"properties": state.JSONSchema{
					"value": state.JSONSchema{"type": "integer"},
				},
				"required":             []string{"value"},
				"additionalProperties": false,
			},
		},
		Approval:    ToolApprovalRequired,
		Permissions: []string{"filesystem.write"},
		Handler: func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			handled++
			var input struct {
				Value int `json:"value"`
			}
			if err := DecodeToolArguments(call, &input); err != nil {
				return llms.ToolResult{}, err
			}
			if input.Value != 7 {
				t.Fatalf("handler received mutated arguments: %d", input.Value)
			}
			return llms.ToolResult{Content: "ok"}, nil
		},
	}
	call := testToolCall("echo_number", `{"value":7}`)
	argumentContext := WithToolPermissions(context.Background(), "filesystem.write")
	argumentContext = WithToolExecutionObserver(argumentContext, func(_ context.Context, event ToolExecutionEvent) {
		if event.Stage == ToolExecutionApprovalNeeded {
			event.Call.FunctionCall.Arguments = []byte(`{"value":1}`)
		}
	})
	argumentContext = WithToolApprover(argumentContext, ToolApproverFunc(func(_ context.Context, request ToolApprovalRequest) (ToolApprovalDecision, error) {
		request.ToolCall.FunctionCall.Arguments = []byte(`{"value":2}`)
		return ToolApprovalDecision{Approved: true}, nil
	}))
	if _, err := ExecuteTool(argumentContext, argumentTool, call); err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if handled != 1 || string(call.FunctionCall.Arguments) != `{"value":7}` {
		t.Fatalf("tool call was mutated: handled=%d call=%s", handled, call.FunctionCall.Arguments)
	}
}

func TestToolObserverPreservesCyclesWithoutSharingResult(t *testing.T) {
	cyclic := map[string]any{"value": "source"}
	cyclic["self"] = cyclic
	ctx := WithToolExecutionObserver(context.Background(), func(_ context.Context, event ToolExecutionEvent) {
		if event.Stage != ToolExecutionReturned {
			return
		}
		if event.CloneError != nil {
			t.Fatalf("observer CloneError = %v", event.CloneError)
		}
		observed := event.Result.Value.(map[string]any)
		if reflect.ValueOf(observed).Pointer() != reflect.ValueOf(observed["self"]).Pointer() {
			t.Fatal("tool observer result lost its map cycle")
		}
		observed["value"] = "observer"
	})
	result, err := ExecuteTool(ctx, Tool{
		Function: &llms.FunctionDefinition{Name: "cyclic"},
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			return llms.ToolResult{Value: cyclic}, nil
		},
	}, testToolCall("cyclic", `{}`))
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if result.Value.(map[string]any)["value"] != "source" || cyclic["value"] != "source" {
		t.Fatalf("observer mutated actual result: %#v", result.Value)
	}
}

func TestToolObserverOmitsOpaqueResultValue(t *testing.T) {
	opaque := &toolObserverOpaqueValue{values: []string{"source"}}
	var returned ToolExecutionEvent
	ctx := WithToolExecutionObserver(context.Background(), func(_ context.Context, event ToolExecutionEvent) {
		if event.Stage == ToolExecutionReturned {
			returned = event
		}
	})
	result, err := ExecuteTool(ctx, Tool{
		Function: &llms.FunctionDefinition{Name: "opaque"},
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			return llms.ToolResult{Value: opaque}, nil
		},
	}, testToolCall("opaque", `{}`))
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if returned.CloneError == nil || returned.Result.Value != nil {
		t.Fatalf("returned observer value = %#v, clone error = %v", returned.Result.Value, returned.CloneError)
	}
	if result.Value != opaque || opaque.values[0] != "source" {
		t.Fatalf("observer clone changed actual result: %#v", result.Value)
	}
}

func TestWithToolsDeepClonesPermissionAndDefinition(t *testing.T) {
	permissions := []string{"filesystem.write"}
	function := &llms.FunctionDefinition{Name: "protected"}
	ctx := WithTools(context.Background(), map[string]Tool{
		"protected": {Function: function, Permissions: permissions},
	})
	permissions[0] = ""
	function.Name = "changed"
	stored := ToolsFromContext(ctx)["protected"]
	if stored.Name() != "protected" || stored.Permissions[0] != "filesystem.write" {
		t.Fatalf("WithTools() retained caller aliases: %#v", stored)
	}
	stored.Permissions[0] = ""
	stored.Function.Name = "changed-again"
	again := ToolsFromContext(ctx)["protected"]
	if again.Name() != "protected" || again.Permissions[0] != "filesystem.write" {
		t.Fatalf("ToolsFromContext() exposed stored aliases: %#v", again)
	}
	filtered := FilterTools(ToolsFromContext(ctx), []string{"protected"})["protected"]
	filtered.Permissions[0] = ""
	if ToolsFromContext(ctx)["protected"].Permissions[0] != "filesystem.write" {
		t.Fatal("FilterTools() exposed stored permission aliases")
	}
}

func TestFindToolRejectsAmbiguousFallbackMatches(t *testing.T) {
	available := map[string]Tool{
		"first":  {Function: &llms.FunctionDefinition{Name: "shared"}},
		"second": {Function: &llms.FunctionDefinition{Name: "shared"}},
	}
	if tool, ok := FindTool(available, "shared"); ok || tool.Function != nil {
		t.Fatalf("FindTool() accepted ambiguous function-name match: %#v, %v", tool, ok)
	}
	if tool, ok := FindTool(available, "first"); !ok || tool.Name() != "shared" {
		t.Fatalf("FindTool() lost exact-key lookup: %#v, %v", tool, ok)
	}
}

func TestExecuteToolUsesStableIdempotencyKeyAndJournalsOutcome(t *testing.T) {
	parent := EffectOperation{Key: "run-task-operation", Kind: "node", Name: "writer", Class: EffectIdempotentWrite, IdempotencyKey: "run-task-operation"}
	records := make([]EffectOperation, 0, 4)
	effects := map[string]int{}
	ctx := WithEffectOperation(context.Background(), parent)
	ctx = WithEffectJournal(ctx, EffectJournalFunc(func(_ context.Context, operation EffectOperation) error {
		records = append(records, operation)
		return nil
	}))
	tool := Tool{
		Function: &llms.FunctionDefinition{Name: "write_once"},
		Effect:   EffectIdempotentWrite,
		Handler: func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			key, ok := IdempotencyKeyFromContext(ctx)
			if !ok {
				t.Fatal("tool handler has no idempotency key")
			}
			if effects[key] == 0 {
				effects[key]++
			}
			return llms.ToolResult{ToolCallID: call.ID, Name: "write_once", ProviderRequestID: "provider-1"}, nil
		},
	}
	call := testToolCall("write_once", `{}`)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := ExecuteTool(ctx, tool, call); err != nil {
			t.Fatalf("ExecuteTool() attempt %d error = %v", attempt+1, err)
		}
	}
	if len(effects) != 1 {
		t.Fatalf("business effects = %#v, want one stable key", effects)
	}
	if len(records) != 4 {
		t.Fatalf("effect records = %#v", records)
	}
	if records[0].Key == "" || records[0].IdempotencyKey != records[2].IdempotencyKey {
		t.Fatalf("idempotency keys changed across retries: %#v", records)
	}
	if records[1].Status != EffectSucceeded || records[1].ProviderRequestID != "provider-1" {
		t.Fatalf("successful outcome = %#v", records[1])
	}
}

func TestExecuteToolRecordsUnknownWriteOutcome(t *testing.T) {
	var records []EffectOperation
	ctx := WithEffectOperation(context.Background(), EffectOperation{Key: "run-task-operation", Kind: "node", Name: "writer"})
	ctx = WithEffectJournal(ctx, EffectJournalFunc(func(_ context.Context, operation EffectOperation) error {
		records = append(records, operation)
		return nil
	}))
	tool := Tool{
		Function: &llms.FunctionDefinition{Name: "uncertain_write"},
		Effect:   EffectNonIdempotentWrite,
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			return llms.ToolResult{ProviderRequestID: "provider-unknown"}, NewExecutionError(ErrorSideEffectFailed, "response lost", nil, nil)
		},
	}
	if _, err := ExecuteTool(ctx, tool, testToolCall("uncertain_write", `{}`)); err == nil {
		t.Fatal("ExecuteTool() error = nil")
	}
	if len(records) != 2 || records[1].Status != EffectUnknown || records[1].ProviderRequestID != "provider-unknown" {
		t.Fatalf("effect records = %#v", records)
	}
}

func TestExecuteToolClassifiesUnavailableWriteAsSideEffectFailure(t *testing.T) {
	ctx := WithEffectOperation(context.Background(), EffectOperation{Key: "run-task-operation", Kind: "node", Name: "writer"})
	tool := Tool{
		Function: &llms.FunctionDefinition{Name: "uncertain_write"},
		Effect:   EffectNonIdempotentWrite,
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			return llms.ToolResult{}, NewExecutionError(ErrorUnavailable, "provider unavailable", nil, nil)
		},
	}
	_, err := ExecuteTool(ctx, tool, testToolCall("uncertain_write", `{}`))
	if err == nil || ClassifyError(err) != ErrorSideEffectFailed {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
}

func TestExecuteToolDoesNotCallHandlerWhenIntentPersistenceFails(t *testing.T) {
	handled := false
	ctx := WithEffectOperation(context.Background(), EffectOperation{Key: "run-task-operation", Kind: "node", Name: "writer"})
	ctx = WithEffectJournal(ctx, EffectJournalFunc(func(context.Context, EffectOperation) error {
		return errors.New("intent persistence failed")
	}))
	tool := Tool{
		Function: &llms.FunctionDefinition{Name: "protected_write"},
		Effect:   EffectIdempotentWrite,
		Handler: func(context.Context, llms.ToolCall) (llms.ToolResult, error) {
			handled = true
			return llms.ToolResult{}, nil
		},
	}
	_, err := ExecuteTool(ctx, tool, testToolCall("protected_write", `{}`))
	if err == nil || ClassifyError(err) != ErrorSideEffectFailed || handled {
		t.Fatalf("ExecuteTool() error = %v, handled = %v", err, handled)
	}
}

func testToolCall(name, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   "call-1",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: json.RawMessage(arguments),
		},
	}
}

func toolStages(stages []ToolExecutionStage) []string {
	out := make([]string, len(stages))
	for index, stage := range stages {
		out[index] = string(stage)
	}
	return out
}
