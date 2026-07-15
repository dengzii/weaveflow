package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/llms/parts"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

const defaultAgentPromptMaxChars = 1000000

type AgentNode struct {
	Base
	ModelID          string
	ToolIDs          []string
	SystemPrompt     string
	TaskPath         state.Path
	ConversationPath state.Path
	ResultPath       state.Path
	MaxIterations    int
	PromptMaxChars   int
	Parallel         bool
	ToolName         string
	ToolDescription  string
}

func NewAgentNode(options ...NodeOption) *AgentNode {
	node := &AgentNode{
		Base: NewBase(Spec{
			Name:        NodeTypeAgent,
			Description: "Run a self-contained ReAct loop with configurable prompt and tools.",
		}),
		Parallel: true,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (a *AgentNode) Validate() error {
	if a == nil {
		return fmt.Errorf("agent node is nil")
	}
	if err := a.Base.Validate(); err != nil {
		return err
	}
	if a.TaskPath.Empty() || a.ConversationPath.Empty() || a.ResultPath.Empty() {
		return fmt.Errorf("agent node %q requires task, conversation, and result paths", a.ID())
	}
	return nil
}

func (a *AgentNode) GraphNodeSpec() dsl.GraphNodeSpec {
	conf := map[string]any{
		"tool_ids":         a.ToolIDs,
		"system_prompt":    a.SystemPrompt,
		"parallel":         a.Parallel,
		"tool_name":        a.ToolName,
		"tool_description": a.ToolDescription,
	}
	if strings.TrimSpace(a.ModelID) != "" {
		conf["model_id"] = a.ModelID
	}
	if a.MaxIterations > 0 {
		conf["max_iterations"] = a.MaxIterations
	}
	if a.PromptMaxChars > 0 {
		conf["prompt_max_chars"] = a.PromptMaxChars
	}
	return newGraphNodeSpec(a.Base, NodeTypeAgent, conf, map[string]state.Path{
		"task": a.TaskPath, "conversation": a.ConversationPath, "result": a.ResultPath,
	})
}

func AgentNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeAgent,
			Title:       "Agent Node",
			Description: "Run a self-contained ReAct loop: LLM inference and tool execution iterate inside the node until a final answer or the iteration cap is reached.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string"},
					"tool_ids": dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"max_iterations":   dsl.JSONSchema{"type": "integer", "minimum": 1},
					"prompt_max_chars": dsl.JSONSchema{"type": "integer", "minimum": 1},
					"parallel":         dsl.JSONSchema{"type": "boolean"},
					"tool_name":        dsl.JSONSchema{"type": "string"},
					"tool_description": dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			primitivePort("task", "Initial task for the agent.", "string", dsl.StateAccessRead, true),
			capabilityPort("conversation", "Conversation owned by this agent loop.", conversationcap.CapabilityID, true,
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldFinalAnswer, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldIterationCount, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMaxIterations, Mode: dsl.StateAccessReadWrite},
			),
			primitivePort("result", "Final answer produced by the agent.", "string", dsl.StateAccessWrite, true),
		},
		Build: func(ctx *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			_ = ctx
			spec := resolved.Spec
			taskPath, err := resolvedPath(resolved, "task")
			if err != nil {
				return nil, err
			}
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			resultPath, err := resolvedPath(resolved, "result")
			if err != nil {
				return nil, err
			}
			agentNode := NewAgentNode(WithID(spec.ID))
			applyNodeMetadata(&agentNode.Base, spec)
			agentNode.ModelID = config.String(spec.Config, "model_id")
			agentNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			agentNode.SystemPrompt = config.String(spec.Config, "system_prompt")
			agentNode.TaskPath = taskPath
			agentNode.ConversationPath = conversationPath
			agentNode.ResultPath = resultPath
			agentNode.MaxIterations, _ = config.Int(spec.Config, "max_iterations")
			agentNode.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			if parallel, ok := config.Bool(spec.Config, "parallel"); ok {
				agentNode.Parallel = parallel
			}
			agentNode.ToolName = config.String(spec.Config, "tool_name")
			agentNode.ToolDescription = config.String(spec.Config, "tool_description")
			return agentNode, nil
		},
	}
}

func (a *AgentNode) Execute(ctx core.Context, access *state.Access) error {
	if ctx.Model(a.ModelID) == nil {
		return fmt.Errorf("agent node: model %q not available", effectiveModelID(a.ModelID))
	}

	conversation, err := conversationcap.Bind(access, a.ConversationPath)
	if err != nil {
		return err
	}
	if a.MaxIterations > 0 && conversation.MaxIterations() < a.MaxIterations {
		if err := conversation.SetMaxIterations(a.MaxIterations); err != nil {
			return err
		}
	}

	task, err := state.Get(access, state.NewRef[string](a.TaskPath))
	if err != nil {
		return err
	}
	if err := a.seedConversation(conversation, strings.TrimSpace(task)); err != nil {
		return err
	}
	if err := a.runLoop(ctx, conversation); err != nil {
		return err
	}
	return state.Replace(access, state.NewRef[string](a.ResultPath), strings.TrimSpace(conversation.FinalAnswer()))
}

func (a *AgentNode) runLoop(ctx core.Context, conversation *conversationcap.View) error {
	model := ctx.Model(a.ModelID)
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

func (a *AgentNode) executeToolCalls(ctx core.Context, conversation *conversationcap.View, toolCalls []llms.ToolCall) error {
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

func (a *AgentNode) seedConversation(conversation *conversationcap.View, task string) error {
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

func (a *AgentNode) effectiveMaxIterations(conversation *conversationcap.View) int {
	if a.MaxIterations > 0 {
		return a.MaxIterations
	}
	if conversation != nil {
		if max := conversation.MaxIterations(); max > 0 {
			return max
		}
	}
	return conversationcap.DefaultMaxIterations
}

func (a *AgentNode) effectivePromptMaxChars() int {
	if a == nil || a.PromptMaxChars <= 0 {
		return defaultAgentPromptMaxChars
	}
	return a.PromptMaxChars
}

func (a *AgentNode) Contract() state.Contract {
	if a == nil {
		return state.Contract{}
	}
	return state.NewContract(
		state.FieldAccess{Path: a.TaskPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Type: "string"},
		state.FieldAccess{Path: a.ConversationPath.MustChild(conversationcap.FieldMessages), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "array"},
		state.FieldAccess{Path: a.ConversationPath.MustChild(conversationcap.FieldFinalAnswer), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "string"},
		state.FieldAccess{Path: a.ConversationPath.MustChild(conversationcap.FieldIterationCount), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "integer"},
		state.FieldAccess{Path: a.ConversationPath.MustChild(conversationcap.FieldMaxIterations), Mode: state.AccessReadWrite, Merge: state.MergeReplace, Type: "integer"},
		state.FieldAccess{Path: a.ResultPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Type: "string"},
	)
}

func (a *AgentNode) AsTool() core.Tool {
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

	return core.Tool{
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
			if coreCtx.Model(a.ModelID) == nil {
				return "", fmt.Errorf("agent tool: model %q not available", effectiveModelID(a.ModelID))
			}

			access := state.NewEditingAccess(state.NewState())
			conversation, err := conversationcap.Bind(access, state.Shared("agent_tool", "conversation"))
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
	AgentNodeID      string                    `json:"agent_node_id,omitempty"`
	ConversationPath string                    `json:"conversation_path,omitempty"`
	Iteration        int                       `json:"agent_iteration"`
	MaxIterations    int                       `json:"max_iterations,omitempty"`
	Messages         []conversationcap.Message `json:"messages,omitempty"`
	Tools            []llmToolArtifact         `json:"tools,omitempty"`
}

type agentResponseArtifact struct {
	AgentNodeID string                      `json:"agent_node_id,omitempty"`
	Iteration   int                         `json:"agent_iteration"`
	Choices     []llmResponseArtifactChoice `json:"choices,omitempty"`
}

func buildAgentPromptArtifact(a *AgentNode, messages []llms.MessageContent, tools []llms.Tool, iteration, maxIterations int) (agentPromptArtifact, error) {
	serialized, err := conversationcap.SerializeMessages(messages)
	if err != nil {
		return agentPromptArtifact{}, err
	}
	payload := agentPromptArtifact{
		AgentNodeID:      a.ID(),
		ConversationPath: a.ConversationPath.String(),
		Iteration:        iteration,
		MaxIterations:    maxIterations,
		Messages:         serialized,
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
