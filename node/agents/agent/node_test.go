package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestNodeUsesExplicitTaskConversationAndResultPaths(t *testing.T) {
	t.Parallel()
	taskPath := state.Shared("request")
	conversationPath := state.Scope("researcher", "conversation")
	resultPath := state.Shared("handoff", "research")
	target := NewNode(core.WithID("researcher"))
	target.TaskPath = taskPath
	target.ConversationPath = conversationPath
	target.ResultPath = resultPath
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "research result"}}}}}
	result, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{"request": "research this"}), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, _ := state.ReadPath(result.State, resultPath.String())
	if value != "research result" {
		t.Fatalf("result = %#v", value)
	}
	view, _ := conversationcap.Bind(state.NewAccess(result.State), conversationPath)
	if len(view.Messages()) != 2 {
		t.Fatalf("conversation = %#v", view.Messages())
	}
}

func TestNodeWritesStructuredFinalOutput(t *testing.T) {
	t.Parallel()

	outputSchema := state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "integer"},
		},
		"required":             []string{"answer"},
		"additionalProperties": false,
	}
	target := NewNode(core.WithID("structured"))
	target.OutputSchema = outputSchema
	target.ResponseName = "agent_answer"
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "  {\n  \"answer\": 7\n}  "}}}}}
	result, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{
		"request": map[string]any{"input": "return an object"},
	}), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, ok := state.ReadPath(result.State, target.ResultPath.String())
	if !ok || !reflect.DeepEqual(value, map[string]any{"answer": json.Number("7")}) {
		t.Fatalf("result = %#v", value)
	}
	view, bindErr := conversationcap.Bind(state.NewAccess(result.State), target.ConversationPath)
	if bindErr != nil {
		t.Fatalf("bind conversation: %v", bindErr)
	}
	if view.FinalAnswer() != `{"answer":7}` {
		t.Fatalf("final answer = %q", view.FinalAnswer())
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	if request.ResponseName != "agent_answer" || !request.StrictResponse || !request.ResponseJSON || !request.ResponseJSONCompatibility || !reflect.DeepEqual(request.ResponseSchema, outputSchema) {
		t.Fatalf("structured request = %#v", request)
	}
	contract := target.Contract()
	resultField := contract.Fields[len(contract.Fields)-1]
	if resultField.Type != "object" || !reflect.DeepEqual(resultField.Schema, outputSchema) {
		t.Fatalf("result contract = %#v", resultField)
	}
}

func TestNodeRequiresJSONOutputWithoutSchema(t *testing.T) {
	t.Parallel()

	target := NewNode(core.WithID("json_output"))
	target.OutputJSON = true
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{
		Content: "Final result:\n```json\n[1, true, null]\n```",
	}}}}}
	result, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{
		"request": map[string]any{"input": "return JSON"},
	}), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, _ := state.ReadPath(result.State, target.ResultPath.String())
	if !reflect.DeepEqual(value, []any{json.Number("1"), true, nil}) {
		t.Fatalf("result = %#v", value)
	}
	view, _ := conversationcap.Bind(state.NewAccess(result.State), target.ConversationPath)
	if view.FinalAnswer() != `[1,true,null]` {
		t.Fatalf("final answer = %q", view.FinalAnswer())
	}
	if len(model.requests) != 1 || !model.requests[0].ResponseJSON || len(model.requests[0].ResponseSchema) != 0 || model.requests[0].StrictResponse != false {
		t.Fatalf("model request = %#v", model.requests)
	}
	resultField := target.Contract().Fields[len(target.Contract().Fields)-1]
	if resultField.Type != "" || len(resultField.Schema) == 0 {
		t.Fatalf("result contract = %#v", resultField)
	}
}

func TestNodeJSONOutputRequiresExtractableResult(t *testing.T) {
	t.Parallel()

	target := NewNode(core.WithID("missing_json_output"))
	target.OutputJSON = true
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "No result was produced."}}}}}
	_, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{
		"request": map[string]any{"input": "return JSON"},
	}), target)
	if err == nil || core.ClassifyError(err) != core.ErrorInvalidOutput {
		t.Fatalf("execute error = %v, want invalid_output", err)
	}
}

func TestNodeExtractsCompatibleStructuredFinalOutputByDefault(t *testing.T) {
	t.Parallel()

	target := NewNode(core.WithID("compatible_structured"))
	target.OutputSchema = state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "string"},
		},
		"required": []string{"answer"},
	}
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{
		Content: "I used the available context.\n```json\n{\"answer\":\"ready\"}\n```\nThat is the final result.",
	}}}}}
	result, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{
		"request": map[string]any{"input": "return status"},
	}), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, _ := state.ReadPath(result.State, target.ResultPath.String())
	if !reflect.DeepEqual(value, map[string]any{"answer": "ready"}) {
		t.Fatalf("result = %#v", value)
	}
	view, _ := conversationcap.Bind(state.NewAccess(result.State), target.ConversationPath)
	if view.FinalAnswer() != `{"answer":"ready"}` {
		t.Fatalf("final answer = %q", view.FinalAnswer())
	}
}

func TestNodeCanDisableCompatibleStructuredOutputExtraction(t *testing.T) {
	t.Parallel()

	target := NewNode(core.WithID("strict_structured"))
	target.OutputSchema = state.JSONSchema{"type": "object"}
	target.OutputJSONCompatibility = false
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "Result: {}"}}}}}
	_, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{
		"request": map[string]any{"input": "return an object"},
	}), target)
	if err == nil || core.ClassifyError(err) != core.ErrorInvalidOutput {
		t.Fatalf("execute error = %v, want invalid_output", err)
	}
	if len(model.requests) != 1 || !model.requests[0].ResponseJSON || model.requests[0].ResponseJSONCompatibility {
		t.Fatalf("model request = %#v, want strict JSON extraction", model.requests)
	}
}

func TestStructuredNodeContinuesAfterToolCall(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ModelResponse{
		{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{{
			ID:   "lookup-call",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "lookup",
				Arguments: json.RawMessage(`{"query":"status"}`),
			},
		}}}}},
		{Choices: []*llms.ModelChoice{{Content: `{"answer":"ready"}`}}},
	}}
	target := NewNode(core.WithID("structured_tool_user"))
	target.ToolIDs = []string{"lookup"}
	target.OutputSchema = state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "string"},
		},
		"required": []string{"answer"},
	}
	lookup := core.NewTool(&llms.FunctionDefinition{
		Name:       "lookup",
		Parameters: state.JSONSchema{"type": "object"},
	}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		return llms.ToolResult{ToolCallID: call.ID, Content: "ready"}, nil
	})
	ctx := core.WithTools(core.WithModel(context.Background(), model), map[string]core.Tool{"lookup": lookup})
	result, err := core.ExecuteNode(ctx, state.FromShared(map[string]any{
		"request": map[string]any{"input": "check status"},
	}), target)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	value, _ := state.ReadPath(result.State, target.ResultPath.String())
	if !reflect.DeepEqual(value, map[string]any{"answer": "ready"}) {
		t.Fatalf("result = %#v", value)
	}
	if len(model.requests) != 2 || !model.requests[0].StrictResponse || !model.requests[1].StrictResponse {
		t.Fatalf("model requests = %#v", model.requests)
	}
}

func TestStructuredNodeRejectsInvalidTerminalOutput(t *testing.T) {
	t.Parallel()

	target := NewNode(core.WithID("invalid_structured"))
	target.OutputSchema = state.JSONSchema{"type": "object"}
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "not JSON"}}}}}
	result, err := core.ExecuteNode(core.WithModel(context.Background(), model), state.FromShared(map[string]any{
		"request": map[string]any{"input": "return an object"},
	}), target)
	if err == nil || core.ClassifyError(err) != core.ErrorInvalidOutput {
		t.Fatalf("execute error = %v, want invalid_output", err)
	}
	if result.State != nil {
		t.Fatalf("result state = %#v, want nil", result.State)
	}
}

func TestNodeStructuredConfigRoundTripClonesSchema(t *testing.T) {
	t.Parallel()

	schema := state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "string"},
		},
	}
	target := NewNode(core.WithID("round_trip"))
	target.OutputSchema = schema
	target.ResponseName = "agent_answer"
	target.OutputJSON = true
	target.OutputJSONCompatibility = false
	spec := target.GraphNodeSpec()
	schema["properties"].(state.JSONSchema)["answer"].(state.JSONSchema)["type"] = "integer"
	specSchema := spec.Config["output_schema"].(state.JSONSchema)
	if specSchema["properties"].(state.JSONSchema)["answer"].(state.JSONSchema)["type"] != "string" {
		t.Fatalf("graph spec schema retained node alias: %#v", specSchema)
	}

	definition := NodeTypeDefinition()
	if err := registry.NewRegistry().RegisterNodeType(definition); err != nil {
		t.Fatalf("register node type: %v", err)
	}
	built, err := definition.Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: spec.ID, Type: spec.Type, Config: spec.Config},
		State: map[string]registry.ResolvedStateBinding{
			"task":         {Path: target.TaskPath},
			"conversation": {Path: target.ConversationPath},
			"result":       {Path: target.ResultPath},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	builtNode := built.(*Node)
	specSchema["properties"].(state.JSONSchema)["answer"].(state.JSONSchema)["type"] = "boolean"
	if builtNode.ResponseName != "agent_answer" || !builtNode.OutputJSON || builtNode.OutputJSONCompatibility || builtNode.OutputSchema["properties"].(state.JSONSchema)["answer"].(state.JSONSchema)["type"] != "string" {
		t.Fatalf("built node = %#v", builtNode)
	}

	invalidSpec := spec
	invalidSpec.Config = map[string]any{"output_schema": map[string]any{"type": "invalid"}}
	_, err = definition.Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: invalidSpec,
		State: map[string]registry.ResolvedStateBinding{
			"task":         {Path: target.TaskPath},
			"conversation": {Path: target.ConversationPath},
			"result":       {Path: target.ResultPath},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "output schema") {
		t.Fatalf("invalid schema build error = %v", err)
	}

	invalidSpec.Config = map[string]any{"output_schema": "object"}
	_, err = definition.Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: invalidSpec,
		State: map[string]registry.ResolvedStateBinding{
			"task":         {Path: target.TaskPath},
			"conversation": {Path: target.ConversationPath},
			"result":       {Path: target.ResultPath},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must be an object") {
		t.Fatalf("non-object schema build error = %v", err)
	}
}

func TestAgentStatePortAndGovernanceContractRemainStable(t *testing.T) {
	t.Parallel()
	target := NewNode(core.WithID("contract"))
	target.MaxIterations = 3
	target.MaxToolCalls = 4
	target.MaxTokens = 256
	target.MaxCost = 1.25
	target.ToolIDs = []string{"lookup"}
	spec := target.GraphNodeSpec()
	if spec.State["task"].Path != target.TaskPath.String() || spec.State["conversation"].Path != target.ConversationPath.String() || spec.State["result"].Path != target.ResultPath.String() {
		t.Fatalf("state bindings changed = %#v", spec.State)
	}
	for key := range spec.Config {
		if strings.Contains(key, "state") || strings.Contains(key, "path") {
			t.Fatalf("state path leaked into config key %q", key)
		}
	}
	definition := NodeTypeDefinition()
	var conversationPort *dsl.StatePortDefinition
	for index := range definition.StatePorts {
		port := &definition.StatePorts[index]
		if port.Name == "conversation" {
			conversationPort = port
			break
		}
	}
	if conversationPort == nil {
		t.Fatal("conversation State Port is missing")
	}
	wantFields := map[string]dsl.StateAccessMode{
		conversationcap.FieldMessages:       dsl.StateAccessReadWrite,
		conversationcap.FieldFinalAnswer:    dsl.StateAccessReadWrite,
		conversationcap.FieldIterationCount: dsl.StateAccessReadWrite,
		conversationcap.FieldMaxIterations:  dsl.StateAccessReadWrite,
	}
	if len(conversationPort.Contract.Fields) != len(wantFields) {
		t.Fatalf("conversation fields = %#v", conversationPort.Contract.Fields)
	}
	for _, field := range conversationPort.Contract.Fields {
		if want, ok := wantFields[field.Path]; !ok || field.Mode != want {
			t.Fatalf("conversation field = %#v", field)
		}
	}
	if _, ok := spec.Config["max_tool_calls"]; !ok {
		t.Fatal("agent budget config was not serialized")
	}
	built, err := NodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: spec.ID, Type: spec.Type, Config: spec.Config},
		State: map[string]registry.ResolvedStateBinding{
			"task":         {Path: target.TaskPath},
			"conversation": {Path: target.ConversationPath},
			"result":       {Path: target.ResultPath},
		},
	})
	if err != nil {
		t.Fatalf("build agent snapshot: %v", err)
	}
	builtNode := built.(*Node)
	if builtNode.MaxIterations != target.MaxIterations || builtNode.MaxToolCalls != target.MaxToolCalls || builtNode.MaxTokens != target.MaxTokens || builtNode.MaxCost != target.MaxCost {
		t.Fatalf("budget config round trip = %#v, want %#v", builtNode.Config, target.Config)
	}
}

func TestAgentNodePreservesToolPermissionAndApprovalGovernance(t *testing.T) {
	t.Parallel()
	tool := core.NewTool(&llms.FunctionDefinition{Name: "protected"}, func(_ context.Context, _ llms.ToolCall) (llms.ToolResult, error) {
		t.Fatal("protected tool executed despite governance failure")
		return llms.ToolResult{}, nil
	})
	tool.Permissions = []string{"filesystem.write"}
	tool.Approval = core.ToolApprovalRequired
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing permission", ctx: context.Background()},
		{name: "missing approval", ctx: core.WithToolPermissions(context.Background(), "filesystem.write")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model := &scriptedModel{
				responses: []*llms.ModelResponse{
					{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{toolCall("protected", `{}`)}}}},
				},
			}
			target := NewNode(core.WithID("governed"))
			target.ToolIDs = []string{"protected"}
			ctx := core.WithTools(core.WithModel(testCase.ctx, model), map[string]core.Tool{"protected": tool})
			_, err := core.ExecuteNode(ctx, state.FromShared(map[string]any{"request": map[string]any{"input": "run protected"}}), target)
			if err == nil || core.ClassifyError(err) != core.ErrorPermissionDenied {
				t.Fatalf("ExecuteNode() error = %v, want permission_denied", err)
			}
		})
	}
}
func TestToolUsesExplicitMetadataAndRunsAgent(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "research result"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:        "research_agent",
		Description: "Delegate research to a specialist.",
		Agent: Config{
			SystemPrompt: "Return a concise research result.",
		},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}

	if tool.Function.Name != "research_agent" || tool.Function.Description != "Delegate research to a specialist." {
		t.Fatalf("tool function = %#v", tool.Function)
	}
	ctx := core.WithTools(core.WithModel(context.Background(), model), map[string]core.Tool{
		"unconfigured": core.NewTool(&llms.FunctionDefinition{Name: "unconfigured"}, nil),
	})
	result, err := tool.Handler(ctx, toolCall("research_agent", `{"task":"research this"}`))
	if err != nil {
		t.Fatalf("execute agent tool: %v", err)
	}
	if result.Content != "research result" || result.Value != "research result" {
		t.Fatalf("result = %#v, want research result", result)
	}
	if len(model.requests) != 1 || len(model.requests[0].Tools) != 0 {
		t.Fatalf("injected tools = %#v, want none", model.requests[0].Tools)
	}
}

func TestToolWritesStructuredOutput(t *testing.T) {
	t.Parallel()

	outputSchema := state.JSONSchema{
		"type": "object",
		"properties": state.JSONSchema{
			"answer": state.JSONSchema{"type": "integer"},
		},
		"required":             []string{"answer"},
		"additionalProperties": false,
	}
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "Result: {\"answer\":7}"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:         "research_agent",
		Description:  "Delegate research to a specialist.",
		OutputSchema: outputSchema,
		Agent:        Config{SystemPrompt: "Return a JSON result."},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	if !reflect.DeepEqual(tool.Function.OutputSchema, outputSchema) {
		t.Fatalf("tool output schema = %#v, want %#v", tool.Function.OutputSchema, outputSchema)
	}
	result, err := core.ExecuteTool(core.WithModel(context.Background(), model), tool, toolCall("research_agent", `{"task":"research this"}`))
	if err != nil {
		t.Fatalf("execute agent tool: %v", err)
	}
	if result.Content != `{"answer":7}` || !reflect.DeepEqual(result.Value, map[string]any{"answer": json.Number("7")}) {
		t.Fatalf("result = %#v, want normalized structured result", result)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	if request.ResponseName != defaultToolResponseName || !request.StrictResponse || !request.ResponseJSON || !request.ResponseJSONCompatibility || !reflect.DeepEqual(request.ResponseSchema, outputSchema) {
		t.Fatalf("structured request = %#v", request)
	}
}

func TestToolCanRequireJSONWithoutOutputSchema(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "```json\n[1,2,3]\n```"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:        "json_agent",
		Description: "Return a JSON result.",
		OutputJSON:  true,
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	result, err := core.ExecuteTool(core.WithModel(context.Background(), model), tool, toolCall("json_agent", `{"task":"return JSON"}`))
	if err != nil {
		t.Fatalf("execute agent tool: %v", err)
	}
	if result.Content != `[1,2,3]` || !reflect.DeepEqual(result.Value, []any{json.Number("1"), json.Number("2"), json.Number("3")}) {
		t.Fatalf("result = %#v, want normalized JSON result", result)
	}
	if len(model.requests) != 1 || !model.requests[0].ResponseJSON || len(model.requests[0].ResponseSchema) != 0 {
		t.Fatalf("structured request = %#v", model.requests)
	}
}

func TestToolCanRequireStrictStructuredOutput(t *testing.T) {
	t.Parallel()

	compatibility := false
	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "Result: {\"answer\":7}"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:                    "strict_agent",
		Description:             "Return a structured result.",
		OutputSchema:            state.JSONSchema{"type": "object"},
		OutputJSONCompatibility: &compatibility,
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	_, err = core.ExecuteTool(core.WithModel(context.Background(), model), tool, toolCall("strict_agent", `{"task":"return JSON"}`))
	if err == nil || core.ClassifyError(err) != core.ErrorInvalidOutput {
		t.Fatalf("execute error = %v, want invalid_output", err)
	}
	if len(model.requests) != 1 || model.requests[0].ResponseJSONCompatibility {
		t.Fatalf("structured request = %#v", model.requests)
	}
}

func TestToolRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config ToolConfig
	}{
		{name: "missing name", config: ToolConfig{Description: "Delegate work."}},
		{name: "missing description", config: ToolConfig{Name: "worker"}},
		{name: "empty tool id", config: ToolConfig{Name: "worker", Description: "Delegate work.", Agent: Config{ToolIDs: []string{" "}}}},
		{name: "duplicate tool id", config: ToolConfig{Name: "worker", Description: "Delegate work.", Agent: Config{ToolIDs: []string{"read", "READ"}}}},
		{name: "self reference", config: ToolConfig{Name: "worker", Description: "Delegate work.", Agent: Config{ToolIDs: []string{"worker"}}}},
		{name: "invalid output schema", config: ToolConfig{Name: "worker", Description: "Delegate work.", OutputSchema: state.JSONSchema{"type": "unsupported"}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewTool(testCase.config); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestToolRestrictsInternalTools(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ModelResponse{
		{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{{
			ID:   "call",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "other",
				Arguments: json.RawMessage(`{}`),
			},
		}}}}},
		{Choices: []*llms.ModelChoice{{Content: "finished"}}},
	}}
	otherCalled := false
	availableTools := map[string]core.Tool{
		"allowed": core.NewTool(&llms.FunctionDefinition{Name: "allowed"}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			return llms.ToolResult{ToolCallID: call.ID, Content: "allowed"}, nil
		}),
		"other": core.NewTool(&llms.FunctionDefinition{Name: "other"}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			otherCalled = true
			return llms.ToolResult{ToolCallID: call.ID, Content: "other"}, nil
		}),
	}
	tool, err := NewTool(ToolConfig{
		Name:        "worker",
		Description: "Delegate work.",
		Agent:       Config{ToolIDs: []string{"allowed"}},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	ctx := core.WithTools(core.WithModel(context.Background(), model), availableTools)
	result, err := tool.Handler(ctx, toolCall("worker", `{"task":"do work"}`))
	if err != nil {
		t.Fatalf("execute agent tool: %v", err)
	}
	if result.Content != "finished" || result.Value != "finished" {
		t.Fatalf("result = %#v, want finished", result)
	}
	if otherCalled {
		t.Fatal("agent tool executed a tool outside its allowlist")
	}
	if len(model.requests) == 0 || len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Function.Name != "allowed" {
		t.Fatalf("injected tools = %#v", model.requests[0].Tools)
	}
}

func TestToolRequiresConfiguredToolsAtRuntime(t *testing.T) {
	t.Parallel()

	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "unused"}}}}}
	tool, err := NewTool(ToolConfig{
		Name:        "worker",
		Description: "Delegate work.",
		Agent:       Config{ToolIDs: []string{"missing"}},
	})
	if err != nil {
		t.Fatalf("new agent tool: %v", err)
	}
	_, err = tool.Handler(core.WithModel(context.Background(), model), toolCall("worker", `{"task":"do work"}`))
	if err == nil || !strings.Contains(err.Error(), `configured tool "missing" is not available`) {
		t.Fatalf("execute error = %v", err)
	}
	if len(model.calls) != 0 {
		t.Fatalf("model calls = %d, want 0", len(model.calls))
	}
}

func TestToolReusesSingleConcurrencySlotForInternalCalls(t *testing.T) {
	model := &scriptedModel{responses: []*llms.ModelResponse{
		{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{
			{ID: "first", Type: "function", FunctionCall: &llms.FunctionCall{Name: "blocked", Arguments: json.RawMessage(`{}`)}},
			{ID: "second", Type: "function", FunctionCall: &llms.FunctionCall{Name: "blocked", Arguments: json.RawMessage(`{}`)}},
		}}}},
		{Choices: []*llms.ModelChoice{{Content: "finished"}}},
	}}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maximumActive atomic.Int32
	blocked := core.NewTool(&llms.FunctionDefinition{Name: "blocked"}, func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		current := active.Add(1)
		for {
			maximum := maximumActive.Load()
			if current <= maximum || maximumActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return llms.ToolResult{ToolCallID: call.ID, Content: "ok"}, nil
	})
	worker, err := NewTool(ToolConfig{
		Name:        "worker",
		Description: "Delegate work.",
		Agent: Config{
			ToolIDs:  []string{"blocked"},
			Parallel: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithTools(core.WithModel(context.Background(), model), map[string]core.Tool{
		"worker":  worker,
		"blocked": blocked,
	})
	ctx = core.WithToolConcurrencyLimiter(ctx, core.NewConcurrencyLimiter(1), nil)
	done := make(chan llms.MessageContent, 1)
	go func() {
		done <- basenode.ExecuteToolCallMessage(core.NewContext(ctx), llms.ToolCall{
			ID:   "outer",
			Type: "function",
			FunctionCall: &llms.FunctionCall{
				Name:      "worker",
				Arguments: json.RawMessage(`{"task":"do work"}`),
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first internal tool call did not start")
	}
	select {
	case <-started:
		t.Fatal("second internal tool call bypassed the inherited concurrency slot")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second internal tool call did not start after capacity was released")
	}
	select {
	case message := <-done:
		if len(message.Parts) != 1 {
			t.Fatalf("tool response = %#v", message)
		}
		response, ok := message.Parts[0].(llms.ToolResult)
		if !ok || response.Content != "finished" {
			t.Fatalf("tool response = %#v, want finished", message.Parts[0])
		}
	case <-time.After(time.Second):
		t.Fatal("nested agent tool execution deadlocked")
	}
	if maximumActive.Load() != 1 {
		t.Fatalf("maximum active internal tools = %d, want 1", maximumActive.Load())
	}
}

type scriptedModel struct {
	mu        sync.Mutex
	responses []*llms.ModelResponse
	calls     [][]llms.MessageContent
	requests  []llms.ModelRequest
}

func (model *scriptedModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.responses) == 0 {
		return nil, errors.New("scripted model exhausted")
	}
	request.Messages = cloneMessages(request.Messages)
	request.Tools = append([]llms.ToolDefinition(nil), request.Tools...)
	model.calls = append(model.calls, cloneMessages(request.Messages))
	model.requests = append(model.requests, request)
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response, nil
}

func toolCall(name, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   "test-call",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      name,
			Arguments: json.RawMessage(arguments),
		},
	}
}

func cloneMessages(messages []llms.MessageContent) []llms.MessageContent {
	cloned := make([]llms.MessageContent, len(messages))
	for index, message := range messages {
		cloned[index] = llms.MessageContent{Role: message.Role, Parts: append([]llms.ContentPart(nil), message.Parts...)}
	}
	return cloned
}
