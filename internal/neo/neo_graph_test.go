package neo

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type scriptedNeoModel struct {
	responses []*llms.ContentResponse
	calls     int
}

func (m *scriptedNeoModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.calls >= len(m.responses) {
		return nil, fmt.Errorf("unexpected model call %d", m.calls+1)
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *scriptedNeoModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
}

func TestNewGraphCurrentTimeUsesToolLoop(t *testing.T) {
	t.Parallel()

	graph, err := NewGraph(DefaultConfig())
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{
					{
						ToolCalls: []llms.ToolCall{
							{
								ID:   "call_time",
								Type: "function",
								FunctionCall: &llms.FunctionCall{
									Name:      "current_time",
									Arguments: "{}",
								},
							},
						},
					},
				},
			},
			{
				Choices: []*llms.ContentChoice{
					{
						Content: "The current time is available from the current_time tool.",
					},
				},
			},
		},
	}

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{
		Model: model,
		Tools: map[string]tools.Tool{
			"current_time": tools.NewCurrentTime(),
		},
	})

	state := NewInitialState("现在几点", nil)
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	if model.calls != 2 {
		t.Fatalf("expected two llm calls for tool loop, got %d", model.calls)
	}

	orchestration := state.Get(wfstate.StateKeyOrchestration)
	if orchestration == nil {
		t.Fatal("expected orchestration state")
	}
	if got := orchestration["direct_answer"]; got != "" {
		t.Fatalf("expected router to avoid direct_answer for time requests, got %#v", got)
	}

	if got := state.Conversation(stateScope).FinalAnswer(); got != "The current time is available from the current_time tool." {
		t.Fatalf("unexpected final answer: %#v", got)
	}
}

func TestNewGraphClarificationShortCircuitsAfterRouter(t *testing.T) {
	t.Parallel()

	graph, err := NewGraph(DefaultConfig())
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{
					{
						Content: `{
  "mode": "planner",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": true,
  "clarification_question": "Do you want only a diagnosis of the orchestration issues, or should I modify the graph as well?",
  "reasoning": "The requested scope is ambiguous.",
  "target_subgraph": "",
  "direct_answer": ""
}`,
					},
				},
			},
		},
	}

	ctx := fruntime.WithServices(context.Background(), &fruntime.Services{Model: model})

	state := NewInitialState("neo agent 编排不是很好现在, 检查需要优化的点", nil)
	state, err = graph.Run(ctx, state)
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	if model.calls != 1 {
		t.Fatalf("expected clarification path to stop after router, got %d model calls", model.calls)
	}

	final := state.Get(wfstate.StateKeyFinal)
	if final == nil {
		t.Fatal("expected final state")
	}
	if got := final["status"]; got != nodes.FinalStatusNeedsClarification {
		t.Fatalf("expected clarification final status, got %#v", got)
	}

	answer, _ := final["answer"].(string)
	if answer == "" || !strings.Contains(answer, "Do you want only a diagnosis of the orchestration issues, or should I modify the graph as well?") {
		t.Fatalf("unexpected clarification answer: %#v", answer)
	}
}
