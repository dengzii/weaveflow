package nodes

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"weaveflow/core"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

// scriptedExploreModel returns a different response each call: tool-call on
// iter 1, plain text on iter 2, then a final summary on the summarizer call.
type scriptedExploreModel struct {
	mu        sync.Mutex
	calls     int
	responses []*llms.ContentResponse
	prompts   [][]llms.MessageContent
}

func (m *scriptedExploreModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := make([]llms.MessageContent, len(messages))
	for i, msg := range messages {
		clone[i] = llms.MessageContent{Role: msg.Role, Parts: append([]llms.ContentPart(nil), msg.Parts...)}
	}
	m.prompts = append(m.prompts, clone)
	if m.calls >= len(m.responses) {
		m.calls++
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "(no more scripted responses)"}}}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *scriptedExploreModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", nil
}

func fakeExploreTool(name, output string) tools.Tool {
	return tools.Tool{
		Function: &llms.FunctionDefinition{
			Name:        name,
			Description: "fake tool used in explore tests",
			Parameters:  map[string]any{"type": "object"},
		},
		Handler: func(_ context.Context, _ string) (string, error) {
			return output, nil
		},
	}
}

func TestExploreNodeIsolatesContextFromParent(t *testing.T) {
	const summary = "## Direct answer\nThe answer.\n## Key files\n- foo.go — entry"

	toolCall := llms.ToolCall{
		ID:   "tc-1",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      "grep",
			Arguments: `{"pattern":"x"}`,
		},
	}

	model := &scriptedExploreModel{
		responses: []*llms.ContentResponse{
			// iter 1: AI emits one grep ToolCall
			{Choices: []*llms.ContentChoice{{ToolCalls: []llms.ToolCall{toolCall}}}},
			// iter 2: AI emits plain text (no tool calls) → terminates loop
			{Choices: []*llms.ContentChoice{{Content: "ok, I have what I need"}}},
			// summarizer call
			{Choices: []*llms.ContentChoice{{Content: summary}}},
		},
	}

	services := &core.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"grep": fakeExploreTool("grep", `{"matches":[{"path":"foo.go","line":1}]}`),
		},
	}
	ctx := core.WithServices(context.Background(), services)

	node := NewExploreNode()
	node.ParentScope = "agent"
	node.ToolIDs = []string{"grep"}
	node.MaxIterations = 5

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "where is X handled?"),
	})

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("explore node returned error: %v", err)
	}

	// Parent scope must NOT contain any tool messages.
	parentMessages := next.Conversation("agent").Messages()
	for _, msg := range parentMessages {
		if msg.Role == llms.ChatMessageTypeTool {
			t.Errorf("parent scope leaked a tool message: %+v", msg)
		}
		for _, part := range msg.Parts {
			if _, ok := part.(llms.ToolCall); ok {
				t.Errorf("parent scope leaked a ToolCall part: %+v", part)
			}
			if _, ok := part.(llms.ToolCallResponse); ok {
				t.Errorf("parent scope leaked a ToolCallResponse part: %+v", part)
			}
		}
	}

	// Parent's final answer must be exactly the summarizer output.
	if got := next.Conversation("agent").FinalAnswer(); got != summary {
		t.Errorf("parent FinalAnswer mismatch:\n got %q\nwant %q", got, summary)
	}

	// Explore scope must contain the tool call/response pair.
	exploreMessages := next.Conversation("explore").Messages()
	if len(exploreMessages) == 0 {
		t.Fatalf("explore scope is empty")
	}
	sawToolCall := false
	sawToolResp := false
	for _, msg := range exploreMessages {
		for _, part := range msg.Parts {
			if _, ok := part.(llms.ToolCall); ok {
				sawToolCall = true
			}
			if _, ok := part.(llms.ToolCallResponse); ok {
				sawToolResp = true
			}
		}
	}
	if !sawToolCall {
		t.Errorf("explore scope did not record a ToolCall")
	}
	if !sawToolResp {
		t.Errorf("explore scope did not record a ToolCallResponse")
	}

	// Explore scope should have incremented iterations.
	if got := next.Conversation("explore").IterationCount(); got < 1 {
		t.Errorf("explore IterationCount = %d, want >= 1", got)
	}
}

func TestExploreNodeClampsLargeToolResults(t *testing.T) {
	big := strings.Repeat("A", 9000)
	model := &scriptedExploreModel{
		responses: []*llms.ContentResponse{
			{Choices: []*llms.ContentChoice{{
				ToolCalls: []llms.ToolCall{{
					ID:           "tc-1",
					Type:         "function",
					FunctionCall: &llms.FunctionCall{Name: "grep", Arguments: "{}"},
				}},
			}}},
			{Choices: []*llms.ContentChoice{{Content: "done"}}},
			{Choices: []*llms.ContentChoice{{Content: "summary"}}},
		},
	}
	services := &core.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"grep": fakeExploreTool("grep", big),
		},
	}
	ctx := core.WithServices(context.Background(), services)

	node := NewExploreNode()
	node.ParentScope = "agent"
	node.ToolIDs = []string{"grep"}
	node.ToolResultCap = 512
	node.MaxIterations = 3

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "scan something"),
	})

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("explore node returned error: %v", err)
	}

	// Find the tool response in explore scope and assert it was clamped.
	exploreMessages := next.Conversation("explore").Messages()
	found := false
	for _, msg := range exploreMessages {
		for _, part := range msg.Parts {
			resp, ok := part.(llms.ToolCallResponse)
			if !ok {
				continue
			}
			found = true
			if len(resp.Content) > 512+128 { // cap + truncation suffix tolerance
				t.Errorf("tool response not clamped: len=%d", len(resp.Content))
			}
			if !strings.Contains(resp.Content, "[truncated:") {
				t.Errorf("clamped response missing truncation marker: %q", resp.Content)
			}
		}
	}
	if !found {
		t.Fatalf("did not find a ToolCallResponse in explore scope")
	}
}

func TestExploreNodeWritesFinalAnswerToParent(t *testing.T) {
	model := &scriptedExploreModel{
		responses: []*llms.ContentResponse{
			// iter 1: no tool calls — terminates immediately
			{Choices: []*llms.ContentChoice{{Content: "I have enough."}}},
			// summarizer
			{Choices: []*llms.ContentChoice{{Content: "FINAL"}}},
		},
	}
	ctx := core.WithServices(context.Background(), &core.Services{Model: model})

	node := NewExploreNode()
	node.ParentScope = "agent"
	node.ToolIDs = nil
	node.MaxIterations = 3

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "what does this project do?"),
	})

	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("explore node returned error: %v", err)
	}

	if got := next.Conversation("agent").FinalAnswer(); got != "FINAL" {
		t.Errorf("parent FinalAnswer = %q, want FINAL", got)
	}

	// Parent should also see a trailing AI message with the same text.
	parentMessages := next.Conversation("agent").Messages()
	lastText := ""
	if len(parentMessages) > 0 {
		lastText = extractText(parentMessages[len(parentMessages)-1])
	}
	if lastText != "FINAL" {
		t.Errorf("parent last message = %q, want FINAL", lastText)
	}
}

// Sanity check: the summarizer is given a non-empty transcript.
func TestExploreSummarizerSeesTranscript(t *testing.T) {
	model := &scriptedExploreModel{
		responses: []*llms.ContentResponse{
			{Choices: []*llms.ContentChoice{{Content: "done"}}},
			{Choices: []*llms.ContentChoice{{Content: "summary"}}},
		},
	}
	ctx := core.WithServices(context.Background(), &core.Services{Model: model})

	node := NewExploreNode()
	node.ParentScope = "agent"

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "audit it"),
	})

	if _, err := runTestNode(t, node, ctx, state); err != nil {
		t.Fatalf("explore: %v", err)
	}

	if len(model.prompts) < 2 {
		t.Fatalf("expected at least 2 model calls (loop + summarizer), got %d", len(model.prompts))
	}
	summarizerPrompt := model.prompts[len(model.prompts)-1]
	body, _ := json.Marshal(summarizerPrompt)
	if !strings.Contains(string(body), "Summarize this codebase exploration") {
		t.Errorf("summarizer was not invoked: prompt body = %s", body)
	}
}
