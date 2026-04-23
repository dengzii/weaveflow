package weaveflow

import (
	"context"
	"strings"
	"testing"
	wfmemory "weaveflow/memory"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

func TestContextAssemblerRegisteredInDefaultRegistry(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if _, ok := registry.NodeTypes["context_assembler"]; !ok {
		t.Fatal("expected context_assembler node type to be registered")
	}
}

func TestContextAssemblerInjectsRecalledMemoryIntoConversation(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint:  "assemble",
		FinishPoint: "assemble",
		Nodes: []GraphNodeSpec{
			{
				ID:   "assemble",
				Type: "context_assembler",
				Config: map[string]any{
					"state_scope": "chat",
				},
			},
		},
	}

	graph, err := registry.BuildGraph(def, &BuildContext{})
	if err != nil {
		t.Fatalf("build context assembler graph: %v", err)
	}

	state := State{}
	fruntime.Conversation(state, "chat").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a careful agent."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Use prior context if available."),
	})
	fruntime.EnsureMemory(state)["recalled"] = []map[string]any{
		{
			"id":   "m1",
			"text": "The user prefers weekday deployments.",
			"role": "assistant",
			"type": string(wfmemory.EntryTypeFact),
		},
	}

	state, err = graph.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("run context assembler graph: %v", err)
	}

	messages := fruntime.Conversation(state, "chat").Messages()
	if len(messages) != 3 {
		t.Fatalf("expected injected memory message, got %#v", messages)
	}
	injected := messages[1]
	if injected.Role != llms.ChatMessageTypeSystem {
		t.Fatalf("expected injected system message, got %#v", injected.Role)
	}
	if text := testMessageText(injected); text == "" || !strings.HasPrefix(text, "Relevant recalled memory:") {
		t.Fatalf("expected injected memory heading, got %q", text)
	}
}

func TestContextAssemblerReplacesPreviousInjectedMemoryMessage(t *testing.T) {
	t.Parallel()

	node := nodes.NewContextAssemblerNode()
	node.StateScope = "chat"

	state := State{}
	fruntime.Conversation(state, "chat").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a careful agent."),
		llms.TextParts(llms.ChatMessageTypeSystem, "Relevant recalled memory:\n- [assistant/fact] old memory"),
		llms.TextParts(llms.ChatMessageTypeHuman, "new request"),
	})
	fruntime.EnsureMemory(state)["recalled"] = []map[string]any{
		{
			"text": "fresh memory",
			"role": "assistant",
			"type": string(wfmemory.EntryTypeFact),
		},
	}

	_, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke context assembler: %v", err)
	}

	messages := fruntime.Conversation(state, "chat").Messages()
	count := 0
	for _, message := range messages {
		text := testMessageText(message)
		if message.Role == llms.ChatMessageTypeSystem && strings.HasPrefix(text, "Relevant recalled memory:") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one injected memory message, got %#v", messages)
	}
}

func TestContextAssemblerInjectsOrchestrationAndPlannerState(t *testing.T) {
	t.Parallel()

	node := nodes.NewContextAssemblerNode()
	node.StateScope = "chat"
	node.IncludeMemory = false
	node.IncludeOrchestration = true
	node.IncludePlanner = true

	state := State{}
	fruntime.Conversation(state, "chat").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a careful agent."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Continue with the task."),
	})
	fruntime.EnsureOrchestration(state)["mode"] = "direct"
	fruntime.EnsureOrchestration(state)["use_memory"] = true
	fruntime.EnsurePlanner(state)["status"] = "planned"
	fruntime.EnsurePlanner(state)["current_step_id"] = "step_1"
	fruntime.EnsurePlanner(state)["summary"] = "Validate inputs, then execute the first safe step."
	fruntime.EnsurePlanner(state)["plan"] = []map[string]any{
		{"id": "step_1", "title": "Validate inputs", "status": "pending", "kind": "validation"},
		{"id": "step_2", "title": "Run tool", "status": "pending", "kind": "action"},
	}

	_, err := node.Invoke(context.Background(), state)
	if err != nil {
		t.Fatalf("invoke context assembler: %v", err)
	}

	messages := fruntime.Conversation(state, "chat").Messages()
	if len(messages) != 4 {
		t.Fatalf("expected orchestration and planner messages to be injected, got %#v", messages)
	}

	foundOrchestration := false
	foundPlanner := false
	for _, message := range messages {
		text := testMessageText(message)
		if strings.HasPrefix(text, "Current orchestration state:") {
			foundOrchestration = true
			if strings.Contains(text, "\"mode\"") {
				t.Fatalf("expected orchestration context to be summarized, got %q", text)
			}
			if !strings.Contains(text, "- mode: direct") {
				t.Fatalf("expected summarized orchestration mode, got %q", text)
			}
		}
		if strings.HasPrefix(text, "Current plan state:") {
			foundPlanner = true
			if strings.Contains(text, "\"plan\"") {
				t.Fatalf("expected planner context to be summarized, got %q", text)
			}
			if !strings.Contains(text, "- next_steps:") {
				t.Fatalf("expected planner next_steps summary, got %q", text)
			}
		}
	}
	if !foundOrchestration || !foundPlanner {
		t.Fatalf("expected orchestration and planner sections, got %#v", messages)
	}
}

func testMessageText(message llms.MessageContent) string {
	for _, part := range message.Parts {
		if textPart, ok := part.(llms.TextContent); ok {
			return textPart.Text
		}
	}
	return ""
}
