package nodes

import (
	"context"
	"testing"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

type captureLLMModel struct {
	lastMessages []llms.MessageContent
}

func (m *captureLLMModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	m.lastMessages = cloneReducerMessages(messages)
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: "trimmed"},
		},
	}, nil
}

func (m *captureLLMModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", nil
}

func TestLLMNodeTrimsPromptToRecentMessages(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{}
	node := NewLLMNode()
	node.StateScope = "agent"
	node.PromptMaxChars = 120

	state := fruntime.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are concise."),
		llms.TextParts(llms.ChatMessageTypeHuman, "older question with a long prefix that should be trimmed away"),
		llms.TextParts(llms.ChatMessageTypeAI, "older answer with a long prefix that should be trimmed away"),
		llms.TextParts(llms.ChatMessageTypeHuman, "latest question"),
	})

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})
	_, err := node.Invoke(ctx, state)
	if err != nil {
		t.Fatalf("invoke llm node: %v", err)
	}

	if len(model.lastMessages) != 2 {
		t.Fatalf("expected prompt trim to keep system plus latest message, got %#v", model.lastMessages)
	}
	if model.lastMessages[0].Role != llms.ChatMessageTypeSystem || extractText(model.lastMessages[0]) != "You are concise." {
		t.Fatalf("unexpected preserved system message: %#v", model.lastMessages[0])
	}
	if model.lastMessages[1].Role != llms.ChatMessageTypeHuman || extractText(model.lastMessages[1]) != "latest question" {
		t.Fatalf("unexpected preserved latest message: %#v", model.lastMessages[1])
	}

	messages := state.Conversation("agent").Messages()
	if len(messages) != 5 {
		t.Fatalf("expected full conversation state to append response without destructive trim, got %d messages", len(messages))
	}
	if got := extractText(messages[len(messages)-1]); got != "trimmed" {
		t.Fatalf("unexpected assistant reply: %q", got)
	}
}
