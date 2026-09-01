package node

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestContextReducerCompactsOlderMessages(t *testing.T) {
	t.Parallel()

	conversationPath := state.Scope("reducer", "conversation")
	access := state.NewEditingAccess(state.NewState())
	conversation, err := conversationcap.Bind(access, conversationPath)
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "follow project rules"),
		llms.TextParts(llms.ChatMessageTypeHuman, "old question"),
		llms.TextParts(llms.ChatMessageTypeAI, "old answer"),
		llms.TextParts(llms.ChatMessageTypeHuman, "recent question"),
		llms.TextParts(llms.ChatMessageTypeAI, "recent answer"),
	}
	if err := conversation.SetMessages(messages); err != nil {
		t.Fatalf("set messages: %v", err)
	}

	model := &scriptedModel{responses: []*llms.ModelResponse{{Choices: []*llms.ModelChoice{{Content: "- retained fact"}}}}}
	target := NewContextReducerNode(WithID("reducer"))
	target.ConversationPath = conversationPath
	target.MaxMessages = 4
	target.PreserveRecent = 2
	target.SummaryPrefix = "Earlier context:"

	result, err := Execute(core.WithModel(context.Background(), model), access.State(), target)
	if err != nil {
		t.Fatalf("execute context reducer: %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	if request.Mode != llms.ModelModeChat || request.Temperature == nil || *request.Temperature != 0 {
		t.Fatalf("model request = %#v", request)
	}
	transcript := ExtractText(request.Messages[1])
	if !strings.Contains(transcript, "human: old question") || !strings.Contains(transcript, "assistant: old answer") {
		t.Fatalf("summary transcript = %q", transcript)
	}
	if strings.Contains(transcript, "recent question") {
		t.Fatalf("summary transcript included preserved messages: %q", transcript)
	}

	restored, err := conversationcap.Bind(state.NewAccess(result.State), conversationPath)
	if err != nil {
		t.Fatalf("bind reduced conversation: %v", err)
	}
	reduced := restored.Messages()
	if len(reduced) != 4 {
		t.Fatalf("reduced messages = %d, want 4", len(reduced))
	}
	if reduced[0].Role != llms.ChatMessageTypeSystem || ExtractText(reduced[0]) != "follow project rules" {
		t.Fatalf("preserved system message = %#v", reduced[0])
	}
	if reduced[1].Role != llms.ChatMessageTypeSystem || ExtractText(reduced[1]) != "Earlier context:\n- retained fact" {
		t.Fatalf("summary message = %#v", reduced[1])
	}
	if ExtractText(reduced[2]) != "recent question" || ExtractText(reduced[3]) != "recent answer" {
		t.Fatalf("preserved recent messages = %#v", reduced[2:])
	}
}

func TestContextReducerSkipsConversationWithinLimit(t *testing.T) {
	t.Parallel()

	conversationPath := state.Scope("reducer", "conversation")
	access := state.NewEditingAccess(state.NewState())
	conversation, err := conversationcap.Bind(access, conversationPath)
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "question"),
		llms.TextParts(llms.ChatMessageTypeAI, "answer"),
	}
	if err := conversation.SetMessages(messages); err != nil {
		t.Fatalf("set messages: %v", err)
	}

	model := &scriptedModel{}
	target := NewContextReducerNode(WithID("reducer"))
	target.ConversationPath = conversationPath
	target.MaxMessages = len(messages)
	result, err := Execute(core.WithModel(context.Background(), model), access.State(), target)
	if err != nil {
		t.Fatalf("execute context reducer: %v", err)
	}
	if len(model.requests) != 0 {
		t.Fatalf("model requests = %d, want 0", len(model.requests))
	}
	restored, err := conversationcap.Bind(state.NewAccess(result.State), conversationPath)
	if err != nil {
		t.Fatalf("bind restored conversation: %v", err)
	}
	if got := restored.Messages(); len(got) != 2 || ExtractText(got[0]) != "question" || ExtractText(got[1]) != "answer" {
		t.Fatalf("messages changed = %#v", got)
	}
}

func TestContextReducerReportsModelFailures(t *testing.T) {
	t.Parallel()

	conversationPath := state.Scope("reducer", "conversation")
	newState := func(t *testing.T) *state.State {
		t.Helper()
		access := state.NewEditingAccess(state.NewState())
		conversation, err := conversationcap.Bind(access, conversationPath)
		if err != nil {
			t.Fatalf("bind conversation: %v", err)
		}
		if err := conversation.SetMessages([]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "first"),
			llms.TextParts(llms.ChatMessageTypeAI, "second"),
			llms.TextParts(llms.ChatMessageTypeHuman, "third"),
		}); err != nil {
			t.Fatalf("set messages: %v", err)
		}
		return access.State()
	}

	tests := []struct {
		name     string
		response *llms.ModelResponse
		want     string
	}{
		{name: "no choices", response: &llms.ModelResponse{}, want: "returned no choices"},
		{name: "nil choice", response: &llms.ModelResponse{Choices: []*llms.ModelChoice{nil}}, want: "returned no choices"},
		{name: "empty summary", response: &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "  "}}}, want: "returned empty summary"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := NewContextReducerNode(WithID("reducer"))
			target.ConversationPath = conversationPath
			target.MaxMessages = 2
			target.PreserveRecent = 1
			model := &scriptedModel{responses: []*llms.ModelResponse{test.response}}
			_, err := Execute(core.WithModel(context.Background(), model), newState(t), target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("execute error = %v, want %q", err, test.want)
			}
		})
	}

	target := NewContextReducerNode(WithID("reducer"))
	target.ConversationPath = conversationPath
	target.MaxMessages = 2
	if _, err := Execute(context.Background(), newState(t), target); err == nil || !strings.Contains(err.Error(), "model service not available") {
		t.Fatalf("missing model error = %v", err)
	}
}

func TestContextReducerDefinitionBuildsConfiguredNode(t *testing.T) {
	t.Parallel()

	target := NewContextReducerNode(WithID("reduce.history"))
	if err := target.Validate(); err != nil {
		t.Fatalf("validate default node: %v", err)
	}
	if got := target.ConversationPath.String(); got != "scopes.reduce_history.conversation" {
		t.Fatalf("default conversation path = %q", got)
	}
	spec := target.GraphNodeSpec()
	if spec.Type != NodeTypeContextReducer || spec.State["conversation"].Path != target.ConversationPath.String() {
		t.Fatalf("graph node spec = %#v", spec)
	}
	if got := spec.Config["max_messages"]; got != defaultContextReducerMaxMessages {
		t.Fatalf("max_messages config = %#v", got)
	}

	conversationPath := state.Scope("shared_reducer", "conversation")
	built, err := ContextReducerNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{
			ID:   "configured",
			Name: "Configured reducer",
			Config: map[string]any{
				"max_messages":    10,
				"preserve_system": false,
				"preserve_recent": 3,
				"summary_prefix":  "History:",
			},
		},
		State: map[string]registry.ResolvedStateBinding{
			"conversation": {Path: conversationPath},
		},
	})
	if err != nil {
		t.Fatalf("build context reducer: %v", err)
	}
	reducer := built.(*ContextReducerNode)
	if reducer.Name() != "Configured reducer" || reducer.ConversationPath.String() != conversationPath.String() {
		t.Fatalf("built reducer metadata = %#v", reducer)
	}
	if reducer.MaxMessages != 10 || reducer.PreserveSystem || reducer.PreserveRecent != 3 || reducer.SummaryPrefix != "History:" {
		t.Fatalf("built reducer config = %#v", reducer)
	}

	var nilReducer *ContextReducerNode
	if err := nilReducer.Validate(); err == nil || !strings.Contains(err.Error(), "is nil") {
		t.Fatalf("nil validation error = %v", err)
	}
	target.ConversationPath = state.Path{}
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "conversation path is required") {
		t.Fatalf("missing path validation error = %v", err)
	}
}

func TestContextReducerMessageHelpersKeepToolExchangeTogether(t *testing.T) {
	t.Parallel()

	summaryPrefix := "Summary:"
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "rules"),
		llms.TextParts(llms.ChatMessageTypeSystem, summaryPrefix+" old"),
		llms.TextParts(llms.ChatMessageTypeHuman, "inspect"),
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ReasoningPart("need a tool"),
			llms.ToolCall{ID: "call-1", FunctionCall: &llms.FunctionCall{Name: "read", Arguments: json.RawMessage(`{"path":"README.md"}`)}},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolResult{Name: "read", Content: "project details", IsError: true, ErrorMessage: "partial"},
		}},
		llms.TextParts(llms.ChatMessageTypeAI, "done"),
	}
	preserved, body := splitReducerMessages(messages, true, summaryPrefix)
	if len(preserved) != 1 || ExtractText(preserved[0]) != "rules" || len(body) != len(messages)-1 {
		t.Fatalf("split messages preserved=%#v body=%#v", preserved, body)
	}
	withoutPreserved, cloned := splitReducerMessages(messages, false, summaryPrefix)
	if withoutPreserved != nil || len(cloned) != len(messages) {
		t.Fatalf("unpreserved split = %#v, %#v", withoutPreserved, cloned)
	}
	cloned[0].Role = llms.ChatMessageTypeHuman
	if messages[0].Role != llms.ChatMessageTypeSystem {
		t.Fatal("split reducer messages mutated the input slice")
	}

	if got := adjustReducerTailStart(messages, 5); got != 3 {
		t.Fatalf("adjusted tool exchange start = %d, want 3", got)
	}
	transcript := buildReducerTranscript(messages[2:])
	for _, fragment := range []string{
		"human: inspect",
		"assistant: need a tool",
		`tool_call read {"path":"README.md"}`,
		"tool: tool_result read project details error=partial",
		"assistant: done",
	} {
		if !strings.Contains(transcript, fragment) {
			t.Fatalf("transcript %q does not contain %q", transcript, fragment)
		}
	}
}

func TestTrimLLMPromptMessagesPreservesPinnedContext(t *testing.T) {
	t.Parallel()

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "rules"),
		llms.TextParts(llms.ChatMessageTypeHuman, "original goal"),
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{ID: "call-1", FunctionCall: &llms.FunctionCall{Name: "search", Arguments: json.RawMessage(`{"query":"details"}`)}},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolResult{Name: "search", Content: strings.Repeat("result", 20)},
		}},
		llms.TextParts(llms.ChatMessageTypeAI, "latest answer"),
	}
	maxChars := promptMessageCharCount(messages[0]) + promptMessageCharCount(messages[1]) + promptMessageCharCount(messages[4])
	trimmed := TrimLLMPromptMessages(messages, maxChars)
	if len(trimmed) != 3 {
		t.Fatalf("trimmed messages = %#v", trimmed)
	}
	if trimmed[0].Role != llms.ChatMessageTypeSystem || ExtractText(trimmed[0]) != "rules" {
		t.Fatalf("trimmed system message = %#v", trimmed[0])
	}
	if trimmed[1].Role != llms.ChatMessageTypeHuman || ExtractText(trimmed[1]) != "original goal" {
		t.Fatalf("trimmed pinned human message = %#v", trimmed[1])
	}
	if trimmed[2].Role != llms.ChatMessageTypeAI || ExtractText(trimmed[2]) != "latest answer" {
		t.Fatalf("trimmed recent message = %#v", trimmed[2])
	}
	if got := promptMessagesCharCount(trimmed); got > maxChars {
		t.Fatalf("trimmed prompt chars = %d, limit %d", got, maxChars)
	}

	unchanged := TrimLLMPromptMessages(messages, 0)
	unchanged[0].Role = llms.ChatMessageTypeHuman
	if messages[0].Role != llms.ChatMessageTypeSystem {
		t.Fatal("trim prompt messages mutated the input slice")
	}
}

func TestContextReducerEffectiveDefaults(t *testing.T) {
	t.Parallel()

	var target *ContextReducerNode
	if got := target.effectiveMaxMessages(); got != defaultContextReducerMaxMessages {
		t.Fatalf("nil max messages = %d", got)
	}
	if got := target.effectivePreserveRecent(); got != defaultContextReducerPreserveTail {
		t.Fatalf("nil preserve recent = %d", got)
	}
	if got := target.effectiveSummaryPrefix(); got != defaultContextReducerSummaryLabel {
		t.Fatalf("nil summary prefix = %q", got)
	}

	target = &ContextReducerNode{MaxMessages: -1, PreserveRecent: -1, SummaryPrefix: "  "}
	if got := target.renderSummary("  "); got != defaultContextReducerSummaryLabel {
		t.Fatalf("empty rendered summary = %q", got)
	}
	if got := target.renderSummary(" facts "); got != defaultContextReducerSummaryLabel+"\nfacts" {
		t.Fatalf("rendered summary = %q", got)
	}
}
