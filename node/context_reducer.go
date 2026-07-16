package node

import (
	"context"
	"errors"
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
	MaxMessages      int
	PreserveSystem   bool
	PreserveRecent   int
	SummaryPrefix    string
	ConversationPath state.Path
}

func NewContextReducerNode(options ...NodeOption) *ContextReducerNode {
	node := &ContextReducerNode{
		Base: NewBase(Spec{
			Name:        NodeTypeContextReducer,
			Description: "Compact older conversation context into a concise summary message.",
		}),
		MaxMessages:    defaultContextReducerMaxMessages,
		PreserveSystem: true,
		PreserveRecent: defaultContextReducerPreserveTail,
		SummaryPrefix:  defaultContextReducerSummaryLabel,
	}
	applyNodeOptions(&node.Base, options)
	ApplyDefaultStatePaths(node)
	return node
}

func (n *ContextReducerNode) Validate() error {
	if n == nil {
		return errors.New("context reducer node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.ConversationPath.Empty() {
		return errors.New("context reducer conversation path is required")
	}
	return nil
}

func (n *ContextReducerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"preserve_system": n.PreserveSystem,
	}
	if n.MaxMessages > 0 {
		config["max_messages"] = n.MaxMessages
	}
	if n.PreserveRecent >= 0 {
		config["preserve_recent"] = n.PreserveRecent
	}
	if strings.TrimSpace(n.SummaryPrefix) != "" {
		config["summary_prefix"] = n.SummaryPrefix
	}
	return newGraphNodeSpec(n.Base, NodeTypeContextReducer, config, map[string]state.Path{"conversation": n.ConversationPath})
}

func ContextReducerNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeContextReducer,
			Title:       "Context Reducer Node",
			Description: "Compact older conversation context into a summary message before the next model turn.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"max_messages":    dsl.JSONSchema{"type": "integer", "minimum": 2},
					"preserve_system": dsl.JSONSchema{"type": "boolean"},
					"preserve_recent": dsl.JSONSchema{"type": "integer", "minimum": 0},
					"summary_prefix":  dsl.JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		StatePorts: []dsl.StatePortDefinition{
			capabilityPort("conversation", "Conversation messages compacted by the reducer.", conversationcap.CapabilityID, true,
				dsl.RelativeStateFieldRef{Path: conversationcap.FieldMessages, Mode: dsl.StateAccessReadWrite}),
		},
		Build: func(ctx *registry.BuildContext, resolved registry.ResolvedNodeSpec) (Node, error) {
			_ = ctx
			spec := resolved.Spec
			conversationPath, err := resolvedPath(resolved, "conversation")
			if err != nil {
				return nil, err
			}
			reducerNode := NewContextReducerNode(WithID(spec.ID))
			applyNodeMetadata(&reducerNode.Base, spec)
			reducerNode.MaxMessages, _ = config.Int(spec.Config, "max_messages")
			if value, ok := config.Bool(spec.Config, "preserve_system"); ok {
				reducerNode.PreserveSystem = value
			}
			reducerNode.PreserveRecent, _ = config.Int(spec.Config, "preserve_recent")
			if value := config.String(spec.Config, "summary_prefix"); value != "" {
				reducerNode.SummaryPrefix = value
			}
			reducerNode.ConversationPath = conversationPath
			return reducerNode, nil
		},
	}
}

func (n *ContextReducerNode) Execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model()
	if model == nil {
		return errors.New("context reducer: model service not available")
	}

	conversation, err := conversationcap.Bind(access, n.ConversationPath)
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
	summary, err := n.reduceMessages(ctx, model, reducible)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "context.reducer.error", map[string]any{
			"conversation_path": n.ConversationPath.String(),
			"error":             err.Error(),
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
		"conversation_path":     n.ConversationPath.String(),
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
