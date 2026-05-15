package nodes

import (
	"context"
	"errors"
	"testing"
	fruntime "weaveflow/runtime"
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

func TestOrchestrationRouterUsesToolHeuristicForCurrentTime(t *testing.T) {
	t.Parallel()

	model := &failIfCalledModel{}
	node := NewOrchestrationRouterNode()
	node.InputPath = "request.input"
	node.AvailableModes = []string{"direct", "planner"}

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{
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
