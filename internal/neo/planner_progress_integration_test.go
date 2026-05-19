package neo

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"weaveflow/core"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

type recordingEventSink struct {
	mu     sync.Mutex
	events []fruntime.Event
}

func (s *recordingEventSink) Publish(_ context.Context, event fruntime.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingEventSink) PublishBatch(ctx context.Context, events []fruntime.Event) error {
	for _, event := range events {
		if err := s.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *recordingEventSink) snapshot() []fruntime.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fruntime.Event(nil), s.events...)
}

func TestNeoPlannerProgressIntegration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = "planner"
	cfg.MemoryRecallLimit = 0
	cfg.MaxIterations = 20

	graph, err := NewGraph(cfg)
	if err != nil {
		t.Fatalf("build neo graph: %v", err)
	}

	model := &scriptedNeoModel{
		responses: []*llms.ContentResponse{
			contentResponse(`{
  "mode": "planner",
  "use_memory": false,
  "memory_query": "",
  "needs_clarification": false,
  "clarification_question": "",
  "reasoning": "The request needs a short checked plan.",
  "target_subgraph": "",
  "direct_answer": ""
}`),
			contentResponse(`{
  "objective": "Prepare a short integration-test summary",
  "status": "planned",
  "summary": "Create and verify a concise summary.",
  "replan_reason": "",
  "plan": [
    {
      "id": "step_1",
      "title": "Draft summary",
      "description": "Write a concise summary for the request.",
      "status": "ready",
      "kind": "action",
      "node_type": "llm",
      "depends_on": [],
      "inputs": ["request.input"],
      "outputs": ["conversation.answer"],
      "acceptance_criteria": ["The response contains a concise summary."],
      "parallelizable": false
    }
  ]
}`),
			contentResponse("The response contains a concise summary."),
			contentResponse(`{
  "status": "pass",
  "issues": [],
  "summary": "The step satisfied its acceptance criteria.",
  "suggestion": "continue"
}`),
			contentResponse("Final answer based on the completed plan."),
		},
	}

	sink := &recordingEventSink{}
	runner := newChatRunner(graph, "neo-progress-test", t.TempDir(), sink)
	ctx := core.WithServices(context.Background(), &core.Services{Model: model})

	_, state, err := runner.Start(ctx, NewInitialState("Prepare a short integration-test summary", nil))
	if err != nil {
		t.Fatalf("run neo graph: %v", err)
	}

	if model.calls != len(model.responses) {
		t.Fatalf("model calls = %d, want %d", model.calls, len(model.responses))
	}

	progress := collectPlannerProgress(t, sink.snapshot())
	if len(progress) == 0 {
		t.Fatal("expected planner_progress events")
	}

	phases := plannerProgressPhases(progress)
	for _, want := range []string{"planned", "step_started", "step_completed", "completed"} {
		if !slices.Contains(phases, want) {
			t.Fatalf("planner progress phases = %#v, missing %q", phases, want)
		}
	}

	finalProgress := progress[len(progress)-1]
	if got := finalProgress["status"]; got != "completed" {
		t.Fatalf("final planner progress status = %#v, want completed", got)
	}
	if got := intFromAny(finalProgress["percent"]); got != 100 {
		t.Fatalf("final planner progress percent = %d, want 100", got)
	}

	planner := state.Get(wfstate.StateKeyPlanner)
	if planner == nil {
		t.Fatal("expected planner state")
	}
	if got := planner["status"]; got != "completed" {
		t.Fatalf("planner.status = %#v, want completed", got)
	}
	if got := planner["current_step_id"]; got != "" {
		t.Fatalf("planner.current_step_id = %#v, want empty after finalize route", got)
	}
	steps := planner["plan"].([]map[string]any)
	if got := steps[0]["status"]; got != "completed" {
		t.Fatalf("plan[0].status = %#v, want completed", got)
	}
	if got := finalAnswerFromState(state); got != "Final answer based on the completed plan." {
		t.Fatalf("final answer = %q", got)
	}
}

func TestTranslatePlannerProgressEvent(t *testing.T) {
	payload := map[string]any{
		"kind":         "planner_progress",
		"phase":        "step_started",
		"message":      "Draft summary",
		"status":       "executing",
		"summary":      "Create and verify a concise summary.",
		"percent":      0,
		"planner_path": "planner",
		"steps": []map[string]any{
			{"id": "step_1", "title": "Draft summary", "status": "in_progress"},
		},
	}

	got := TranslateEvent(fruntime.Event{
		Type:    fruntime.EventNodeCustom,
		NodeID:  "PlanStepExecutor_test",
		Payload: rawJSON(payload),
	})
	if got == nil {
		t.Fatal("TranslateEvent() = nil, want planner_progress event")
	}
	if got.Type != ChatEventTypePlan {
		t.Fatalf("TranslateEvent().Type = %q, want %q", got.Type, ChatEventTypePlan)
	}
	if got.Content != "Draft summary" {
		t.Fatalf("TranslateEvent().Content = %q, want Draft summary", got.Content)
	}

	var data map[string]any
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal planner progress data: %v", err)
	}
	if data["kind"] != "planner_progress" {
		t.Fatalf("data[kind] = %#v, want planner_progress", data["kind"])
	}
	if data["phase"] != "step_started" {
		t.Fatalf("data[phase] = %#v, want step_started", data["phase"])
	}
}

func contentResponse(content string) *llms.ContentResponse {
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: content}},
	}
}

func collectPlannerProgress(t *testing.T, events []fruntime.Event) []map[string]any {
	t.Helper()
	var progress []map[string]any
	for _, event := range events {
		if event.Type != fruntime.EventNodeCustom {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal custom event payload: %v", err)
		}
		if payload["kind"] == "planner_progress" {
			progress = append(progress, payload)
		}
	}
	return progress
}

func plannerProgressPhases(progress []map[string]any) []string {
	phases := make([]string, 0, len(progress))
	for _, item := range progress {
		if phase, _ := item["phase"].(string); phase != "" {
			phases = append(phases, phase)
		}
	}
	return phases
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}
