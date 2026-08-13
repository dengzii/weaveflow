package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

type agentToolInput struct {
	Task string `json:"task"`
}

// ToolConfig defines the external tool contract and its internal agent execution.
type ToolConfig struct {
	Name        string
	Description string
	Agent       Config
}

// NewTool creates an isolated agent exposed through the runtime tool contract.
func NewTool(config ToolConfig) (core.Tool, error) {
	normalized, err := normalizeToolConfig(config)
	if err != nil {
		return core.Tool{}, err
	}
	runner := agentRuntime{
		config: normalized.Agent,
		identity: executionIdentity{
			ToolName: normalized.Name,
		},
		strictToolIDs: true,
	}
	tool := core.Tool{
		Function: &llms.FunctionDefinition{
			Name:        normalized.Name,
			Description: normalized.Description,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "The task or question for the agent to handle.",
					},
				},
				"required":             []string{"task"},
				"additionalProperties": false,
			},
			OutputSchema: state.JSONSchema{"type": "string"},
		},
		ExecutionMode: core.ToolExecutionComposite,
		Handler: func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			var input agentToolInput
			if err := core.DecodeToolArguments(call, &input); err != nil {
				return llms.ToolResult{}, err
			}
			task := strings.TrimSpace(input.Task)
			if task == "" {
				return llms.ToolResult{}, errors.New("agent tool: task is required")
			}

			coreCtx := core.NewContext(ctx)
			if coreCtx.Model(normalized.Agent.ModelID) == nil {
				return llms.ToolResult{}, fmt.Errorf("agent tool: model %q not available", effectiveModelID(normalized.Agent.ModelID))
			}

			access := state.NewEditingAccess(state.NewState())
			conversation, err := conversationcap.Bind(access, state.Shared("agent_tool", "conversation"))
			if err != nil {
				return llms.ToolResult{}, err
			}
			if err := conversation.SetMaxIterations(runner.effectiveMaxIterations(conversation)); err != nil {
				return llms.ToolResult{}, err
			}
			if err := runner.seedConversation(conversation, task); err != nil {
				return llms.ToolResult{}, err
			}
			if err := runner.runLoop(coreCtx, conversation); err != nil {
				return llms.ToolResult{}, err
			}
			answer := conversation.FinalAnswer()
			return llms.ToolResult{Content: answer, Value: answer}, nil
		},
	}
	return tool, nil
}

func normalizeToolConfig(config ToolConfig) (ToolConfig, error) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return ToolConfig{}, errors.New("agent tool: name is required")
	}
	config.Description = strings.TrimSpace(config.Description)
	if config.Description == "" {
		return ToolConfig{}, errors.New("agent tool: description is required")
	}
	if config.Agent.MaxIterations < 0 {
		return ToolConfig{}, errors.New("agent tool: max iterations cannot be negative")
	}
	if config.Agent.PromptMaxChars < 0 {
		return ToolConfig{}, errors.New("agent tool: prompt max chars cannot be negative")
	}

	toolIDs := make([]string, 0, len(config.Agent.ToolIDs))
	seen := make(map[string]struct{}, len(config.Agent.ToolIDs))
	for _, configuredID := range config.Agent.ToolIDs {
		toolID := strings.TrimSpace(configuredID)
		if toolID == "" {
			return ToolConfig{}, errors.New("agent tool: tool IDs cannot contain an empty value")
		}
		key := strings.ToLower(toolID)
		if strings.EqualFold(toolID, config.Name) {
			return ToolConfig{}, fmt.Errorf("agent tool %q cannot include itself in tool IDs", config.Name)
		}
		if _, ok := seen[key]; ok {
			return ToolConfig{}, fmt.Errorf("agent tool %q contains duplicate tool ID %q", config.Name, toolID)
		}
		seen[key] = struct{}{}
		toolIDs = append(toolIDs, toolID)
	}
	config.Agent.ToolIDs = toolIDs
	return config, nil
}
