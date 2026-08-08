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

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultLLMTurnPromptMaxChars = 200000
	defaultReasoningEffort       = "auto"
	reasoningEffortOptions       = "auto, none, minimal, low, medium, high, xhigh, or max"
)

type LLMTurnNode struct {
	Base
	ModelID          string
	ToolIDs          []string
	SystemPrompt     string
	PromptMaxChars   int
	ReasoningEffort  string
	ConversationPath state.Path
	OutputPath       state.Path
}

func NewLLMTurnNode(options ...NodeOption) *LLMTurnNode {
	node := &LLMTurnNode{
		Base: NewBase(Spec{
			Name:        NodeTypeLLMTurn,
			Description: "Run one LLM inference turn against a bound conversation.",
		}),
		ReasoningEffort: defaultReasoningEffort,
	}
	applyNodeOptions(&node.Base, options)
	ApplyDefaultStatePaths(node)
	return node
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
		"tool_ids":         n.ToolIDs,
		"system_prompt":    n.SystemPrompt,
		"reasoning_effort": n.effectiveReasoningEffort(),
	}
	if strings.TrimSpace(n.ModelID) != "" {
		conf["model_id"] = n.ModelID
	}
	if n.PromptMaxChars > 0 {
		conf["prompt_max_chars"] = n.PromptMaxChars
	}
	return newGraphNodeSpec(n.Base, NodeTypeLLMTurn, conf, map[string]state.Path{
		"conversation": n.ConversationPath,
		"output":       n.OutputPath,
	})
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
					"model_id": dsl.JSONSchema{"type": "string"},
					"tool_ids": dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt": dsl.JSONSchema{
						"type":      "string",
						"title":     "System Prompt",
						"x-control": "textarea",
					},
					"prompt_max_chars": dsl.JSONSchema{
						"type": "integer", "minimum": 1, "default": defaultLLMTurnPromptMaxChars,
					},
					"reasoning_effort": reasoningEffortSchema(),
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
			primitivePort("output", "Optional final text output.", "string", dsl.StateAccessWrite, false),
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

func (n *LLMTurnNode) Execute(ctx core.Context, access *state.Access) error {
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

	var toolSets []llms.Tool
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.NewTool())
	}
	if payload, err := buildLLMPromptArtifact(promptMessages, toolSets, n.ConversationPath.String(), conversation.IterationCount(), conversation.MaxIterations()); err == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm_turn.prompt", payload)
	}

	resp, err := model.GenerateContent(
		ctx,
		promptMessages,
		llms.WithTools(toolSets),
		llms.WithThinkingMode(llms.ThinkingMode(n.effectiveReasoningEffort())),
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
	for _, toolCall := range choice.ToolCalls {
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
	if len(choice.ToolCalls) == 0 {
		answer := extractText(aiMessage)
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
