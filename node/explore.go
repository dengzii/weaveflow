package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"weaveflow/core"
	fruntime "weaveflow/runtime"
	"weaveflow/state"
	"weaveflow/state/accessors"

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultExploreMaxIterations = 12
	defaultExploreToolResultCap = 4096
	exploreSystemPrompt         = "" +
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

type ExploreNode struct {
	Base
	ParentScope        string
	ExploreScope       string
	MaxIterations      int
	ToolIDs            []string
	SystemPrompt       string
	ToolResultCap      int
	IncludeEnvironment bool
	EnvironmentHeading string
}

func NewExploreNode(options ...NodeOption) *ExploreNode {
	node := &ExploreNode{
		Base: NewBase(Spec{
			Name:        "explore",
			Description: "Run an isolated file-reading loop and return a structured summary.",
		}),
		ParentScope:        DefaultScope,
		ExploreScope:       accessors.KeyExplore,
		MaxIterations:      defaultExploreMaxIterations,
		ToolIDs:            []string{"read", "grep", "glob"},
		ToolResultCap:      defaultExploreToolResultCap,
		IncludeEnvironment: true,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (n *ExploreNode) AccessorUses() []AccessorUse {
	return []AccessorUse{
		UseScoped(accessors.ConversationID.Name(), n.effectiveParentScope()),
		UseScoped(accessors.ConversationID.Name(), n.effectiveExploreScope()),
		UseRoot(accessors.RequestID.Name()),
		UseRoot(accessors.EnvironmentID.Name()),
		UseRoot(accessors.FinalID.Name()),
	}
}

func (n *ExploreNode) Execute(ctx context.Context, access *state.Access) error {
	svc := core.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return errors.New("explore node: model service not available")
	}

	parentAccess := access.WithScope(n.effectiveParentScope())
	parentConversation, err := state.UseAccessor(parentAccess, accessors.ConversationID)
	if err != nil {
		return err
	}

	request, err := n.resolveRequest(access, parentConversation)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.error", map[string]any{"error": err.Error()})
		return err
	}

	exploreScope := n.effectiveExploreScope()
	exploreAccess := access.WithScope(exploreScope)
	convo, err := state.UseAccessor(exploreAccess, accessors.ConversationID)
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

	nodeTools := svc.FilterTools(n.effectiveToolIDs())
	toolSets := make([]llms.Tool, 0, len(nodeTools))
	for _, tool := range nodeTools {
		toolSets = append(toolSets, tool.NewTool())
	}

	maxIter := n.effectiveMaxIterations()
	iter := 0
	terminated := false
	for ; iter < maxIter; iter++ {
		messages := convo.Messages()

		if payload, err := buildLLMPromptArtifact(messages, toolSets, exploreScope, iter, maxIter); err == nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.prompt", payload)
		}

		resp, err := svc.Model.GenerateContent(
			ctx,
			messages,
			llms.WithTools(toolSets),
			llms.WithThinkingMode(llms.ThinkingModeNone),
			llms.WithTemperature(0),
		)
		if err != nil {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.error", map[string]any{
				"error":     err.Error(),
				"iteration": iter,
			})
			return err
		}
		if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
			return errors.New("explore: model returned no choices")
		}
		if payload := buildLLMResponseArtifact(resp); len(payload.Choices) > 0 {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.response", payload)
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

	summary, err := summarizeExploration(ctx, svc.Model, convo.Messages())
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.summarizer.error", map[string]any{
			"error": err.Error(),
		})
		return err
	}

	if err := n.writeAnswerToParent(access, parentConversation, summary); err != nil {
		return err
	}

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":           "explore_done",
		"explore_scope":  exploreScope,
		"iterations":     iter,
		"terminated":     terminated,
		"summary_length": len(summary),
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.summary", map[string]any{
		"explore_scope": exploreScope,
		"iterations":    iter,
		"terminated":    terminated,
		"summary":       summary,
	})

	return nil
}

func (n *ExploreNode) renderSystemPrompt(access *state.Access) string {
	prompt := n.effectiveSystemPrompt()
	if !n.IncludeEnvironment {
		return prompt
	}
	environment, err := state.UseAccessor(access, accessors.EnvironmentID)
	if err != nil {
		return prompt
	}
	values := environment.Value()
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

func (n *ExploreNode) clampToolMessage(message llms.MessageContent) llms.MessageContent {
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

func (n *ExploreNode) writeAnswerToParent(access *state.Access, parent accessors.Conversation, summary string) error {
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
	final, err := state.UseAccessor(access, accessors.FinalID)
	if err != nil {
		return err
	}
	return final.SetAnswer(summary)
}

func (n *ExploreNode) resolveRequest(access *state.Access, parent accessors.Conversation) (string, error) {
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

	request, err := state.UseAccessor(access, accessors.RequestID)
	if err == nil {
		if input := strings.TrimSpace(request.Input()); input != "" {
			return input, nil
		}
	}
	return "", errors.New("explore: no user input found in parent scope or request state")
}

func (n *ExploreNode) effectiveExploreScope() string {
	if n == nil || strings.TrimSpace(n.ExploreScope) == "" {
		return accessors.KeyExplore
	}
	return strings.TrimSpace(n.ExploreScope)
}

func (n *ExploreNode) effectiveParentScope() string {
	if n == nil || strings.TrimSpace(n.ParentScope) == "" {
		return ""
	}
	return strings.TrimSpace(n.ParentScope)
}

func (n *ExploreNode) effectiveMaxIterations() int {
	if n == nil || n.MaxIterations <= 0 {
		return defaultExploreMaxIterations
	}
	return n.MaxIterations
}

func (n *ExploreNode) effectiveToolResultCap() int {
	if n == nil || n.ToolResultCap <= 0 {
		return defaultExploreToolResultCap
	}
	return n.ToolResultCap
}

func (n *ExploreNode) effectiveSystemPrompt() string {
	if n == nil || strings.TrimSpace(n.SystemPrompt) == "" {
		return exploreSystemPrompt
	}
	return n.SystemPrompt
}

func (n *ExploreNode) effectiveToolIDs() []string {
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
		return "", errors.New("explore summarizer: model is nil")
	}

	body := buildReducerTranscript(stripExploreSystemMessages(transcript))
	if strings.TrimSpace(body) == "" {
		return "", errors.New("explore summarizer: transcript is empty")
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
		return "", errors.New("explore summarizer returned no choices")
	}

	summary := strings.TrimSpace(resp.Choices[0].Content)
	if summary == "" {
		return "", errors.New("explore summarizer returned empty summary")
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
