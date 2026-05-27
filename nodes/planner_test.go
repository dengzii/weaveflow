package nodes

import (
	"context"
	"strings"
	"testing"
	"weaveflow/core"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestPlannerPromptIncludesStepComplexityConstraints(t *testing.T) {
	t.Parallel()

	model := &captureLLMModel{response: `{
		"objective": "Inspect state design",
		"status": "planned",
		"summary": "Use focused research before synthesis.",
		"replan_reason": "",
		"plan": [{
			"id": "step_1",
			"title": "Inspect state keys",
			"description": "Read state/keys.go only.",
			"status": "ready",
			"kind": "research",
			"node_type": "researcher",
			"depends_on": [],
			"inputs": ["state/keys.go"],
			"outputs": ["state_keys_summary"],
			"acceptance_criteria": ["State keys are summarized."],
			"parallelizable": false
		}]
	}`}
	node := NewPlannerNode()

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"objective": "Inspect state design",
		},
	}
	ctx := core.WithServices(context.Background(), &core.Services{Model: model})

	if _, err := runTestNode(t, node, ctx, state); err != nil {
		t.Fatalf("invoke planner: %v", err)
	}

	prompt := promptText(model.lastMessages)
	for _, want := range []string{
		`"step_constraints"`,
		`"max_inputs_per_step": 3`,
		`"split_failed_broad_steps": true`,
		"do not minimize step count by merging unrelated work",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected planner prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func promptText(messages []llms.MessageContent) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, extractText(message))
	}
	return strings.Join(parts, "\n")
}
