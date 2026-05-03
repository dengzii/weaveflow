package neo

import (
	"context"
	"strings"
	"testing"

	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type countingModel struct {
	response string
	calls    int
}

func (m *countingModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	m.calls++
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{
				Content: m.response,
			},
		},
	}, nil
}

func (m *countingModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return m.response, nil
}

func TestNewGraphDirectAnswerShortCircuitsAfterRouter(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	graph, err := NewGraph(cfg)
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &countingModel{
		response: `{
  "mode": "direct",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": false,
  "clarification_question": "",
  "reasoning": "Simple arithmetic.",
  "target_subgraph": "",
  "direct_answer": "2 + 2 = 4."
}`,
	}
	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})

	state := NewInitialState("What is 2 + 2?", []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
	})
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	if model.calls != 1 {
		t.Fatalf("expected router direct-answer path to use exactly 1 model call, got %d", model.calls)
	}

	if got := state.Conversation(stateScope).FinalAnswer(); got != "2 + 2 = 4." {
		t.Fatalf("expected final answer to be preserved, got %#v", got)
	}

	final := state.Get(fruntime.StateKeyFinal)
	if got := final["answer"]; got != "2 + 2 = 4." {
		t.Fatalf("expected final state answer to be written, got %#v", got)
	}

	messages := state.Conversation(stateScope).Messages()
	if len(messages) == 0 {
		t.Fatal("expected conversation messages to be preserved")
	}
	if got := messageText(messages[len(messages)-1]); got != "2 + 2 = 4." {
		t.Fatalf("expected final assistant message to be appended, got %#v", got)
	}
}

func messageText(message llms.MessageContent) string {
	parts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		textPart, ok := part.(llms.TextContent)
		if !ok {
			continue
		}
		text := strings.TrimSpace(textPart.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}
