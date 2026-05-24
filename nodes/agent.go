package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"weaveflow/core"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const defaultAgentPromptMaxChars = 20000

// AgentNode runs a self-contained ReAct loop: each Execute call iterates
// LLM inference and tool execution inside one node, returning only when the
// model emits a final text answer (no tool_calls) or the iteration cap is hit.
//
// It can also be exposed via AsTool() so an upper-level LLM can invoke the
// whole agent as a single tool (agent-as-tool). The tool handler runs the
// loop against a fresh ephemeral state, completely isolated from any caller.
type AgentNode struct {
	NodeInfo
	StateScope      string
	ToolIDs         []string
	SystemPrompt    string
	InputPath       string
	OutputPath      string
	MaxIterations   int
	PromptMaxChars  int
	Parallel        bool
	ToolName        string
	ToolDescription string
}

func NewAgentNode() *AgentNode {
	id := uuid.New()
	return &AgentNode{
		NodeInfo: NodeInfo{
			NodeID:          "Agent_" + id.String(),
			NodeName:        "Agent",
			NodeDescription: "Run a self-contained ReAct loop with configurable prompt and tools.",
		},
		Parallel: true,
	}
}

func (a *AgentNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return a.execute(ctx, state)
	})
}

func (a *AgentNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	svc := core.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("agent node: model service not available")
	}

	conversation := state.Conversation(a.StateScope)
	if a.MaxIterations > 0 && conversation.MaxIterations() < a.MaxIterations {
		conversation.SetMaxIterations(a.MaxIterations)
	}

	a.seedConversation(conversation, a.readTaskFromState(state))

	if err := a.runLoop(ctx, state, conversation, svc); err != nil {
		return state, err
	}

	if a.OutputPath != "" {
		if answer := conversation.FinalAnswer(); answer != "" {
			wfstate.SetContractPathValue(state, a.OutputPath, answer)
		}
	}

	return state, nil
}

// runLoop is the shared ReAct core used both by Execute (node mode) and
// AsTool's handler (tool mode). It mutates conversation in place and writes
// the final answer via SetFinalAnswer when the loop terminates.
func (a *AgentNode) runLoop(ctx context.Context, state wfstate.State, conversation wfstate.ConversationFacet, svc *core.Services) error {
	model := svc.Model
	nodeTools := svc.FilterTools(a.ToolIDs)

	var toolSets []llms.Tool
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.NewTool())
	}

	maxIter := a.effectiveMaxIterations(conversation)
	promptCap := a.effectivePromptMaxChars()

	for {
		if conversation.IterationCount() >= maxIter {
			message := "Maximum agent iterations reached. The agent stopped before producing a final answer."
			conversation.UpdateMessage(append(conversation.Messages(), llms.TextParts(llms.ChatMessageTypeAI, message)))
			conversation.SetFinalAnswer(message)
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
				"agent_node_id":   a.NodeID,
				"agent_iteration": iteration,
				"error":           err.Error(),
			})
			return err
		}
		if resp == nil || len(resp.Choices) == 0 {
			err := errors.New("agent node: llm returned no choices")
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.error", map[string]any{
				"agent_node_id":   a.NodeID,
				"agent_iteration": iteration,
				"error":           err.Error(),
			})
			return err
		}
		if payload := buildAgentResponseArtifact(a, resp, iteration); len(payload.Choices) > 0 {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.llm.response", payload)
		}

		choice := resp.Choices[0]
		_ = RecordChoiceUsage(ctx, state, Record{
			NodeID:     a.ID(),
			Model:      modelLabel(model),
			StateScope: a.StateScope,
		}, choice)

		aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
		if strings.TrimSpace(choice.Content) != "" {
			aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
		}
		for _, toolCall := range choice.ToolCalls {
			if toolCall.Type == "" {
				_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, map[string]any{
					"message":         "agent node received a tool call with no type",
					"agent_node_id":   a.NodeID,
					"agent_iteration": iteration,
				})
				continue
			}
			aiMessage.Parts = append(aiMessage.Parts, toolCall)
		}

		conversation.UpdateMessage(append(messages, aiMessage))
		conversation.IncrementIteration()

		if len(choice.ToolCalls) == 0 {
			conversation.SetFinalAnswer(extractText(aiMessage))
			a.publishLoopStopped(ctx, conversation.IterationCount(), "final_answer")
			return nil
		}

		if err := a.executeToolCalls(ctx, conversation, nodeTools, choice.ToolCalls, iteration); err != nil {
			return err
		}
	}
}

func (a *AgentNode) executeToolCalls(ctx context.Context, conversation wfstate.ConversationFacet, available map[string]tools.Tool, toolCalls []llms.ToolCall, iteration int) error {
	if len(toolCalls) == 0 {
		return nil
	}

	a.publishAgentToolCallsStart(ctx, toolCalls, iteration)

	toolMessages := make([]llms.MessageContent, len(toolCalls))
	if a.Parallel && len(toolCalls) > 1 {
		var wg sync.WaitGroup
		wg.Add(len(toolCalls))
		for index, toolCall := range toolCalls {
			go func(index int, toolCall llms.ToolCall) {
				defer wg.Done()
				toolMessages[index] = executeToolCallMessage(ctx, available, toolCall)
			}(index, toolCall)
		}
		wg.Wait()
	} else {
		for index, toolCall := range toolCalls {
			toolMessages[index] = executeToolCallMessage(ctx, available, toolCall)
		}
	}

	conversation.UpdateMessage(append(conversation.Messages(), toolMessages...))
	return nil
}

func (a *AgentNode) publishAgentToolCallsStart(ctx context.Context, toolCalls []llms.ToolCall, iteration int) {
	if len(toolCalls) == 0 {
		return
	}
	items := make([]toolCallEventItem, 0, len(toolCalls))
	artifactItems := make([]map[string]any, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		name := toolCallName(toolCall)
		arguments := toolCallArguments(toolCall)
		items = append(items, toolCallEventItem{
			ToolCallID: toolCall.ID,
			Name:       name,
			Arguments:  arguments,
		})
		artifactItems = append(artifactItems, map[string]any{
			"tool_call_id": toolCall.ID,
			"name":         name,
			"arguments":    arguments,
			"input":        decodeToolInput(arguments),
		})
	}

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolCalled, map[string]any{
		"agent_node_id":   a.NodeID,
		"agent_iteration": iteration,
		"tools":           items,
		"count":           len(items),
		"parallel":        a.Parallel && len(toolCalls) > 1,
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "agent.tool.inputs", map[string]any{
		"agent_node_id":   a.NodeID,
		"agent_iteration": iteration,
		"tools":           artifactItems,
		"count":           len(artifactItems),
		"parallel":        a.Parallel && len(toolCalls) > 1,
	})
}

func (a *AgentNode) publishLoopStopped(ctx context.Context, iteration int, reason string) {
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"agent_node_id":   a.NodeID,
		"agent_iteration": iteration,
		"reason":          reason,
		"event":           "agent.loop_stopped",
	})
}

func (a *AgentNode) seedConversation(conversation wfstate.ConversationFacet, task string) {
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
		conversation.UpdateMessage(messages)
	}
}

func (a *AgentNode) readTaskFromState(state wfstate.State) string {
	if a.InputPath == "" {
		return ""
	}
	value, ok := wfstate.ResolveContractPathValue(state, a.InputPath)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func (a *AgentNode) effectiveMaxIterations(conversation wfstate.ConversationFacet) int {
	if a.MaxIterations > 0 {
		return a.MaxIterations
	}
	if max := conversation.MaxIterations(); max > 0 {
		return max
	}
	return wfstate.DefaultMaxIterations
}

func (a *AgentNode) effectivePromptMaxChars() int {
	if a == nil || a.PromptMaxChars <= 0 {
		return defaultAgentPromptMaxChars
	}
	return a.PromptMaxChars
}

func (a *AgentNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          a.ID(),
		Name:        a.Name(),
		Type:        "agent",
		Description: a.Description(),
		Config: map[string]any{
			"tool_ids":         append([]string(nil), a.ToolIDs...),
			"state_scope":      a.StateScope,
			"system_prompt":    a.SystemPrompt,
			"input_path":       a.InputPath,
			"output_path":      a.OutputPath,
			"max_iterations":   a.MaxIterations,
			"prompt_max_chars": a.effectivePromptMaxChars(),
			"parallel":         a.Parallel,
			"tool_name":        a.ToolName,
			"tool_description": a.ToolDescription,
		},
	}
}

// AsTool wraps the agent into a tools.Tool so an upper-level LLM can invoke
// the whole loop as a single tool call. The handler builds a fresh ephemeral
// State so no parent conversation state leaks in or out.
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

			svc := core.ServicesFrom(ctx)
			if svc == nil || svc.Model == nil {
				return "", errors.New("agent tool: model service not available")
			}

			state := wfstate.State{}
			conversation := state.Conversation("")
			conversation.SetMaxIterations(a.effectiveMaxIterations(conversation))
			a.seedConversation(conversation, task)

			if err := a.runLoop(ctx, state, conversation, svc); err != nil {
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
		return task, nil
	}
	if task, ok := payload["input"].(string); ok {
		return task, nil
	}
	return raw, nil
}

type agentPromptArtifact struct {
	AgentNodeID   string                 `json:"agent_node_id,omitempty"`
	StateScope    string                 `json:"state_scope,omitempty"`
	Iteration     int                    `json:"agent_iteration"`
	MaxIterations int                    `json:"max_iterations,omitempty"`
	Messages      []wfstate.StateMessage `json:"messages,omitempty"`
	Tools         []llmToolArtifact      `json:"tools,omitempty"`
}

type agentResponseArtifact struct {
	AgentNodeID string                      `json:"agent_node_id,omitempty"`
	Iteration   int                         `json:"agent_iteration"`
	Choices     []llmResponseArtifactChoice `json:"choices,omitempty"`
}

func buildAgentPromptArtifact(a *AgentNode, messages []llms.MessageContent, tools []llms.Tool, iteration, maxIterations int) (agentPromptArtifact, error) {
	serialized, err := wfstate.SerializeMessages(messages)
	if err != nil {
		return agentPromptArtifact{}, err
	}
	payload := agentPromptArtifact{
		AgentNodeID:   a.NodeID,
		StateScope:    a.StateScope,
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
		AgentNodeID: a.NodeID,
		Iteration:   iteration,
		Choices:     inner.Choices,
	}
}
