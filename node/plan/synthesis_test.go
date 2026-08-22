package plan

import (
	"strings"
	"testing"

	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestSynthesisRequiresStableSuccessfulEvidenceRefs(t *testing.T) {
	steps := []plancap.Step{
		{ID: "inspect", Evidence: []plancap.Evidence{
			{ToolID: "read", Status: "succeeded", Summary: "source read"},
			{ToolID: "verify", Status: "failed", Summary: "first check failed"},
		}},
		{ID: "verify", Evidence: []plancap.Evidence{
			{ToolID: "go-test", Status: "succeeded", Summary: "tests passed"},
		}},
	}
	answer, err := ensureFinalEvidenceReferences("Completed with [S1:E1].", steps)
	if err != nil {
		t.Fatalf("ensure refs: %v", err)
	}
	if !strings.Contains(answer, "[S2:E1]") || strings.Contains(answer, "[S1:E2]") {
		t.Fatalf("answer = %q", answer)
	}
	prompt := buildPlanSynthesisPrompt("complete task", "2-step execution plan", steps, true)
	if !strings.Contains(prompt, "evidence [S1:E1]") || !strings.Contains(prompt, "Every material factual claim") {
		t.Fatalf("prompt = %q", prompt)
	}
	if _, err := ensureFinalEvidenceReferences("unsupported", []plancap.Step{{ID: "empty"}}); err == nil {
		t.Fatal("expected missing evidence error")
	}
}

func TestSynthesisBuildsEvidenceReferenceConfiguration(t *testing.T) {
	target, err := SynthesisNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "synthesize", Config: map[string]any{
			"require_evidence_refs": true,
		}},
		State: map[string]registry.ResolvedStateBinding{
			"plan":   {Path: state.Shared("custom", "plan")},
			"result": {Path: state.Shared("custom", "result")},
		},
	})
	if err != nil {
		t.Fatalf("build synthesis: %v", err)
	}
	synthesis := target.(*SynthesisNode)
	if !synthesis.RequireEvidenceRefs || synthesis.PlanPath.String() != "shared.custom.plan" || synthesis.ResultPath.String() != "shared.custom.result" {
		t.Fatalf("synthesis = %#v", synthesis)
	}
}

func TestSynthesisBuildsCompletionAndModelControls(t *testing.T) {
	target, err := SynthesisNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "synthesize", Config: map[string]any{
			"fail_on_incomplete": false, "max_tokens": 900, "temperature": 0.3, "thinking": "medium",
		}},
		State: map[string]registry.ResolvedStateBinding{
			"plan":   {Path: state.Shared("plan")},
			"result": {Path: state.Shared("result")},
		},
	})
	if err != nil {
		t.Fatalf("build synthesis: %v", err)
	}
	synthesis := target.(*SynthesisNode)
	if synthesis.FailOnIncomplete || synthesis.MaxTokens != 900 || synthesis.Temperature != 0.3 || synthesis.Thinking != llms.ThinkingModeMedium {
		t.Fatalf("synthesis controls = %#v", synthesis)
	}
}
