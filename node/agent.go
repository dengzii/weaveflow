package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms/parts"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
	"github.com/dengzii/weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

const defaultAgentPromptMaxChars = 1000000

type AgentNode struct {
	Base
	ToolIDs         []string
	SystemPrompt    string
	InputPath       state.Path
	OutputPath      state.Path
	MaxIterations   int
	PromptMaxChars  int
	Parallel        bool
	ToolName        string
	ToolDescription string
}

func NewAgentNode(options ...NodeOption) *AgentNode {
	node := &AgentNode{
		Base: NewBase(Spec{
			Name:        NodeTypeAgent,
			Description: "Run a self-contained ReAct loop with configurable prompt and tools.",
			Scope:       DefaultScope,
			AccessorUses: []AccessorUse{
				Use(accessors.ConversationID.Name()),
				UseRoot(accessors.RequestID.Name()),
				UseRoot(accessors.FinalID.Name()),
			},
		}),
		Parallel: true,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (a *AgentNode) Execute(ctx core.Context, access *state.Access) error {
	if ctx.Model() == nil {
		return errors.New("agent node: model service not available")
	}

	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		return err
	}
	if a.MaxIterations > 0 && conversation.MaxIterations() < a.MaxIterations {
		if err := conversation.SetMaxIterations(a.MaxIterations); err != nil {
			return err
		}
	}

	if err := a.seedConversation(conversation, a.readTaskFromAccess(access)); err != nil {
		return err
	}
	if err := a.runLoop(ctx, conversation); err != nil {
		return err
	}
	return a.writeAnswer(access, conversation.FinalAnswer())
}

func (a *AgentNode) runLoop(ctx core.Context, conversation accessors.Conversation) error {
	model := ctx.Model()
	nodeTools := ctx.FilterTools(a.ToolIDs)

	var toolSets []llms.Tool
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.NewTool())
	}

	maxIter := a.effectiveMaxIterations(conversation)
	promptCap := a.effectivePromptMaxChars()

	for {
		if conversation.IterationCount() >= maxIter {
			message := "Maximum agent iterations reached. The agent stopped before producing a final answer."
			if err := conversation.SetMessages(append(conversation.Messages(), llms.TextParts(llms.ChatMessageTypeAI, message))); err != nil {
				return err
			}
			if err := conversation.SetFinalAnswer(message); err != nil {
				return err
			}
			a.publishLoopStopped(ctx, conversation.IterationCount(), "max_iterations")
			return nil
		}

		messages := conversation.Messages()
		promptMessages := trimLLMPromptMessages(messages, promptCap)

		iteration := conversation.IterationCount()
		if payload, err := buildAgentPromptArtifact(a, promptMessages, toolSets, iteration, maxIter); err == nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.prompt", payload)
		}

		resp, err := model.GenerateContent(ctx, promptMessages,
			llms.WithTools(toolSets),
			llms.WithThinkingMode(llms.ThinkingModeHigh),
		)
		if err != nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.error", map[string]any{
				"agent_node_id":   a.ID(),
				"agent_iteration": iteration,
				"error":           err.Error(),
			})
			return err
		}
		if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
			err := errors.New("agent node: llm returned no choices")
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.error", map[string]any{
				"agent_node_id":   a.ID(),
				"agent_iteration": iteration,
				"error":           err.Error(),
			})
			return err
		}
		if payload := buildAgentResponseArtifact(a, resp, iteration); len(payload.Choices) > 0 {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.response", payload)
		}

		choice := resp.Choices[0]
		aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
		if strings.TrimSpace(choice.ReasoningContent) != "" {
			aiMessage.Parts = append(aiMessage.Parts, parts.NewReasoningPart(choice.ReasoningContent))
		}
		if strings.TrimSpace(choice.Content) != "" {
			aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
		}
		for _, toolCall := range choice.ToolCalls {
			if toolCall.Type == "" {
				_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, map[string]any{
					"message":         "agent node received a tool call with no type",
					"agent_node_id":   a.ID(),
					"agent_iteration": iteration,
				})
				continue
			}
			aiMessage.Parts = append(aiMessage.Parts, toolCall)
		}

		if err := conversation.SetMessages(append(messages, aiMessage)); err != nil {
			return err
		}
		if err := conversation.IncrementIteration(); err != nil {
			return err
		}

		if len(choice.ToolCalls) == 0 {
			if err := conversation.SetFinalAnswer(extractText(aiMessage)); err != nil {
				return err
			}
			a.publishLoopStopped(ctx, conversation.IterationCount(), "final_answer")
			return nil
		}

		if err := a.executeToolCalls(ctx, conversation, choice.ToolCalls); err != nil {
			return err
		}
	}
}

func (a *AgentNode) executeToolCalls(ctx core.Context, conversation accessors.Conversation, toolCalls []llms.ToolCall) error {
	if len(toolCalls) == 0 {
		return nil
	}

	toolMessages := make([]llms.MessageContent, len(toolCalls))
	if a.Parallel && len(toolCalls) > 1 {
		var wg sync.WaitGroup
		wg.Add(len(toolCalls))
		for index, toolCall := range toolCalls {
			go func(index int, toolCall llms.ToolCall) {
				defer wg.Done()
				toolMessages[index] = executeToolCallMessage(ctx, toolCall)
			}(index, toolCall)
		}
		wg.Wait()
	} else {
		for index, toolCall := range toolCalls {
			toolMessages[index] = executeToolCallMessage(ctx, toolCall)
		}
	}

	return conversation.SetMessages(append(conversation.Messages(), toolMessages...))
}

func (a *AgentNode) publishLoopStopped(ctx context.Context, iteration int, reason string) {
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"agent_node_id":   a.ID(),
		"agent_iteration": iteration,
		"reason":          reason,
		"event":           "agent.loop_stopped",
	})
}

func (a *AgentNode) seedConversation(conversation accessors.Conversation, task string) error {
	messages := conversation.Messages()
	hasSystem := false
	hasAnyHuman := false
	for _, message := range messages {
		switch message.Role {
		case llms.ChatMessageTypeSystem:
			hasSystem = true
		case llms.ChatMessageTypeHuman:
			hasAnyHuman = true
		}
	}

	updated := false
	if strings.TrimSpace(a.SystemPrompt) != "" && !hasSystem {
		messages = append([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeSystem, a.SystemPrompt)}, messages...)
		updated = true
	}
	if task != "" && !hasAnyHuman {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, task))
		updated = true
	}
	if updated {
		return conversation.SetMessages(messages)
	}
	return nil
}

func (a *AgentNode) readTaskFromAccess(access *state.Access) string {
	if !a.InputPath.Empty() {
		value, ok := access.ReadAny(a.InputPath)
		if !ok {
			return ""
		}
		return stringifyStateValue(value)
	}
	request, err := state.UseAccessor(access, accessors.RequestID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(request.Input())
}

func (a *AgentNode) writeAnswer(access *state.Access, answer string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil
	}
	if !a.OutputPath.Empty() {
		return access.SetAny(a.OutputPath, answer)
	}
	final, err := state.UseAccessor(access, accessors.FinalID)
	if err != nil {
		return err
	}
	return final.SetAnswer(answer)
}

func (a *AgentNode) effectiveMaxIterations(conversation accessors.Conversation) int {
	if a.MaxIterations > 0 {
		return a.MaxIterations
	}
	if conversation != nil {
		if max := conversation.MaxIterations(); max > 0 {
			return max
		}
	}
	return accessors.DefaultMaxIterations
}

func (a *AgentNode) effectivePromptMaxChars() int {
	if a == nil || a.PromptMaxChars <= 0 {
		return defaultAgentPromptMaxChars
	}
	return a.PromptMaxChars
}

func (a *AgentNode) Contract(registry *state.Registry) (state.Contract, error) {
	base, err := contractFromAccessorUses(registry, a.ID(), a.Scope(), a.Base.AccessorUses())
	if err != nil {
		return state.Contract{}, err
	}
	fields := append([]state.FieldAccess(nil), base.Fields...)
	if !a.InputPath.Empty() {
		fields = append(fields, state.FieldAccess{Path: a.InputPath, Mode: state.AccessRead, Type: "any"})
	}
	if !a.OutputPath.Empty() {
		fields = append(fields, state.FieldAccess{Path: a.OutputPath, Mode: state.AccessWrite, Type: "string"})
	}
	contract := state.NewContract(fields...)
	contract.WildcardRead = base.WildcardRead
	contract.WildcardWrite = base.WildcardWrite
	return contract, nil
}

func (a *AgentNode) AsTool() tools.Tool {
	name := strings.TrimSpace(a.ToolName)
	if name == "" {
		name = "agent"
	}
	description := strings.TrimSpace(a.ToolDescription)
	if description == "" {
		description = a.Description()
	}

	function := &llms.FunctionDefinition{
		Name:        name,
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task or question for the agent to handle.",
				},
			},
			"required": []string{"task"},
		},
	}

	return tools.Tool{
		Function: function,
		Handler: func(ctx context.Context, input string) (string, error) {
			task, err := decodeAgentToolInput(input)
			if err != nil {
				return "", err
			}
			if task == "" {
				return "", errors.New("agent tool: empty task")
			}

			coreCtx := core.NewContext(ctx)
			if coreCtx.Model() == nil {
				return "", errors.New("agent tool: model service not available")
			}

			registry, err := NewDefaultRegistry()
			if err != nil {
				return "", err
			}
			access := state.NewEditingAccess(registry, state.NewState()).WithScope(a.Scope())
			conversation, err := state.UseAccessor(access, accessors.ConversationID)
			if err != nil {
				return "", err
			}
			if err := conversation.SetMaxIterations(a.effectiveMaxIterations(conversation)); err != nil {
				return "", err
			}
			if err := a.seedConversation(conversation, task); err != nil {
				return "", err
			}

			if err := a.runLoop(coreCtx, conversation); err != nil {
				return "", err
			}
			return conversation.FinalAnswer(), nil
		},
	}
}

func decodeAgentToolInput(input string) (string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw, nil
	}
	if task, ok := payload["task"].(string); ok {
		return strings.TrimSpace(task), nil
	}
	if task, ok := payload["input"].(string); ok {
		return strings.TrimSpace(task), nil
	}
	return raw, nil
}

func stringifyStateValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

type agentPromptArtifact struct {
	AgentNodeID   string               `json:"agent_node_id,omitempty"`
	StateScope    string               `json:"state_scope,omitempty"`
	Iteration     int                  `json:"agent_iteration"`
	MaxIterations int                  `json:"max_iterations,omitempty"`
	Messages      []state.StateMessage `json:"messages,omitempty"`
	Tools         []llmToolArtifact    `json:"tools,omitempty"`
}

type agentResponseArtifact struct {
	AgentNodeID string                      `json:"agent_node_id,omitempty"`
	Iteration   int                         `json:"agent_iteration"`
	Choices     []llmResponseArtifactChoice `json:"choices,omitempty"`
}

func buildAgentPromptArtifact(a *AgentNode, messages []llms.MessageContent, tools []llms.Tool, iteration, maxIterations int) (agentPromptArtifact, error) {
	serialized, err := state.SerializeMessages(messages)
	if err != nil {
		return agentPromptArtifact{}, err
	}
	payload := agentPromptArtifact{
		AgentNodeID:   a.ID(),
		StateScope:    a.Scope(),
		Iteration:     iteration,
		MaxIterations: maxIterations,
		Messages:      serialized,
	}
	if len(tools) > 0 {
		payload.Tools = make([]llmToolArtifact, 0, len(tools))
		for _, tool := range tools {
			payload.Tools = append(payload.Tools, llmToolArtifact{
				Type:     tool.Type,
				Function: tool.Function,
			})
		}
	}
	return payload, nil
}

func buildAgentResponseArtifact(a *AgentNode, resp *llms.ContentResponse, iteration int) agentResponseArtifact {
	if resp == nil {
		return agentResponseArtifact{}
	}
	inner := buildLLMResponseArtifact(resp)
	return agentResponseArtifact{
		AgentNodeID: a.ID(),
		Iteration:   iteration,
		Choices:     inner.Choices,
	}
}
