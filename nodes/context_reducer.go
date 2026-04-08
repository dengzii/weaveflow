package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"weaveflow/dsl"

	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
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
	NodeInfo
	model          llms.Model
	StateScope     string
	MaxMessages    int
	PreserveSystem bool
	PreserveRecent int
	SummaryPrefix  string
}

func NewContextReducerNode(model llms.Model) *ContextReducerNode {
	id := uuid.New()
	return &ContextReducerNode{
		NodeInfo: NodeInfo{
			NodeID:          "ContextReducer_" + id.String(),
			NodeName:        "ContextReducer",
			NodeDescription: "Compact older conversation context into a concise summary message.",
		},
		model:          model,
		MaxMessages:    defaultContextReducerMaxMessages,
		PreserveSystem: true,
		PreserveRecent: defaultContextReducerPreserveTail,
		SummaryPrefix:  defaultContextReducerSummaryLabel,
	}
}

func (n *ContextReducerNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if n.model == nil {
		return state, errors.New("context reducer model is nil")
	}

	conversation := fruntime.Conversation(state, n.StateScope)
	messages := conversation.Messages()
	if len(messages) == 0 || len(messages) <= n.effectiveMaxMessages() {
		return state, nil
	}

	preservedSystem, body := splitReducerMessages(messages, n.PreserveSystem, n.effectiveSummaryPrefix())
	if len(body) == 0 {
		return state, nil
	}

	tailStart := n.reducerTailStart(body, len(preservedSystem))
	if tailStart <= 0 {
		return state, nil
	}

	reducible := body[:tailStart]
	recent := body[tailStart:]
	summary, err := n.reduceMessages(ctx, reducible)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.reducer.error", map[string]any{
			"state_scope": n.StateScope,
			"error":       err.Error(),
		})
		return state, err
	}

	reducedMessages := make([]llms.MessageContent, 0, len(preservedSystem)+len(recent)+1)
	reducedMessages = append(reducedMessages, preservedSystem...)
	reducedMessages = append(reducedMessages, llms.TextParts(llms.ChatMessageTypeSystem, n.renderSummary(summary)))
	reducedMessages = append(reducedMessages, recent...)
	conversation.UpdateMessage(reducedMessages)

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.reducer", map[string]any{
		"state_scope":           n.StateScope,
		"max_messages":          n.effectiveMaxMessages(),
		"preserve_system":       n.PreserveSystem,
		"preserve_recent":       n.effectivePreserveRecent(),
		"messages_before_count": len(messages),
		"messages_after_count":  len(reducedMessages),
		"messages_reduced":      len(reducible),
		"summary":               summary,
	})

	return state, nil
}

func (n *ContextReducerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "context_reducer",
		Description: n.Description(),
		Config: map[string]any{
			"state_scope":     n.StateScope,
			"max_messages":    n.effectiveMaxMessages(),
			"preserve_system": n.PreserveSystem,
			"preserve_recent": n.effectivePreserveRecent(),
			"summary_prefix":  n.effectiveSummaryPrefix(),
		},
	}
}

func (n *ContextReducerNode) reduceMessages(ctx context.Context, messages []llms.MessageContent) (string, error) {
	transcript := buildReducerTranscript(messages)
	if strings.TrimSpace(transcript) == "" {
		return "", errors.New("context reducer transcript is empty")
	}

	resp, err := n.model.GenerateContent(
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

func splitReducerMessages(messages []llms.MessageContent, preserveSystem bool, summaryPrefix string) ([]llms.MessageContent, []llms.MessageContent) {
	if !preserveSystem || len(messages) == 0 {
		return nil, cloneReducerMessages(messages)
	}

	index := 0
	for index < len(messages) {
		if messages[index].Role != llms.ChatMessageTypeSystem {
			break
		}
		if isReducerSummaryMessage(messages[index], summaryPrefix) {
			break
		}
		index++
	}
	return cloneReducerMessages(messages[:index]), cloneReducerMessages(messages[index:])
}

func adjustReducerTailStart(messages []llms.MessageContent, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	for start > 0 {
		current := messages[start]
		previous := messages[start-1]
		if current.Role == llms.ChatMessageTypeTool || previous.Role == llms.ChatMessageTypeTool || messageHasToolCalls(previous) {
			start--
			continue
		}
		break
	}
	return start
}

func buildReducerTranscript(messages []llms.MessageContent) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(reducerMessageText(message))
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", reducerRoleLabel(message.Role), text))
	}
	return strings.Join(lines, "\n\n")
}

func reducerMessageText(message llms.MessageContent) string {
	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		switch typed := part.(type) {
		case llms.TextContent:
			text := strings.TrimSpace(typed.Text)
			if text != "" {
				parts = append(parts, text)
			}
		case llms.ToolCall:
			name := ""
			arguments := ""
			if typed.FunctionCall != nil {
				name = strings.TrimSpace(typed.FunctionCall.Name)
				arguments = strings.TrimSpace(typed.FunctionCall.Arguments)
			}
			item := "tool_call"
			if name != "" {
				item += " " + name
			}
			if arguments != "" {
				item += " " + arguments
			}
			parts = append(parts, item)
		case llms.ToolCallResponse:
			item := "tool_result"
			if name := strings.TrimSpace(typed.Name); name != "" {
				item += " " + name
			}
			if content := strings.TrimSpace(typed.Content); content != "" {
				item += " " + content
			}
			parts = append(parts, item)
		}
	}
	return strings.Join(parts, "\n")
}

func reducerRoleLabel(role llms.ChatMessageType) string {
	switch role {
	case llms.ChatMessageTypeSystem:
		return "system"
	case llms.ChatMessageTypeHuman:
		return "human"
	case llms.ChatMessageTypeAI:
		return "assistant"
	case llms.ChatMessageTypeTool:
		return "tool"
	default:
		return strings.TrimSpace(string(role))
	}
}

func isReducerSummaryMessage(message llms.MessageContent, summaryPrefix string) bool {
	if message.Role != llms.ChatMessageTypeSystem {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(extractText(message)), strings.TrimSpace(summaryPrefix))
}

func messageHasToolCalls(message llms.MessageContent) bool {
	for _, part := range message.Parts {
		if _, ok := part.(llms.ToolCall); ok {
			return true
		}
	}
	return false
}

func cloneReducerMessages(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) == 0 {
		return nil
	}

	cloned := make([]llms.MessageContent, len(messages))
	for i, message := range messages {
		cloned[i] = llms.MessageContent{
			Role:  message.Role,
			Parts: append([]llms.ContentPart(nil), message.Parts...),
		}
	}
	return cloned
}
