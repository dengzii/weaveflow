package node

import (
	"errors"
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/llms/parts"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultLLMTurnPromptMaxChars = 200000
	defaultReasoningEffort       = "auto"
	reasoningEffortOptions       = "auto, none, minimal, low, medium, high, xhigh, or max"
	finalIterationPrompt         = "The conversation has exhausted its tool-iteration budget. Do not call tools. Return the best supported final answer now using the tool results already present, and explicitly acknowledge any material limitation."
)

type LLMTurnNode struct {
	Base
	ModelID                    string
	ToolIDs                    []string
	SystemPrompt               string
	PromptMaxChars             int
	ReasoningEffort            string
	FinalizeAfterMaxIterations bool
	ConversationPath           state.Path
	OutputPath                 state.Path
}

func NewLLMTurnNode(options ...Option) *LLMTurnNode {
	target := &LLMTurnNode{
		Base: NewBase(Spec{
			Name:        NodeTypeLLMTurn,
			Description: "Run one LLM inference turn against a bound conversation.",
		}),
		ReasoningEffort:            defaultReasoningEffort,
		FinalizeAfterMaxIterations: true,
	}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *LLMTurnNode) Validate() error {
	if n == nil {
		return fmt.Errorf("llm turn node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.ConversationPath.Empty() {
		return fmt.Errorf("llm turn node %q requires conversation path", n.ID())
	}
	if !isReasoningEffort(n.effectiveReasoningEffort()) {
		return fmt.Errorf("llm turn node %q reasoning_effort must be one of %s", n.ID(), reasoningEffortOptions)
	}
	return nil
}

func (n *LLMTurnNode) GraphNodeSpec() dsl.GraphNodeSpec {
	conf := map[string]any{
		"tool_ids":                      n.ToolIDs,
		"system_prompt":                 n.SystemPrompt,
		"reasoning_effort":              n.effectiveReasoningEffort(),
		"finalize_after_max_iterations": n.FinalizeAfterMaxIterations,
	}
	if strings.TrimSpace(n.ModelID) != "" {
		conf["model_id"] = n.ModelID
	}
	if n.PromptMaxChars > 0 {
		conf["prompt_max_chars"] = n.PromptMaxChars
	}
	statePaths := map[string]state.Path{"conversation": n.ConversationPath}
	if !n.OutputPath.Empty() {
		statePaths["output"] = n.OutputPath
	}
	return newGraphNodeSpec(n.Base, NodeTypeLLMTurn, conf, statePaths)
}

func LLMTurnNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeLLMTurn,
			Title:       "LLM Turn",
			Description: "Run one model inference turn against a bound conversation.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"model_id": dsl.JSONSchema{"type": "string", "title": "Model ID"},
					"tool_ids": dsl.JSONSchema{"type": "array", "title": "Tools", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"prompt_max_chars": dsl.JSONSchema{
						"type": "integer", "title": "Prompt Character Limit", "minimum": 1, "default": defaultLLMTurnPromptMaxChars,
						"description": "Maximum character budget for conversation messages sent to the model; older messages are trimmed when exceeded.",
					},
					"reasoning_effort": reasoningEffortSchema(),
					"finalize_after_max_iterations": dsl.JSONSchema{
						"type": "boolean", "title": "Finalize After Max Iterations", "default": true,
						"description": "When invoked after the tool-iteration limit, disable tools and require a final answer from the evidence already collected.",
					},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("conversation", "Conversation messages and loop state.", conversationcap.CapabilityID, true,
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldFinalAnswer, Mode: dsl.StateAccessWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldIterationCount, Mode: dsl.StateAccessReadWrite},
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMaxIterations, Mode: dsl.StateAccessRead},
			),
			primitivePortWithDefault("output", "Optional text output. Bind this explicitly only when the turn owns a durable output field.", "string", dsl.StateAccessWrite, false, ""),
		},
		Build: func(ctx *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			_ = ctx
			spec := resolved.Spec
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			llmTurnNode := NewLLMTurnNode(WithID(spec.ID))
			applyNodeMetadata(&llmTurnNode.Base, spec)
			llmTurnNode.ModelID = config.String(spec.Config, "model_id")
			llmTurnNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			llmTurnNode.SystemPrompt = config.String(spec.Config, "system_prompt")
			llmTurnNode.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			if value, ok := config.Bool(spec.Config, "finalize_after_max_iterations"); ok {
				llmTurnNode.FinalizeAfterMaxIterations = value
			}
			if reasoningEffort := strings.TrimSpace(config.String(spec.Config, "reasoning_effort")); reasoningEffort != "" {
				if !isReasoningEffort(reasoningEffort) {
					return nil, fmt.Errorf("build llm turn node %q: reasoning_effort must be one of %s", spec.ID, reasoningEffortOptions)
				}
				llmTurnNode.ReasoningEffort = reasoningEffort
			}
			llmTurnNode.ConversationPath = conversationPath
			llmTurnNode.OutputPath = optionalResolvedPath(resolved, "output")
			return llmTurnNode, nil
		},
	}
}

func (n *LLMTurnNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *LLMTurnNode) execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model(n.ModelID)
	if model == nil {
		return fmt.Errorf("llm turn node: model %q not available", effectiveModelID(n.ModelID))
	}
	var nodeTools map[string]core.Tool
	if len(n.ToolIDs) > 0 {
		nodeTools = ctx.FilterTools(n.ToolIDs)
	}

	conversation, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	if err := n.seedSystemPrompt(conversation); err != nil {
		return err
	}
	messages := conversation.Messages()
	promptMessages := trimLLMPromptMessages(messages, n.effectivePromptMaxChars())
	forceFinalization := n.FinalizeAfterMaxIterations && len(n.ToolIDs) > 0 && conversation.IterationCount() >= conversation.MaxIterations()
	if forceFinalization {
		nodeTools = nil
		promptMessages = append(promptMessages, llms.TextParts(llms.ChatMessageTypeHuman, finalIterationPrompt))
	}

	var toolSets []llms.ToolDefinition
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.Definition())
	}
	if payload, err := buildLLMPromptArtifact(promptMessages, toolSets, n.ConversationPath.String(), conversation.IterationCount(), conversation.MaxIterations()); err == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm_turn.prompt", payload)
	}

	resp, err := core.GenerateModel(
		ctx,
		model,
		llms.ModelRequest{
			ModelID:  effectiveModelID(n.ModelID),
			Mode:     llms.ModelModeChat,
			Messages: promptMessages,
			Tools:    toolSets,
			Thinking: llms.ThinkingMode(n.effectiveReasoningEffort()),
		},
	)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm_turn.error", map[string]any{"error": err.Error()})
		return err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err := errors.New("llm turn returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm_turn.error", map[string]any{"error": err.Error()})
		return err
	}
	if payload := buildLLMResponseArtifact(resp); len(payload.Choices) > 0 {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm_turn.response", payload)
	}

	choice := resp.Choices[0]
	aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
	if strings.TrimSpace(choice.ReasoningContent) != "" {
		aiMessage.Parts = append(aiMessage.Parts, parts.NewReasoningPart(choice.ReasoningContent))
	}
	if strings.TrimSpace(choice.Content) != "" {
		aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
	}
	toolCalls := choice.ToolCalls
	if forceFinalization && len(toolCalls) > 0 {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, map[string]any{
			"message": "llm turn node ignored tool calls returned during forced finalization",
		})
		toolCalls = nil
	}
	for _, toolCall := range toolCalls {
		if toolCall.Type == "" {
			_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, map[string]any{
				"message": "llm turn node received a tool call with no type",
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
	if len(toolCalls) == 0 {
		answer := extractText(aiMessage)
		if forceFinalization && strings.TrimSpace(answer) == "" {
			return errors.New("llm turn: model returned no answer during forced finalization")
		}
		if err := conversation.SetFinalAnswer(answer); err != nil {
			return err
		}
		return n.writeOutput(access, answer)
	}
	return nil
}

func (n *LLMTurnNode) seedSystemPrompt(conversation *conversationcap.View) error {
	if n == nil || conversation == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return nil
	}
	messages := conversation.Messages()
	for _, message := range messages {
		if message.Role == llms.ChatMessageTypeSystem {
			return nil
		}
	}
	return conversation.SetMessages(append([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, n.SystemPrompt),
	}, messages...))
}

func (n *LLMTurnNode) writeOutput(access *state.Access, value string) error {
	if n == nil || n.OutputPath.Empty() {
		return nil
	}
	return state.Replace(access, state.NewRef[string](n.OutputPath), value)
}

func effectiveModelID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return core.DefaultModelID
	}
	return id
}

func (n *LLMTurnNode) effectivePromptMaxChars() int {
	if n == nil || n.PromptMaxChars <= 0 {
		return defaultLLMTurnPromptMaxChars
	}
	return n.PromptMaxChars
}

func (n *LLMTurnNode) effectiveReasoningEffort() string {
	if n == nil || strings.TrimSpace(n.ReasoningEffort) == "" {
		return defaultReasoningEffort
	}
	return strings.TrimSpace(n.ReasoningEffort)
}

func reasoningEffortSchema() dsl.JSONSchema {
	return dsl.JSONSchema{
		"type":        "string",
		"title":       "Reasoning Effort",
		"description": "Controls model reasoning effort when the selected model supports it.",
		"enum":        []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"},
		"default":     defaultReasoningEffort,
	}
}

func isReasoningEffort(value string) bool {
	switch value {
	case "auto", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
