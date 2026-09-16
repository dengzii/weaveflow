package plan

import (
	"strings"
	"testing"

	plancap "github.com/dengzii/weaveflow/capability/plan"
)

func TestBuildPlanStepPromptIncludesCanonicalExistingEvidenceRefs(t *testing.T) {
	steps := []plancap.Step{{
		ID: "research", Title: "Research", Description: "Explain the topic.",
		Evidence: []plancap.Evidence{
			{ToolID: "web_search", Status: "succeeded", Summary: "search lead"},
			{ToolID: "web_fetch", Status: "succeeded", Summary: "official source body", URL: "https://example.com/official", HTTPStatus: 200, Title: "Official"},
			{ToolID: "web_fetch", Status: "succeeded", Summary: "duplicate body", URL: "https://EXAMPLE.com/official#copy", HTTPStatus: 200},
		},
	}}
	prompt := buildPlanStepPrompt(map[string]any{
		planFieldObjective: "Explain the topic.",
		planFieldSummary:   "one research step",
	}, steps, 0)
	if !strings.Contains(prompt, "plan-global refs") || !strings.Contains(prompt, "- E2:") || !strings.Contains(prompt, "https://example.com/official") {
		t.Fatalf("prompt = %q", prompt)
	}
	if strings.Contains(prompt, "- E1:") || strings.Contains(prompt, "- E3:") || strings.Contains(prompt, "duplicate body") {
		t.Fatalf("prompt retained ineligible or duplicate evidence: %q", prompt)
	}
}
