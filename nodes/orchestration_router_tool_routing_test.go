package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"weaveflow/core"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type failIfCalledModel struct {
	calls int
}

func (m *failIfCalledModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	m.calls++
	return nil, errors.New("model should not be called")
}

func (m *failIfCalledModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("model should not be called")
}

type recordingRouterModel struct {
	system string
	human  string
}

func (m *recordingRouterModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	for _, message := range messages {
		switch message.Role {
		case llms.ChatMessageTypeSystem:
			m.system = extractText(message)
		case llms.ChatMessageTypeHuman:
			m.human = extractText(message)
		}
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{
				Content: `{"mode":"direct","use_memory":false,"memory_query":"","needs_clarification":false,"clarification_question":"","clarification_options":[],"reasoning":"Ordinary execution is sufficient.","target_subgraph":"","direct_answer":""}`,
			},
		},
	}, nil
}

func (m *recordingRouterModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("unexpected Call")
}

func TestOrchestrationRouterUsesToolHeuristicForCurrentTime(t *testing.T) {
	t.Parallel()

	model := &failIfCalledModel{}
	node := NewOrchestrationRouterNode()
	node.InputPath = "request.input"
	node.AvailableModes = []string{"direct", "planner"}

	ctx := core.WithServices(context.Background(), &core.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"current_time": tools.NewCurrentTime(),
		},
	})

	state := wfstate.State{
		"request": map[string]any{
			"input": "现在几点",
		},
	}

	state, err := runTestNode(t, node, ctx, state)
	if err != nil {
		t.Fatalf("invoke orchestration router: %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("expected heuristic routing to skip model call, got %d calls", model.calls)
	}

	orchestration := state.Get(wfstate.StateKeyOrchestration)
	if orchestration == nil {
		t.Fatal("expected orchestration state to be written")
	}
	if got := orchestration["mode"]; got != "direct" {
		t.Fatalf("expected direct mode, got %#v", got)
	}
	if got := orchestration["use_memory"]; got != false {
		t.Fatalf("expected use_memory=false, got %#v", got)
	}
	if got := orchestration["direct_answer"]; got != "" {
		t.Fatalf("expected empty direct_answer so tool loop can proceed, got %#v", got)
	}
}

func TestOrchestrationRouterSystemPromptUsesConfiguredModes(t *testing.T) {
	t.Parallel()

	model := &recordingRouterModel{}
	node := NewOrchestrationRouterNode()
	node.InputPath = "request.input"
	node.AvailableModes = []string{"direct", "planner"}

	ctx := core.WithServices(context.Background(), &core.Services{
		Model: model,
	})

	state := wfstate.State{
		"request": map[string]any{
			"input": "帮我实现一个登录功能",
		},
	}

	if _, err := runTestNode(t, node, ctx, state); err != nil {
		t.Fatalf("invoke orchestration router: %v", err)
	}
	if !strings.Contains(model.system, "Available modes: direct, planner") {
		t.Fatalf("expected configured modes in system prompt, got %q", model.system)
	}
	if !strings.Contains(model.system, "mode=direct means route into the ordinary executor") {
		t.Fatalf("expected direct mode semantics in system prompt, got %q", model.system)
	}
	if strings.Contains(model.human, "answer immediately when direct is enough") {
		t.Fatalf("human prompt still encourages direct answering: %q", model.human)
	}
}
