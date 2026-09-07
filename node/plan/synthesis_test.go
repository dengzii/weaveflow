package plan

import (
	"context"
	"strings"
	"testing"

	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
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
			{ToolID: "web_fetch", Status: "succeeded", Summary: "tests passed", URL: "https://example.com/source", HTTPStatus: 200, AccessedAt: "2026-09-05T00:00:00Z"},
		}},
	}
	answer, err := ensureFinalEvidenceReferences("Completed with [S1:E1].", steps)
	if err != nil {
		t.Fatalf("ensure refs: %v", err)
	}
	if !strings.Contains(answer, "[S2:E1]") || strings.Contains(answer, "[S1:E2]") {
		t.Fatalf("answer = %q", answer)
	}
	if !strings.Contains(answer, "https://example.com/source") || !strings.Contains(answer, "accessed 2026-09-05T00:00:00Z") {
		t.Fatalf("answer does not map references to source metadata: %q", answer)
	}
	prompt := buildPlanSynthesisPrompt("complete task", "2-step execution plan", steps, true)
	if !strings.Contains(prompt, "evidence [S1:E1]") || !strings.Contains(prompt, "Every material factual claim") {
		t.Fatalf("prompt = %q", prompt)
	}
	if _, err := ensureFinalEvidenceReferences("unsupported", []plancap.Step{{ID: "empty"}}); err == nil {
		t.Fatal("expected missing evidence error")
	}
	if _, err := ensureFinalEvidenceReferences("Unsupported [S1:E2]", steps); err == nil {
		t.Fatal("expected failed evidence reference rejection")
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

func TestSynthesisRejectsIncompletePlanBeforeWritingAnswer(t *testing.T) {
	target := NewSynthesisNode(core.WithID("synthesize"))
	access := state.NewEditingAccess(state.NewState())
	planner, err := plancap.Bind(access, target.PlanPath)
	if err != nil {
		t.Fatalf("bind plan: %v", err)
	}
	if err := planner.Merge(map[string]any{
		plancap.FieldObjective: "answer the question",
		plancap.FieldStatus:    PlanStatusFinalizing,
		plancap.FieldSteps: []map[string]any{{
			"id": "step_1", "title": "Research", "status": PlanStepStatusFailed,
			"verification_status": VerificationStatusRetry, "result": "partial",
		}},
	}); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	model := staticPlanModel{}
	ctx := core.NewContext(core.WithModel(context.Background(), model))
	if _, err := target.Execute(ctx, access); err == nil {
		t.Fatal("synthesis accepted an incomplete plan")
	}
	planValue := planner.Value()
	if planValue[plancap.FieldStatus] != PlanStatusFailed {
		t.Fatalf("plan status = %v, want failed", planValue[plancap.FieldStatus])
	}
	if _, ok := state.ReadPath(access.State(), target.ResultPath.String()); ok {
		t.Fatal("incomplete synthesis wrote a final result")
	}
	if _, ok := planner.Field(plancap.FieldFinalAnswer); ok {
		t.Fatal("incomplete synthesis wrote a final answer")
	}
}
