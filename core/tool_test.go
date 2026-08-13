package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/state"
)

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
