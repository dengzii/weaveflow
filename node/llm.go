package node

import (
	"errors"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/llms/parts"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"

	"github.com/tmc/langchaingo/llms"
)

type LLMNode struct {
	Base
	ToolIDs        []string
	PromptMaxChars int
}

func NewLLMNode(options ...NodeOption) *LLMNode {
	node := &LLMNode{
		Base: NewBase(Spec{
			Name:        NodeTypeLLM,
			Description: "Run one LLM inference turn against a scoped conversation.",
			Scope:       DefaultScope,
			AccessorUses: []AccessorUse{
				Use(accessors.ConversationID.Name()),
				UseRoot(accessors.ExecutionID.Name()),
				UseRoot(accessors.OrchestrationID.Name()),
			},
		}),
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func LLMNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeLLM,
			Title:       "LLM Node",
			Description: "Built-in model inference nodes.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"tool_ids":         dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"state_scope":      dsl.JSONSchema{"type": "string"},
					"prompt_max_chars": dsl.JSONSchema{"type": "integer", "minimum": 1},
				},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			scope := nodeStateScope(spec.Config)
			return dsl.StateContract{
				Fields: []dsl.StateFieldRef{
					{Path: scopedConversationPath(scope, accessors.ConversationFieldMessages), Mode: dsl.StateAccessReadWrite, Description: "Conversation messages sent to the model and extended with the model response."},
					{Path: scopedConversationPath(scope, accessors.ConversationFieldIterationCount), Mode: dsl.StateAccessReadWrite, Description: "Iteration counter used to stop tool loops and incremented after each model turn."},
					{Path: scopedConversationPath(scope, accessors.ConversationFieldMaxIterations), Mode: dsl.StateAccessRead, Description: "Maximum number of tool-using iterations allowed for the current conversation scope."},
					{Path: scopedConversationPath(scope, accessors.ConversationFieldFinalAnswer), Mode: dsl.StateAccessWrite, Description: "Final answer written when the model finishes without further tool calls."},
					{Path: state.Shared(accessors.KeyExecution).String(), Mode: dsl.StateAccessReadWrite, Description: "Plan step execution state: read current_step.id and track last_llm_step_id to scrub prior-step messages at step boundary."},
				},
			}, nil
		},
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (Node, error) {
			_ = ctx
			llmNode := NewLLMNode(WithScope(nodeStateScope(spec.Config)), WithID(spec.ID))
			applyNodeMetadata(&llmNode.Base, spec)
			llmNode.ToolIDs = config.StringSlice(spec.Config, "tool_ids")
			llmNode.PromptMaxChars, _ = config.Int(spec.Config, "prompt_max_chars")
			return llmNode, nil
		},
	}
}

func (n *LLMNode) Execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model()
	if model == nil {
		return errors.New("llm node: model service not available")
	}
	nodeTools := ctx.FilterTools(n.ToolIDs)

	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		return err
	}
	execution, err := state.UseAccessor(access, accessors.ExecutionID)
	if err != nil {
		return err
	}
	orchestration, err := state.UseAccessor(access, accessors.OrchestrationID)
	if err != nil {
		return err
	}

	if err := scrubPriorStepIfBoundaryCrossed(conversation, execution); err != nil {
		return err
	}
	messages := conversation.Messages()
	promptMessages := trimLLMPromptMessages(messages, n.effectivePromptMaxChars())
	synthesizePlannerToolResult := shouldSynthesizePlannerToolResult(orchestration, promptMessages)

	if conversation.IterationCount() >= conversation.MaxIterations() {
		message := "Maximum tool iterations reached. Please simplify the question or reduce tool usage."
		finalMessage := llms.TextParts(llms.ChatMessageTypeAI, message)
		if err := conversation.SetMessages(append(messages, finalMessage)); err != nil {
			return err
		}
		return conversation.SetFinalAnswer(message)
	}

	var toolSets []llms.Tool
	if !synthesizePlannerToolResult {
		for _, tool := range nodeTools {
			toolSets = append(toolSets, tool.NewTool())
		}
	}
	if payload, err := buildLLMPromptArtifact(promptMessages, toolSets, access.Scope(), conversation.IterationCount(), conversation.MaxIterations()); err == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.prompt", payload)
	}

	resp, err := model.GenerateContent(
		ctx,
		promptMessages,
		llms.WithTools(toolSets),
		llms.WithThinkingMode(llms.ThinkingModeHigh),
	)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.error", map[string]any{"error": err.Error()})
		return err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err := errors.New("llm returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.error", map[string]any{"error": err.Error()})
		return err
	}
	if payload := buildLLMResponseArtifact(resp); len(payload.Choices) > 0 {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "llm.response", payload)
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
				"message": "llm node received a tool call with no type",
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
		return conversation.SetFinalAnswer(extractText(aiMessage))
	}
	return nil
}

func (n *LLMNode) effectivePromptMaxChars() int {
	if n == nil || n.PromptMaxChars <= 0 {
		return 20000
	}
	return n.PromptMaxChars
}

func scrubPriorStepIfBoundaryCrossed(conversation accessors.Conversation, execution accessors.Execution) error {
	if conversation == nil || execution == nil {
		return nil
	}
	currentStep := execution.CurrentStep()
	currentStepID := ""
	if currentStep != nil {
		currentStepID, _ = currentStep["id"].(string)
	}
	lastStepID := execution.LastLLMStepID()
	if currentStepID == "" {
		return nil
	}
	if lastStepID == "" {
		return execution.SetLastLLMStepID(currentStepID)
	}
	if currentStepID == lastStepID {
		return nil
	}

	original := conversation.Messages()
	scrubbed := scrubAIAndToolMessages(original)
	if len(scrubbed) != len(original) {
		if err := conversation.SetMessages(scrubbed); err != nil {
			return err
		}
		if err := conversation.ResetIteration(); err != nil {
			return err
		}
	}
	return execution.SetLastLLMStepID(currentStepID)
}

func scrubAIAndToolMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return messages
	}
	out := make([]llms.MessageContent, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case llms.ChatMessageTypeSystem, llms.ChatMessageTypeHuman:
			out = append(out, msg)
		default:
		}
	}
	return out
}

func shouldSynthesizePlannerToolResult(orchestration accessors.Object, messages []llms.MessageContent) bool {
	if orchestration == nil || len(messages) == 0 {
		return false
	}
	modeValue, _ := orchestration.Field("mode")
	mode, _ := modeValue.(string)
	if !strings.EqualFold(strings.TrimSpace(mode), "planner") {
		return false
	}
	return messageHasToolResponses(messages[len(messages)-1])
}
