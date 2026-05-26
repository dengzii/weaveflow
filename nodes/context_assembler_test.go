package nodes

import (
	"context"
	"strings"
	"testing"
	wfstate "weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestContextAssemblerIncludesCurrentStepDetails(t *testing.T) {
	t.Parallel()

	node := NewContextAssemblerNode()
	node.StateScope = "agent"

	state := wfstate.State{
		wfstate.StateKeyPlanner: map[string]any{
			"objective":        "Deploy service",
			"status":           "executing",
			"summary":          "Deploy safely.",
			"current_step_id":  "step_1",
			"current_step_ids": []string{"step_1"},
			"plan": []map[string]any{
				{
					"id":                  "step_1",
					"title":               "Check rollout",
					"description":         "Verify rollout safety before writing changes.",
					"status":              "in_progress",
					"kind":                "validation",
					"inputs":              []string{"observations"},
					"outputs":             []string{"verification"},
					"acceptance_criteria": []string{"Safety criteria are explicit."},
				},
			},
		},
		wfstate.StateKeyExecution: map[string]any{
			"route": "verifier",
			"current_step": map[string]any{
				"id":                  "step_1",
				"title":               "Check rollout",
				"description":         "Verify rollout safety before writing changes.",
				"status":              "in_progress",
				"kind":                "validation",
				"inputs":              []string{"observations"},
				"outputs":             []string{"verification"},
				"acceptance_criteria": []string{"Safety criteria are explicit."},
			},
		},
	}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Deploy service"),
	})

	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke context assembler: %v", err)
	}

	messages := next.Conversation("agent").Messages()
	var plannerContext string
	for _, message := range messages {
		text := extractText(message)
		if strings.HasPrefix(text, defaultContextAssemblerPlannerHeader) {
			plannerContext = text
			break
		}
	}
	if plannerContext == "" {
		t.Fatal("expected planner context message")
	}
	for _, want := range []string{
		"execution_route: verifier",
		"current_step:",
		"description: Verify rollout safety before writing changes.",
		`acceptance_criteria: ["Safety criteria are explicit."]`,
	} {
		if !strings.Contains(plannerContext, want) {
			t.Fatalf("expected planner context to contain %q, got:\n%s", want, plannerContext)
		}
	}
}
