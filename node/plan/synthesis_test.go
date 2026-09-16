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
	answer, err := ensureFinalEvidenceReferences("Completed with [S1:E1] and [S2:E1].", steps)
	if err != nil {
		t.Fatalf("ensure refs: %v", err)
	}
	if strings.Contains(answer, "[S1:E2]") {
		t.Fatalf("answer = %q", answer)
	}
	if strings.Count(answer, "[S1:E1]") != 2 || strings.Count(answer, "[S2:E1]") != 2 {
		t.Fatalf("answer does not contain one claim ref and one footer ref per citation: %q", answer)
	}
	if !strings.Contains(answer, "https://example.com/source") || !strings.Contains(answer, "accessed 2026-09-05T00:00:00Z") {
		t.Fatalf("answer does not map cited references to source metadata: %q", answer)
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
	if _, err := ensureFinalEvidenceReferences("Unsupported without a reference", steps); err == nil {
		t.Fatal("expected absent evidence reference rejection")
	}
	searchSteps := []plancap.Step{{ID: "search", Evidence: []plancap.Evidence{{
		ToolID: "web_search", Status: "succeeded", Summary: "search snippet",
	}}}}
	if _, err := ensureFinalEvidenceReferences("Unsupported [S1:E1]", searchSteps); err == nil {
		t.Fatal("expected uninspected search evidence rejection")
	}
	clockSteps := []plancap.Step{{ID: "clock", Evidence: []plancap.Evidence{{
		ToolID: "current_time", Status: "succeeded", Summary: "2026-09-11T07:42:03Z",
	}}}}
	if _, err := ensureFinalEvidenceReferences("Unsupported [S1:E1]", clockSteps); err == nil {
		t.Fatal("expected clock metadata evidence rejection")
	}
}

func TestSynthesisAppendsEveryCitedReferenceBeyondOldFooterLimit(t *testing.T) {
	evidence := make([]plancap.Evidence, 13)
	for index := range evidence {
		evidence[index] = plancap.Evidence{ToolID: "read", Status: "succeeded", Summary: "source content"}
	}
	answer, err := ensureFinalEvidenceReferences("Material claim [S1:E13].", []plancap.Step{{ID: "inspect", Evidence: evidence}})
	if err != nil {
		t.Fatalf("ensure refs: %v", err)
	}
	if strings.Count(answer, "[S1:E13]") != 2 || strings.Contains(answer, "[S1:E1] ") {
		t.Fatalf("answer = %q", answer)
	}
}

func TestSynthesisRendersStructuredBlocksWithCanonicalEvidenceRefs(t *testing.T) {
	steps := []plancap.Step{{ID: "research", Evidence: []plancap.Evidence{
		{ToolID: "web_search", Status: "succeeded", Summary: "search lead"},
		{ToolID: "web_fetch", Status: "succeeded", Summary: "official source", URL: "https://example.com/official", HTTPStatus: 200},
		{ToolID: "web_fetch", Status: "succeeded", Summary: "original paper", URL: "https://example.com/paper", HTTPStatus: 200},
	}}}
	answer, err := renderEvidenceGroundedSynthesis(synthesisOutput{AnswerBlocks: []synthesisAnswerBlock{
		{Text: "The architecture combines both approaches.", EvidenceRefs: []string{"[S1:E2]", "[S1:E3]", "[S1:E2]"}},
	}}, steps)
	if err != nil {
		t.Fatalf("render structured synthesis: %v", err)
	}
	if !strings.Contains(answer, "[S1:E2][S1:E3]") || strings.Contains(answer, "[S1:E1]") {
		t.Fatalf("answer = %q", answer)
	}
	if strings.Count(answer, "[S1:E2]") != 2 || strings.Count(answer, "[S1:E3]") != 2 {
		t.Fatalf("answer references = %q", answer)
	}
}

func TestSynthesisRequestsStructuredEvidenceOutput(t *testing.T) {
	target := NewSynthesisNode(core.WithID("synthesize"))
	target.RequireEvidenceRefs = true
	target.Temperature = 0
	access := state.NewEditingAccess(state.NewState())
	planner, err := plancap.Bind(access, target.PlanPath)
	if err != nil {
		t.Fatalf("bind plan: %v", err)
	}
	if err := planner.Merge(map[string]any{
		plancap.FieldObjective: "explain the architecture",
		plancap.FieldStatus:    PlanStatusFinalizing,
		plancap.FieldSteps: []map[string]any{{
			"id": "step_1", "title": "Research", "status": PlanStepStatusDone,
			"verification_status": VerificationStatusPassed,
			"evidence": []map[string]any{{
				"tool_id": "web_fetch", "status": "succeeded", "summary": "official source",
				"url": "https://example.com/official", "http_status": 200,
			}},
		}},
	}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	model := &structuredSynthesisModel{content: `{"answer_blocks":[{"text":"The architecture is documented.","evidence_refs":["[S1:E1]"]}]}`}
	ctx := core.NewContext(core.WithModel(context.Background(), model))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute synthesis: %v", err)
	}
	if !model.request.StrictResponse || model.request.ResponseName != "evidence_grounded_plan_synthesis" || len(model.request.ResponseSchema) == 0 {
		t.Fatalf("model request = %#v", model.request)
	}
	if model.request.Temperature == nil || *model.request.Temperature != 0 {
		t.Fatalf("temperature = %#v", model.request.Temperature)
	}
	result, ok := state.ReadPath(access.State(), target.ResultPath.String())
	if !ok || !strings.Contains(result.(string), "[S1:E1]") || !strings.Contains(result.(string), "https://example.com/official") {
		t.Fatalf("result = %#v, exists = %v", result, ok)
	}
}

type structuredSynthesisModel struct {
	content string
	request llms.ModelRequest
}

func (model *structuredSynthesisModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	model.request = request
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: model.content}}}, nil
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

func TestSynthesisWritesPartialAnswerAndFailsBusinessPlan(t *testing.T) {
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
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("synthesize incomplete plan: %v", err)
	}
	planValue := planner.Value()
	if planValue[plancap.FieldStatus] != PlanStatusFailed {
		t.Fatalf("plan status = %v, want failed", planValue[plancap.FieldStatus])
	}
	if result, ok := state.ReadPath(access.State(), target.ResultPath.String()); !ok || strings.TrimSpace(result.(string)) == "" {
		t.Fatalf("incomplete synthesis result = %#v, exists = %v", result, ok)
	}
	if answer, ok := planner.Field(plancap.FieldFinalAnswer); !ok || strings.TrimSpace(answer.(string)) == "" {
		t.Fatalf("incomplete synthesis final answer = %#v, exists = %v", answer, ok)
	}
	prompt := buildPlanSynthesisPrompt("answer the question", "research", []plancap.Step{{
		ID: "step_1", Status: PlanStepStatusPending, VerificationStatus: VerificationStatusRetry,
	}}, false)
	if !strings.Contains(prompt, "best evidence-supported partial answer") || !strings.Contains(prompt, "not fully completed") {
		t.Fatalf("incomplete synthesis prompt = %q", prompt)
	}
}
