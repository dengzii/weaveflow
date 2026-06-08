package node

import (
	"context"
	"errors"
	"strings"

	"weaveflow/core"
	fruntime "weaveflow/runtime"
	"weaveflow/state"
	"weaveflow/state/accessors"

	"github.com/tmc/langchaingo/llms"
)

const (
	defaultContextReducerMaxMessages  = 24
	defaultContextReducerPreserveTail = 6
	defaultContextReducerSummaryLabel = "Summary of earlier conversation:"
	contextReducerSystemPrompt        = "" +
		"You condense earlier conversation context for continued execution. " +
		"Preserve user goals, constraints, decisions, facts, open questions, and important tool results. " +
		"Do not answer the user. Do not invent facts. Return concise plain text bullet points."
)

type ContextReducerNode struct {
	Base
	MaxMessages    int
	PreserveSystem bool
	PreserveRecent int
	SummaryPrefix  string
}

func NewContextReducerNode(options ...NodeOption) *ContextReducerNode {
	node := &ContextReducerNode{
		Base: NewBase(Spec{
			Name:         NodeTypeContextReducer,
			Description:  "Compact older conversation context into a concise summary message.",
			Scope:        DefaultScope,
			AccessorUses: []AccessorUse{Use(accessors.ConversationID.Name())},
		}),
		MaxMessages:    defaultContextReducerMaxMessages,
		PreserveSystem: true,
		PreserveRecent: defaultContextReducerPreserveTail,
		SummaryPrefix:  defaultContextReducerSummaryLabel,
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (n *ContextReducerNode) Execute(ctx context.Context, access *state.Access) error {
	svc := core.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return errors.New("context reducer: model service not available")
	}

	conversation, err := state.UseAccessor(access, accessors.ConversationID)
	if err != nil {
		return err
	}
	messages := conversation.Messages()
	if len(messages) == 0 || len(messages) <= n.effectiveMaxMessages() {
		return nil
	}

	preservedSystem, body := splitReducerMessages(messages, n.PreserveSystem, n.effectiveSummaryPrefix())
	if len(body) == 0 {
		return nil
	}

	tailStart := n.reducerTailStart(body, len(preservedSystem))
	if tailStart <= 0 {
		return nil
	}

	reducible := body[:tailStart]
	recent := body[tailStart:]
	summary, err := n.reduceMessages(ctx, svc.Model, reducible)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.reducer.error", map[string]any{
			"state_scope": access.Scope(),
			"error":       err.Error(),
		})
		return err
	}

	reducedMessages := make([]llms.MessageContent, 0, len(preservedSystem)+len(recent)+1)
	reducedMessages = append(reducedMessages, preservedSystem...)
	reducedMessages = append(reducedMessages, llms.TextParts(llms.ChatMessageTypeSystem, n.renderSummary(summary)))
	reducedMessages = append(reducedMessages, recent...)
	if err := conversation.SetMessages(reducedMessages); err != nil {
		return err
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.reducer", map[string]any{
		"state_scope":           access.Scope(),
		"max_messages":          n.effectiveMaxMessages(),
		"preserve_system":       n.PreserveSystem,
		"preserve_recent":       n.effectivePreserveRecent(),
		"messages_before_count": len(messages),
		"messages_after_count":  len(reducedMessages),
		"messages_reduced":      len(reducible),
		"summary":               summary,
	})
	return nil
}

func (n *ContextReducerNode) reduceMessages(ctx context.Context, model llms.Model, messages []llms.MessageContent) (string, error) {
	transcript := buildReducerTranscript(messages)
	if strings.TrimSpace(transcript) == "" {
		return "", errors.New("context reducer transcript is empty")
	}

	resp, err := model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, contextReducerSystemPrompt),
			llms.TextParts(
				llms.ChatMessageTypeHuman,
				"Summarize the following earlier conversation for future turns.\n\n"+transcript,
			),
		},
		llms.WithTemperature(0),
	)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return "", errors.New("context reducer returned no choices")
	}

	summary := strings.TrimSpace(resp.Choices[0].Content)
	if summary == "" {
		return "", errors.New("context reducer returned empty summary")
	}
	return summary, nil
}

func (n *ContextReducerNode) reducerTailStart(messages []llms.MessageContent, preservedSystemCount int) int {
	preserveRecent := n.effectivePreserveRecent()
	if preserveRecent > len(messages) {
		preserveRecent = len(messages)
	}

	maxTail := n.effectiveMaxMessages() - preservedSystemCount - 1
	if maxTail < preserveRecent {
		preserveRecent = maxTail
	}
	if preserveRecent < 0 {
		preserveRecent = 0
	}

	start := len(messages) - preserveRecent
	if start < 0 {
		start = 0
	}
	return adjustReducerTailStart(messages, start)
}

func (n *ContextReducerNode) effectiveMaxMessages() int {
	if n == nil || n.MaxMessages <= 0 {
		return defaultContextReducerMaxMessages
	}
	return n.MaxMessages
}

func (n *ContextReducerNode) effectivePreserveRecent() int {
	if n == nil || n.PreserveRecent < 0 {
		return defaultContextReducerPreserveTail
	}
	return n.PreserveRecent
}

func (n *ContextReducerNode) effectiveSummaryPrefix() string {
	if n == nil || strings.TrimSpace(n.SummaryPrefix) == "" {
		return defaultContextReducerSummaryLabel
	}
	return strings.TrimSpace(n.SummaryPrefix)
}

func (n *ContextReducerNode) renderSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return n.effectiveSummaryPrefix()
	}
	return n.effectiveSummaryPrefix() + "\n" + summary
}
