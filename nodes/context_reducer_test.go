package nodes

import (
	"context"
	"strings"
	"testing"

	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type stubReducerModel struct {
	responses    []string
	callCount    int
	lastMessages []llms.MessageContent
}

func (m *stubReducerModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	m.callCount++
	m.lastMessages = cloneReducerMessages(messages)

	content := ""
	if len(m.responses) > 0 {
		content = m.responses[0]
		m.responses = m.responses[1:]
	}

	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: content},
		},
	}, nil
}

func (m *stubReducerModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", nil
}

func TestContextReducerNodeNoOpBelowLimit(t *testing.T) {
	t.Parallel()

	model := &stubReducerModel{}
	node := NewContextReducerNode()
	node.StateScope = "agent"
	node.MaxMessages = 4
	node.PreserveRecent = 2

	state := fruntime.State{}
	fruntime.Conversation(state, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
		llms.TextParts(llms.ChatMessageTypeHuman, "hello"),
		llms.TextParts(llms.ChatMessageTypeAI, "hi"),
	})

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})
	_, err := node.Invoke(ctx, state)
	if err != nil {
		t.Fatalf("invoke context reducer: %v", err)
	}
	if model.callCount != 0 {
		t.Fatalf("expected reducer model to not be called, got %d", model.callCount)
	}
}

func TestContextReducerNodeSummarizesOlderMessages(t *testing.T) {
	t.Parallel()

	model := &stubReducerModel{responses: []string{"- User asked about deployment\n- Assistant suggested blue-green rollout"}}
	node := NewContextReducerNode()
	node.StateScope = "agent"
	node.MaxMessages = 5
	node.PreserveRecent = 2
	node.SummaryPrefix = "Earlier:"

	state := fruntime.State{}
	fruntime.Conversation(state, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Need a safe deployment plan."),
		llms.TextParts(llms.ChatMessageTypeAI, "We can evaluate rollout strategies."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Downtime must stay near zero."),
		llms.TextParts(llms.ChatMessageTypeAI, "Blue-green is a good fit."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Proceed with the final recommendation."),
	})

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})
	_, err := node.Invoke(ctx, state)
	if err != nil {
		t.Fatalf("invoke context reducer: %v", err)
	}
	if model.callCount != 1 {
		t.Fatalf("expected reducer model call count 1, got %d", model.callCount)
	}

	reduced := fruntime.Conversation(state, "agent").Messages()
	if len(reduced) != 4 {
		t.Fatalf("expected 4 messages after reduction, got %d", len(reduced))
	}
	if reduced[0].Role != llms.ChatMessageTypeSystem {
		t.Fatalf("expected first message to remain system, got %q", reduced[0].Role)
	}
	if reduced[1].Role != llms.ChatMessageTypeSystem {
		t.Fatalf("expected summary message to be system, got %q", reduced[1].Role)
	}
	summary := extractText(reduced[1])
	if got, want := summary, "Earlier:\n- User asked about deployment\n- Assistant suggested blue-green rollout"; got != want {
		t.Fatalf("unexpected summary message: %q", got)
	}
	if got := extractText(reduced[2]); got != "Blue-green is a good fit." {
		t.Fatalf("expected recent AI message to be preserved, got %q", got)
	}
	if got := extractText(reduced[3]); got != "Proceed with the final recommendation." {
		t.Fatalf("expected latest human message to be preserved, got %q", got)
	}

	if len(model.lastMessages) != 2 {
		t.Fatalf("expected reducer prompt to contain system+human prompt, got %d messages", len(model.lastMessages))
	}
	if got := extractText(model.lastMessages[1]); got == "" || !containsAll(got, "Need a safe deployment plan.", "Downtime must stay near zero.") {
		t.Fatalf("expected reducer prompt to contain earlier context, got %q", got)
	}
}

func TestContextReducerNodeKeepsToolSpanTogether(t *testing.T) {
	t.Parallel()

	model := &stubReducerModel{responses: []string{"- Earlier request captured"}}
	node := NewContextReducerNode()
	node.StateScope = "agent"
	node.MaxMessages = 5
	node.PreserveRecent = 2

	state := fruntime.State{}
	fruntime.Conversation(state, "agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Check the server status."),
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				llms.ToolCall{
					ID:   "call_1",
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      "current_time",
						Arguments: `{"input":"now"}`,
					},
				},
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_1",
					Name:       "current_time",
					Content:    "2026-04-08T10:00:00Z",
				},
			},
		},
		llms.TextParts(llms.ChatMessageTypeAI, "The server is up and responding."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Summarize it."),
	})

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})
	_, err := node.Invoke(ctx, state)
	if err != nil {
		t.Fatalf("invoke context reducer: %v", err)
	}

	reduced := fruntime.Conversation(state, "agent").Messages()
	if len(reduced) != 6 {
		t.Fatalf("expected tool span preservation to keep 6 messages, got %d", len(reduced))
	}
	if reduced[2].Role != llms.ChatMessageTypeAI || !messageHasToolCalls(reduced[2]) {
		t.Fatalf("expected AI tool call message to be preserved, got %#v", reduced[2])
	}
	if reduced[3].Role != llms.ChatMessageTypeTool {
		t.Fatalf("expected tool response to be preserved with tool call, got %#v", reduced[3])
	}
}

func containsAll(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			return false
		}
	}
	return true
}
