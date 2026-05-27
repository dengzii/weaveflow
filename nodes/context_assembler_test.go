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

func TestContextAssemblerInjectsEnvironmentContext(t *testing.T) {
	t.Parallel()

	node := NewContextAssemblerNode()
	node.StateScope = "agent"

	state := wfstate.State{
		wfstate.StateKeyEnvironment: map[string]any{
			"workspace_root": "/repo/weaveflow",
			"cwd":            "/repo/weaveflow",
			"source":         "process_cwd",
			"os":             "linux",
			"project": map[string]any{
				"name":         "weaveflow",
				"type":         "go",
				"summary":      "Graph runtime for LLM agents.",
				"test_command": "go test ./...",
			},
			"git": map[string]any{
				"branch":             "main",
				"dirty":              true,
				"changed_file_count": 2,
				"changed_files":      []string{"M nodes/context_assembler.go", "A nodes/environment_context.go"},
			},
		},
	}
	state.Conversation("agent").UpdateMessage([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are helpful."),
		llms.TextParts(llms.ChatMessageTypeHuman, "Inspect project"),
	})

	next, err := runTestNode(t, node, context.Background(), state)
	if err != nil {
		t.Fatalf("invoke context assembler: %v", err)
	}

	messages := next.Conversation("agent").Messages()
	var environmentContext string
	for _, message := range messages {
		text := extractText(message)
		if strings.HasPrefix(text, defaultContextAssemblerEnvironmentHeader) {
			environmentContext = text
			break
		}
	}
	if environmentContext == "" {
		t.Fatal("expected environment context message")
	}
	for _, want := range []string{
		"workspace_root: /repo/weaveflow",
		"project:",
		"test_command: go test ./...",
		"git:",
		"changed_file_count: 2",
		`changed_files: ["M nodes/context_assembler.go","A nodes/environment_context.go"]`,
	} {
		if !strings.Contains(environmentContext, want) {
			t.Fatalf("expected environment context to contain %q, got:\n%s", want, environmentContext)
		}
	}
}
