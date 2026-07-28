package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultExploreAgentMaxIterations = 12
	defaultExploreAgentToolResultCap = 4096
	exploreAgentSystemPrompt         = "" +
		"You are a codebase explorer running in an isolated sub-session. " +
		"Your job: answer the user's question by inspecting files in the workspace. " +
		"\n" +
		"Strategy:\n" +
		"1. Prefer `grep` and `glob` to locate relevant files before reading them.\n" +
		"2. Use `read` only on files you've already narrowed down. Read in small chunks; never request large limits.\n" +
		"3. Track which files you've examined; do not re-read the same file.\n" +
		"4. As soon as you have enough to answer, stop calling tools and reply with plain text.\n" +
		"\n" +
		"Output rules:\n" +
		"- Be terse. Cite `path:line` references rather than quoting large blocks.\n" +
		"- Never paste entire files. When showing code, show 1-5 line snippets max.\n" +
		"- Stop when answered. You are not the final responder; a separate summarizer will format your reply for the user."
)

type ExploreAgentNode struct {
	Base
	MaxIterations          int
	ToolIDs                []string
	SystemPrompt           string
	ToolResultCap          int
	IncludeEnvironment     bool
	EnvironmentHeading     string
	TaskPath               state.Path
	ParentConversationPath state.Path
	ConversationPath       state.Path
	EnvironmentPath        state.Path
	ResultPath             state.Path
}

func NewExploreAgentNode(options ...NodeOption) *ExploreAgentNode {
	node := &ExploreAgentNode{
		Base: NewBase(Spec{
			Name:        NodeTypeExploreAgent,
			Description: "Run an isolated file-reading loop and return a structured summary.",
		}),
		MaxIterations:      defaultExploreAgentMaxIterations,
		ToolIDs:            []string{"read", "grep", "glob"},
		ToolResultCap:      defaultExploreAgentToolResultCap,
		IncludeEnvironment: true,
	}
	applyNodeOptions(&node.Base, options)
	ApplyDefaultStatePaths(node)
	return node
}

func (n *ExploreAgentNode) Validate() error {
	if n == nil {
		return errors.New("explore agent node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.TaskPath.Empty() || n.ParentConversationPath.Empty() || n.ConversationPath.Empty() || n.ResultPath.Empty() {
		return fmt.Errorf("explore agent node %q requires task, parent_conversation, conversation, and result paths", n.ID())
	}
	return nil
}

func (n *ExploreAgentNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.Base, NodeTypeExploreAgent, map[string]any{
		"max_iterations": n.MaxIterations, "tool_ids": n.ToolIDs, "system_prompt": n.SystemPrompt,
		"tool_result_cap": n.ToolResultCap, "include_environment": n.IncludeEnvironment, "environment_heading": n.EnvironmentHeading,
	}, map[string]state.Path{
		"task": n.TaskPath, "parent_conversation": n.ParentConversationPath, "conversation": n.ConversationPath,
		"environment": n.EnvironmentPath, "result": n.ResultPath,
	})
}

func ExploreAgentNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: NodeTypeExploreAgent, Title: "Explore Agent", Description: "Run an isolated file-reading loop and return a structured summary.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object", "properties": dsl.JSONSchema{
					"max_iterations":      dsl.JSONSchema{"type": "integer", "minimum": 1},
					"tool_ids":            dsl.JSONSchema{"type": "array", "items": dsl.JSONSchema{"type": "string"}},
					"system_prompt":       dsl.JSONSchema{"type": "string", "x-control": "textarea"},
					"tool_result_cap":     dsl.JSONSchema{"type": "integer", "minimum": 1},
					"include_environment": dsl.JSONSchema{"type": "boolean"},
					"environment_heading": dsl.JSONSchema{"type": "string"},
				}, "additionalProperties": false,
			},
			StatePorts: []dsl.StatePortDefinition{
				primitivePort("task", "Exploration request.", "string", dsl.StateAccessRead, true),
				capabilityPort("parent_conversation", "Parent conversation receiving the summary.", conversationcap.CapabilityID, true,
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldFinalAnswer, Mode: dsl.StateAccessWrite}),
				capabilityPort("conversation", "Isolated exploration conversation.", conversationcap.CapabilityID, true,
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldIterationCount, Mode: dsl.StateAccessReadWrite},
					dsl.RelativeStateFieldRef{Path: conversationcap.FieldMaxIterations, Mode: dsl.StateAccessReadWrite}),
				primitivePort("environment", "Optional environment context.", "object", dsl.StateAccessRead, false),
				primitivePort("result", "Exploration summary.", "string", dsl.StateAccessWrite, true),
			},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			spec := resolved.Spec
			taskPath, err := resolvedPath(resolved, "task")
			if err != nil {
				return nil, err
			}
			parentPath, err := resolvedPath(resolved, "parent_conversation")
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
			target := NewExploreAgentNode(WithID(spec.ID))
			applyNodeMetadata(&target.Base, spec)
			if value, ok := config.Int(spec.Config, "max_iterations"); ok {
				target.MaxIterations = value
			}
			if values := config.StringSlice(spec.Config, "tool_ids"); len(values) > 0 {
				target.ToolIDs = values
			}
			if value := config.String(spec.Config, "system_prompt"); value != "" {
				target.SystemPrompt = value
			}
			if value, ok := config.Int(spec.Config, "tool_result_cap"); ok {
				target.ToolResultCap = value
			}
			if value, ok := config.Bool(spec.Config, "include_environment"); ok {
				target.IncludeEnvironment = value
			}
			target.EnvironmentHeading = config.String(spec.Config, "environment_heading")
			target.TaskPath, target.ParentConversationPath, target.ConversationPath = taskPath, parentPath, conversationPath
			target.EnvironmentPath = optionalResolvedPath(resolved, "environment")
			target.ResultPath = resultPath
			return target, nil
		},
	}
}

func (n *ExploreAgentNode) Execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model()
	if model == nil {
		return errors.New("explore agent node: model service not available")
	}

	parentConversation, err := conversationcap.Bind(access, n.ParentConversationPath)
	if err != nil {
		return err
	}

	request, err := n.resolveRequest(access, parentConversation)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore_agent.error", map[string]any{"error": err.Error()})
		return err
	}

	convo, err := conversationcap.Bind(access, n.ConversationPath)
	if err != nil {
		return err
	}
	if err := convo.SetMaxIterations(n.effectiveMaxIterations()); err != nil {
		return err
	}
	if err := convo.SetMessages([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, n.renderSystemPrompt(access)),
		llms.TextParts(llms.ChatMessageTypeHuman, request),
	}); err != nil {
		return err
	}

	nodeTools := ctx.FilterTools(n.effectiveToolIDs())
	toolSets := make([]llms.Tool, 0, len(nodeTools))
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.NewTool())
	}

	maxIter := n.effectiveMaxIterations()
	iter := 0
	terminated := false
	for ; iter < maxIter; iter++ {
		messages := convo.Messages()

		if payload, err := buildLLMPromptArtifact(messages, toolSets, n.ConversationPath.String(), iter, maxIter); err == nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore_agent.prompt", payload)
		}

		resp, err := model.GenerateContent(
			ctx,
			messages,
			llms.WithTools(toolSets),
			llms.WithThinkingMode(llms.ThinkingModeNone),
			llms.WithTemperature(0),
		)
		if err != nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore_agent.error", map[string]any{
				"error":     err.Error(),
				"iteration": iter,
			})
			return err
		}
		if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
			return errors.New("explore agent: model returned no choices")
		}
		if payload := buildLLMResponseArtifact(resp); len(payload.Choices) > 0 {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore_agent.response", payload)
		}

		choice := resp.Choices[0]
		aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
		if strings.TrimSpace(choice.Content) != "" {
			aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
		}
		for _, toolCall := range choice.ToolCalls {
			if toolCall.Type == "" {
				continue
			}
			aiMessage.Parts = append(aiMessage.Parts, toolCall)
		}
		if err := convo.SetMessages(append(messages, aiMessage)); err != nil {
			return err
		}
		if err := convo.IncrementIteration(); err != nil {
			return err
		}

		if len(choice.ToolCalls) == 0 {
			terminated = true
			break
		}

		toolMessages := make([]llms.MessageContent, 0, len(choice.ToolCalls))
		for _, toolCall := range choice.ToolCalls {
			result := executeToolCallMessage(ctx, toolCall)
			toolMessages = append(toolMessages, n.clampToolMessage(result))
		}
		if err := convo.SetMessages(append(convo.Messages(), toolMessages...)); err != nil {
			return err
		}
	}

	if !terminated {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventWarning, map[string]any{
			"node":      n.Name(),
			"message":   "explore reached max iterations without natural termination",
			"iteration": iter,
		})
	}

	summary, err := summarizeExploration(ctx, model, convo.Messages())
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore_agent.summarizer.error", map[string]any{
			"error": err.Error(),
		})
		return err
	}

	if err := n.writeAnswerToParent(access, parentConversation, summary); err != nil {
		return err
	}

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":              "explore_done",
		"conversation_path": n.ConversationPath.String(),
		"iterations":        iter,
		"terminated":        terminated,
		"summary_length":    len(summary),
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore_agent.summary", map[string]any{
		"conversation_path": n.ConversationPath.String(),
		"iterations":        iter,
		"terminated":        terminated,
		"summary":           summary,
	})

	return nil
}

func (n *ExploreAgentNode) renderSystemPrompt(access *state.Access) string {
	prompt := n.effectiveSystemPrompt()
	if !n.IncludeEnvironment {
		return prompt
	}
	if n.EnvironmentPath.Empty() {
		return prompt
	}
	rawValue, ok := access.ReadAny(n.EnvironmentPath)
	if !ok {
		return prompt
	}
	values, _ := rawValue.(map[string]any)
	if len(values) == 0 {
		return prompt
	}
	raw, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return prompt
	}
	heading := strings.TrimSpace(n.EnvironmentHeading)
	if heading == "" {
		heading = "Environment context:"
	}
	return prompt + "\n\n" + heading + "\n" + string(raw)
}

func (n *ExploreAgentNode) clampToolMessage(message llms.MessageContent) llms.MessageContent {
	cap := n.effectiveToolResultCap()
	if cap <= 0 {
		return message
	}
	for i, part := range message.Parts {
		typed, ok := part.(llms.ToolCallResponse)
		if !ok {
			continue
		}
		if len(typed.Content) <= cap {
			continue
		}
		full := len(typed.Content)
		typed.Content = typed.Content[:cap] + fmt.Sprintf("\n[truncated: showing first %d of %d bytes]", cap, full)
		message.Parts[i] = typed
	}
	return message
}

func (n *ExploreAgentNode) writeAnswerToParent(access *state.Access, parent *conversationcap.View, summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	messages := parent.Messages()
	if !lastExploreMessageMatches(messages, summary) {
		if err := parent.SetMessages(append(messages, llms.TextParts(llms.ChatMessageTypeAI, summary))); err != nil {
			return err
		}
	}
	if err := parent.SetFinalAnswer(summary); err != nil {
		return err
	}
	return state.Replace(access, state.NewRef[string](n.ResultPath), summary)
}

func (n *ExploreAgentNode) resolveRequest(access *state.Access, parent *conversationcap.View) (string, error) {
	messages := parent.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		text := strings.TrimSpace(extractText(messages[i]))
		if text != "" {
			return text, nil
		}
	}

	request, err := state.Get(access, state.NewRef[string](n.TaskPath))
	if err == nil {
		if input := strings.TrimSpace(request); input != "" {
			return input, nil
		}
	}
	return "", errors.New("explore agent: no user input found in parent conversation or task state")
}

func (n *ExploreAgentNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return defaultExploreAgentMaxIterations
	}
	return n.MaxIterations
}

func (n *ExploreAgentNode) effectiveToolResultCap() int {
	if n == nil || n.ToolResultCap <= 0 {
		return defaultExploreAgentToolResultCap
	}
	return n.ToolResultCap
}

func (n *ExploreAgentNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return exploreAgentSystemPrompt
	}
	return n.SystemPrompt
}

func (n *ExploreAgentNode) effectiveToolIDs() []string {
	if n == nil || len(n.ToolIDs) == 0 {
		return []string{"read", "grep", "glob"}
	}
	out := make([]string, len(n.ToolIDs))
	copy(out, n.ToolIDs)
	return out
}

func lastExploreMessageMatches(messages []llms.MessageContent, answer string) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	if last.Role != llms.ChatMessageTypeAI {
		return false
	}
	return strings.TrimSpace(extractText(last)) == answer
}

const exploreSummarizerSystemPrompt = "" +
	"You are summarizing a codebase exploration session. " +
	"Produce a structured answer with four short sections in this order:\n" +
	"  1. Direct answer - answer the user's question in 1-3 sentences.\n" +
	"  2. Key files - bullet list, each entry `path` followed by a one-line role.\n" +
	"  3. Important locations - bullet list of `path:line` references the user can jump to.\n" +
	"  4. Open questions - list anything you could not determine; empty if none.\n" +
	"Be terse. Never paste raw file contents. Never invent facts that did not appear in the exploration."

func summarizeExploration(ctx context.Context, model llms.Model, transcript []llms.MessageContent) (string, error) {
	if model == nil {
		return "", errors.New("explore agent summarizer: model is nil")
	}

	body := buildReducerTranscript(stripExploreSystemMessages(transcript))
	if strings.TrimSpace(body) == "" {
		return "", errors.New("explore agent summarizer: transcript is empty")
	}

	resp, err := model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, exploreSummarizerSystemPrompt),
			llms.TextParts(
				llms.ChatMessageTypeHuman,
				"Summarize this codebase exploration for the user.\n\n"+body,
			),
		},
		llms.WithTemperature(0),
	)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return "", errors.New("explore agent summarizer returned no choices")
	}

	summary := strings.TrimSpace(resp.Choices[0].Content)
	if summary == "" {
		return "", errors.New("explore agent summarizer returned empty summary")
	}
	return summary, nil
}

func stripExploreSystemMessages(messages []llms.MessageContent) []llms.MessageContent {
	out := make([]llms.MessageContent, 0, len(messages))
	for _, message := range messages {
		if message.Role == llms.ChatMessageTypeSystem {
			continue
		}
		out = append(out, message)
	}
	return out
}
