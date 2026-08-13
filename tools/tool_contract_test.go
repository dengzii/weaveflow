package tools

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestBundledToolContractsDeclareSchemasAndGovernance(t *testing.T) {
	tests := []struct {
		name        string
		tool        Tool
		permissions []string
		approval    core.ToolApprovalMode
	}{
		{name: "bash", tool: NewBash(), permissions: []string{"process.execute"}, approval: core.ToolApprovalRequired},
		{name: "calculator", tool: NewCalculator()},
		{name: "current_time", tool: NewCurrentTime()},
		{name: "edit", tool: NewEdit(), permissions: []string{"filesystem.write"}, approval: core.ToolApprovalRequired},
		{name: "glob", tool: NewGlob(), permissions: []string{"filesystem.read"}},
		{name: "grep", tool: NewGrep(), permissions: []string{"filesystem.read"}},
		{name: "outline", tool: NewOutline(), permissions: []string{"filesystem.read"}},
		{name: "read", tool: NewRead(), permissions: []string{"filesystem.read"}},
		{name: "tree", tool: NewTree(), permissions: []string{"filesystem.read"}},
		{name: "web_fetch", tool: NewWebFetch(), permissions: []string{"network.http"}},
		{name: "web_search", tool: NewWebSearch(), permissions: []string{"network.search"}},
		{name: "write", tool: NewWrite(), permissions: []string{"filesystem.write"}, approval: core.ToolApprovalRequired},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tool := testCase.tool
			if tool.Function == nil || tool.Handler == nil {
				t.Fatalf("tool contract is incomplete: %#v", tool)
			}
			if tool.Name() != testCase.name {
				t.Fatalf("tool name = %q, want %q", tool.Name(), testCase.name)
			}
			if err := state.ValidateJSONSchemaDefinition(tool.Function.Parameters); err != nil {
				t.Fatalf("input schema: %v", err)
			}
			if len(tool.Function.OutputSchema) == 0 {
				t.Fatal("output schema is missing")
			}
			if err := state.ValidateJSONSchemaDefinition(tool.Function.OutputSchema); err != nil {
				t.Fatalf("output schema: %v", err)
			}
			if !reflect.DeepEqual(tool.Permissions, testCase.permissions) {
				t.Fatalf("permissions = %#v, want %#v", tool.Permissions, testCase.permissions)
			}
			if tool.Approval != testCase.approval {
				t.Fatalf("approval = %q, want %q", tool.Approval, testCase.approval)
			}
		})
	}
}

func TestBundledWriteToolUsesNativeStructuredResult(t *testing.T) {
	workspace := t.TempDir()
	ctx := core.WithEnvironment(context.Background(), map[string]string{toolWorkspaceEnv: workspace})
	ctx = core.WithToolPermissions(ctx, "filesystem.write")
	ctx = core.WithToolApprover(ctx, core.ToolApproverFunc(func(context.Context, core.ToolApprovalRequest) (core.ToolApprovalDecision, error) {
		return core.ToolApprovalDecision{Approved: true, Actor: "test"}, nil
	}))

	result, err := core.ExecuteTool(ctx, NewWrite(), toolCallForTest("write", `{"file_path":"empty.txt","content":""}`))
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	value, ok := result.Value.(writeResponse)
	if !ok || value.Path != "empty.txt" || value.BytesWritten != 0 || !value.Exists {
		t.Fatalf("structured result = %#v", result)
	}
	if !strings.Contains(result.Content, `"bytes_written":0`) {
		t.Fatalf("result content = %q", result.Content)
	}
}
