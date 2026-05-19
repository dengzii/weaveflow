package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
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
		"2. Use `file_read` only on files you've already narrowed down. Read in small chunks; never request large limits.\n" +
		"3. Track which files you've examined; do not re-read the same file.\n" +
		"4. As soon as you have enough to answer, stop calling tools and reply with plain text.\n" +
		"\n" +
		"Output rules:\n" +
		"- Be terse. Cite `path:line` references rather than quoting large blocks.\n" +
		"- Never paste entire files. When showing code, show 1-5 line snippets max.\n" +
		"- Stop when answered. You are not the final responder; a separate summarizer will format your reply for the user."
)

type ExploreNode struct {
	NodeInfo
	ParentScope   string
	ExploreScope  string
	MaxIterations int
	ToolIDs       []string
	SystemPrompt  string
	ToolResultCap int
}

func NewExploreNode() *ExploreNode {
	id := uuid.New()
	return &ExploreNode{
		NodeInfo: NodeInfo{
			NodeID:          "Explore_" + id.String(),
			NodeName:        "Explore",
			NodeDescription: "Run an isolated file-reading loop and return a structured summary.",
		},
		ExploreScope:  wfstate.StateKeyExplore,
		MaxIterations: defaultExploreMaxIterations,
		ToolIDs:       []string{"file_read", "grep", "glob"},
		ToolResultCap: defaultExploreToolResultCap,
	}
}

func (n *ExploreNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	svc := core.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("explore node: model service not available")
	}
	if state == nil {
		state = wfstate.State{}
	}

	request, err := n.resolveRequest(state)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.error", map[string]any{"error": err.Error()})
		return state, err
	}

	exploreScope := n.effectiveExploreScope()
	convo := state.Conversation(exploreScope)
	convo.SetMaxIterations(n.effectiveMaxIterations())
	convo.UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, n.effectiveSystemPrompt()),
		llms.TextParts(llms.ChatMessageTypeHuman, request),
	})

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
			return state, err
		}
		if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
			return state, errors.New("explore: model returned no choices")
		}
		if payload := buildLLMResponseArtifact(resp); len(payload.Choices) > 0 {
			_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "explore.response", payload)
		}

		choice := resp.Choices[0]
		_ = RecordChoiceUsage(ctx, state, Record{
			NodeID:     n.ID(),
			Model:      modelLabel(svc.Model),
			StateScope: exploreScope,
		}, choice)

		aiMessage := llms.MessageContent{Role: llms.ChatMessageTypeAI}
		if strings.TrimSpace(choice.Content) != "" {
			aiMessage.Parts = append(aiMessage.Parts, llms.TextPart(choice.Content))
		}
		for _, tc := range choice.ToolCalls {
			if tc.Type == "" {
				continue
			}
			aiMessage.Parts = append(aiMessage.Parts, tc)
		}
		convo.UpdateMessage(append(messages, aiMessage))
		convo.IncrementIteration()

		if len(choice.ToolCalls) == 0 {
			terminated = true
			break
		}

		// Execute tool calls; clamp each result before appending to sub-conversation.
		toolMessages := make([]llms.MessageContent, 0, len(choice.ToolCalls))
		for _, tc := range choice.ToolCalls {
			result := executeToolCallMessage(ctx, nodeTools, tc)
			toolMessages = append(toolMessages, n.clampToolMessage(result))
		}
		convo.UpdateMessage(append(convo.Messages(), toolMessages...))
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
		return state, err
	}

	n.writeAnswerToParent(state, summary)

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

	return state, nil
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

func (n *ExploreNode) writeAnswerToParent(state wfstate.State, summary string) {
	summary = strings.TrimSpace(summary)
	if summary == "" || state == nil {
		return
	}
	parent := state.Conversation(n.effectiveParentScope())
	messages := parent.Messages()
	if !lastExploreMessageMatches(messages, summary) {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeAI, summary))
		parent.UpdateMessage(messages)
	}
	parent.SetFinalAnswer(summary)
}

func (n *ExploreNode) resolveRequest(state wfstate.State) (string, error) {
	parentScope := n.effectiveParentScope()
	messages := state.Conversation(parentScope).Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		text := strings.TrimSpace(extractText(messages[i]))
		if text != "" {
			return text, nil
		}
	}

	if req := state.Get(wfstate.StateKeyRequest); req != nil {
		if input, _ := req["input"].(string); strings.TrimSpace(input) != "" {
			return strings.TrimSpace(input), nil
		}
	}
	return "", errors.New("explore: no user input found in parent scope or request state")
}

func (n *ExploreNode) effectiveExploreScope() string {
	if n == nil || strings.TrimSpace(n.ExploreScope) == "" {
		return wfstate.StateKeyExplore
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
		return []string{"file_read", "grep", "glob"}
	}
	out := make([]string, len(n.ToolIDs))
	copy(out, n.ToolIDs)
	return out
}

func (n *ExploreNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return n.execute(ctx, state)
	})
}

func (n *ExploreNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "explore",
		Description: n.Description(),
		Config: map[string]any{
			"parent_scope":    n.effectiveParentScope(),
			"explore_scope":   n.effectiveExploreScope(),
			"max_iterations":  n.effectiveMaxIterations(),
			"tool_result_cap": n.effectiveToolResultCap(),
			"tool_ids":        n.effectiveToolIDs(),
		},
	}
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
