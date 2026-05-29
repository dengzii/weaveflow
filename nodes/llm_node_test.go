package nodes

import (
	"context"
	"strings"
	"testing"
	"weaveflow/core"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type captureLLMModel struct {
	lastMessages  []llms.MessageContent
	lastToolCount int
	response      string
}

func (m *captureLLMModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	m.lastMessages = cloneReducerMessages(messages)
	callOptions := &llms.CallOptions{}
	for _, option := range options {
		option(callOptions)
	}
	m.lastToolCount = len(callOptions.Tools)

	content := m.response
	if content == "" {
		content = "trimmed"
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: content},
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

	state := wfstate.State{}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are concise."),
		llms.TextParts(llms.ChatMessageTypeHuman, "the original task"),
		llms.TextParts(llms.ChatMessageTypeAI, "older answer with a long prefix that should be trimmed away"),
		llms.TextParts(llms.ChatMessageTypeHuman, "follow-up that should also be trimmed away"),
		llms.TextParts(llms.ChatMessageTypeAI, "another older answer with a long prefix that should be trimmed away"),
		llms.TextParts(llms.ChatMessageTypeHuman, "latest question"),
	})

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke llm node: %v", err)
	}

	if len(model.lastMessages) != 3 {
		t.Fatalf("expected prompt trim to keep system + pinned first human + latest message, got %#v", model.lastMessages)
	}
	if model.lastMessages[0].Role != llms.ChatMessageTypeSystem || extractText(model.lastMessages[0]) != "You are concise." {
		t.Fatalf("unexpected preserved system message: %#v", model.lastMessages[0])
	}
	if model.lastMessages[1].Role != llms.ChatMessageTypeHuman || extractText(model.lastMessages[1]) != "the original task" {
		t.Fatalf("expected first human to be pinned to prefix, got: %#v", model.lastMessages[1])
	}
	if model.lastMessages[2].Role != llms.ChatMessageTypeHuman || extractText(model.lastMessages[2]) != "latest question" {
		t.Fatalf("unexpected preserved latest message: %#v", model.lastMessages[2])
	}

	messages := next.Conversation("agent").Messages()
	if len(messages) != 7 {
		t.Fatalf("expected full conversation state to append response without destructive trim, got %d messages", len(messages))
	}
	if got := extractText(messages[len(messages)-1]); got != "trimmed" {
		t.Fatalf("unexpected assistant reply: %q", got)
	}
}

func TestTrimLLMPromptMessagesPinsFirstHumanAcrossLargeToolResults(t *testing.T) {
	t.Parallel()

	bigToolResult := strings.Repeat("X", 1000)
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "you are an agent"),
		llms.TextParts(llms.ChatMessageTypeHuman, "explore /repo and summarize"),
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_1", "read", `{"path":"README.md"}`),
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{ToolCallID: "call_1", Name: "read", Content: bigToolResult},
			},
		},
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_2", "read", `{"path":"main.go"}`),
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{ToolCallID: "call_2", Name: "read", Content: bigToolResult},
			},
		},
	}

	trimmed := trimLLMPromptMessages(messages, 1500)

	if len(trimmed) == 0 {
		t.Fatalf("expected at least one message after trim")
	}
	if trimmed[0].Role != llms.ChatMessageTypeSystem {
		t.Fatalf("expected leading system to survive, got %s", trimmed[0].Role)
	}

	foundFirstHuman := false
	for _, msg := range trimmed {
		if msg.Role == llms.ChatMessageTypeHuman && extractText(msg) == "explore /repo and summarize" {
			foundFirstHuman = true
			break
		}
	}
	if !foundFirstHuman {
		t.Fatalf("expected the original human task to be pinned in trimmed prompt, got %#v", trimmed)
	}

	humanCount := 0
	for _, msg := range trimmed {
		if msg.Role == llms.ChatMessageTypeHuman {
			humanCount++
		}
	}
	if humanCount != 1 {
		t.Fatalf("expected pinned human to appear exactly once (no duplication into tail), got %d", humanCount)
	}
}

func TestTrimLLMPromptMessagesWithoutHumanLeavesPrefixIntact(t *testing.T) {
	t.Parallel()

	bigText := strings.Repeat("Y", 1000)
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "you are an agent"),
		llms.TextParts(llms.ChatMessageTypeAI, bigText),
		llms.TextParts(llms.ChatMessageTypeAI, bigText),
		llms.TextParts(llms.ChatMessageTypeAI, "tail"),
	}

	trimmed := trimLLMPromptMessages(messages, 1500)

	if len(trimmed) == 0 {
		t.Fatalf("expected at least one message after trim")
	}
	if trimmed[0].Role != llms.ChatMessageTypeSystem || extractText(trimmed[0]) != "you are an agent" {
		t.Fatalf("expected leading system unchanged, got %#v", trimmed[0])
	}
	for _, msg := range trimmed {
		if msg.Role == llms.ChatMessageTypeHuman {
			t.Fatalf("did not expect to synthesize a human message: %#v", trimmed)
		}
	}
	if extractText(trimmed[len(trimmed)-1]) != "tail" {
		t.Fatalf("expected the most recent AI message to be kept, got %q", extractText(trimmed[len(trimmed)-1]))
	}
}

func TestLLMNodePlannerKeepsToolsBeforeToolResult(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{response: "ready"}
	node := NewLLMNode()
	node.StateScope = "agent"
	node.ToolIDs = []string{"echo"}

	state := wfstate.State{}
	state.Ensure(wfstate.StateKeyOrchestration)["mode"] = "planner"
	state.Ensure(wfstate.StateKeyExecution)["route"] = ExecutionRouteLLMWithTools
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "run the step"),
	})

	ctx := core.WithServices(context.Background(), &core.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"echo": testTool("echo", func(_ context.Context, input string) (string, error) {
				return "echo:" + input, nil
			}),
		},
	})
	if _, err := runTestNode(t, node, ctx, state); err != nil {
		t.Fatalf("invoke llm node: %v", err)
	}

	if model.lastToolCount != 1 {
		t.Fatalf("expected planner step to expose tools before any tool result, got %d", model.lastToolCount)
	}
	if promptContains(model.lastMessages, "Do not call more tools") {
		t.Fatalf("did not expect synthesis prompt before tool result: %#v", model.lastMessages)
	}
}

func TestLLMNodePlannerSynthesizesAfterToolResultWithoutTools(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{response: "step result uses echo:hi"}
	node := NewLLMNode()
	node.StateScope = "agent"
	node.ToolIDs = []string{"echo"}

	state := wfstate.State{}
	state.Ensure(wfstate.StateKeyOrchestration)["mode"] = "planner"
	state.Ensure(wfstate.StateKeyExecution)["route"] = ExecutionRouteLLMWithTools
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "run the step"),
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				testToolCall("call_1", "echo", `{"input":"hi"}`),
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_1",
					Name:       "echo",
					Content:    "echo:hi",
				},
			},
		},
	})

	ctx := core.WithServices(context.Background(), &core.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"echo": testTool("echo", func(_ context.Context, input string) (string, error) {
				return "echo:" + input, nil
			}),
		},
	})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke llm node: %v", err)
	}

	if model.lastToolCount != 0 {
		t.Fatalf("expected planner synthesis turn to disable tools, got %d", model.lastToolCount)
	}
	if !promptContains(model.lastMessages, "Do not call more tools") {
		t.Fatalf("expected synthesis prompt after tool result: %#v", model.lastMessages)
	}
	if got := next.Conversation("agent").FinalAnswer(); got != "step result uses echo:hi" {
		t.Fatalf("unexpected final answer: %q", got)
	}
}

func promptContains(messages []llms.MessageContent, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(extractText(message), fragment) {
			return true
		}
	}
	return false
}

// TestLLMNodeScrubsPriorStepWhenCurrentStepIsStateType guards against
// a regression where the scrub-on-step-boundary logic skipped its work
// because exec["current_step"] arrived as wfstate.State (the named type)
// after a patch round-trip rather than map[string]any, causing the
// type assertion to fail silently and iteration_count to grow forever
// across step boundaries.
func TestLLMNodeScrubsPriorStepWhenCurrentStepIsStateType(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{response: "ack"}
	node := NewLLMNode()
	node.StateScope = "agent"

	state := wfstate.State{}
	exec := state.Ensure(wfstate.StateKeyExecution)
	// Simulate the post-patch round-trip shape: current_step is a
	// wfstate.State, not a bare map[string]any.
	exec["current_step"] = wfstate.State{"id": "step_new", "title": "New step"}
	exec["last_llm_step_id"] = "step_old"

	conv := state.Conversation("agent")
	conv.UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "system prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "original question"),
		llms.TextParts(llms.ChatMessageTypeAI, "step_old answer that must be scrubbed"),
	})
	for i := 0; i < 5; i++ {
		conv.IncrementIteration()
	}

	ctx := core.WithServices(context.Background(), &core.Services{Model: model})
	next, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke llm node: %v", err)
	}

	// After scrub + the model's own reply, the conversation should contain
	// system + human + new AI reply only — no trace of the prior step's AI msg.
	messages := next.Conversation("agent").Messages()
	for _, msg := range messages {
		if msg.Role == llms.ChatMessageTypeAI && strings.Contains(extractText(msg), "step_old answer that must be scrubbed") {
			t.Fatalf("expected prior step AI message to be scrubbed, still present in %#v", messages)
		}
	}

	// Iteration count must have been reset to 0 by the scrub, then incremented
	// to 1 by the LLM call itself.
	if got := next.Conversation("agent").IterationCount(); got != 1 {
		t.Fatalf("expected iteration_count to be reset and re-incremented to 1, got %d", got)
	}

	// last_llm_step_id must have advanced to the new step.
	nextExec := next.Get(wfstate.StateKeyExecution)
	if got, _ := nextExec["last_llm_step_id"].(string); got != "step_new" {
		t.Fatalf("expected last_llm_step_id to advance to step_new, got %q", got)
	}
}
