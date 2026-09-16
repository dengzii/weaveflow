package plan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type staticPlanModel struct{}

func (staticPlanModel) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: `{"summary":"implement and test","steps":[{"id":"step_1","title":"Implement","description":"Implement the requested behavior.","tool_ids":[],"deliverables":["implementation"],"acceptance_criteria":["focused tests pass"],"verification_strategy":"evidence"}]}`}}}, nil
}

func TestGeneratorReplanPreservesSuccessfulStepEvidenceInHistory(t *testing.T) {
	target := NewGeneratorNode(core.WithID("generate_plan"))
	access := state.NewEditingAccess(state.FromShared(map[string]any{
		"request": map[string]any{"input": "Implement and test the feature."},
	}))
	planner, _ := plancap.Bind(access, target.PlanPath)
	if err := planner.Merge(map[string]any{
		plancap.FieldObjective:    "Implement and test the feature.",
		plancap.FieldStatus:       PlanStatusReplan,
		plancap.FieldSummary:      "old plan",
		plancap.FieldCurrentIndex: 1,
		plancap.FieldReplanCount:  0,
		plancap.FieldMaxReplans:   1,
		plancap.FieldReplanReason: "second step failed",
	}); err != nil {
		t.Fatalf("seed planner: %v", err)
	}
	if err := planner.SetSteps([]plancap.Step{{
		ID: "inspect", Title: "Inspect", Status: PlanStepStatusDone, VerificationStatus: VerificationStatusPassed,
		Evidence: []plancap.Evidence{{ToolID: "read", Status: "succeeded", Summary: "inspected file"}},
	}}); err != nil {
		t.Fatalf("seed steps: %v", err)
	}
	ctx := core.NewContext(core.WithModel(context.Background(), staticPlanModel{}))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute generator: %v", err)
	}
	history := planner.History()
	if len(history) != 1 {
		t.Fatalf("history = %#v", history)
	}
	archived := plancap.DecodeSteps(history[0]["steps"])
	if len(archived) != 1 || len(archived[0].Evidence) != 1 || archived[0].Evidence[0].Summary != "inspected file" {
		t.Fatalf("archived steps = %#v", archived)
	}
}

func TestGeneratorCompactsPreviousStepsForReplanPrompt(t *testing.T) {
	largeBody := "do-not-copy-full-evidence:" + strings.Repeat("x", 32*1024)
	steps := []plancap.Step{{
		ID: "research", Title: "Research", Description: "Fetch sources.", Status: PlanStepStatusPending,
		Result: largeBody, VerificationSummary: largeBody,
		Evidence: []plancap.Evidence{
			{ToolID: "web_search", Status: "succeeded", Summary: largeBody},
			{ToolID: "web_fetch", Status: "succeeded", Summary: largeBody, URL: "https://example.com/source", HTTPStatus: 200, Title: "Source"},
		},
		AttemptHistory: []plancap.Attempt{{Number: 1, Result: largeBody, Evidence: []plancap.Evidence{{ToolID: "web_fetch", Summary: largeBody}}}},
	}}
	encoded, err := json.Marshal(compactPlanStepsForPrompt(steps))
	if err != nil {
		t.Fatalf("encode compact steps: %v", err)
	}
	if len(encoded) > 8*1024 {
		t.Fatalf("compact steps size = %d", len(encoded))
	}
	if strings.Count(string(encoded), "do-not-copy-full-evidence:") != 2 {
		t.Fatalf("compact steps retained raw evidence or attempt history: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"ref":"E2"`) || !strings.Contains(string(encoded), `"url":"https://example.com/source"`) {
		t.Fatalf("compact steps lost evidence identity: %s", encoded)
	}
}

func TestGeneratorNormalizesUnicodeStepSeparators(t *testing.T) {
	steps := normalizePlanSteps([]plancap.Step{{ID: "step‑1", Title: "Inspect", Description: "Inspect"}, {ID: "step-1", Title: "Verify", Description: "Verify"}}, 4, nil)
	if len(steps) != 2 || steps[0].ID != "step-1" || steps[1].ID != "step-1_2" {
		t.Fatalf("normalized steps = %#v", steps)
	}
}

func TestGeneratorInitializesHistoryAsArray(t *testing.T) {
	target := NewGeneratorNode(core.WithID("generate_plan"))
	access := state.NewEditingAccess(state.FromShared(map[string]any{
		"request": map[string]any{"input": "Implement and test the feature."},
	}))
	ctx := core.NewContext(core.WithModel(context.Background(), staticPlanModel{}))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute generator: %v", err)
	}

	history, ok := access.ReadAny(target.PlanPath.MustChild(planFieldHistory))
	if !ok || history == nil {
		t.Fatalf("history = %#v, want an empty array", history)
	}
	if _, ok := history.([]map[string]any); !ok {
		t.Fatalf("history type = %T, want []map[string]any", history)
	}
}

func TestGeneratorEnforcesObjectiveAndSummaryInvariants(t *testing.T) {
	objective := "Implement NormalizeTags and prove it with focused tests."
	target := NewGeneratorNode(core.WithID("generate_plan"))
	target.MaxSteps = 1
	target.VerifierID = "go-test"
	model := staticContentPlanModel{content: `{"summary":"Plan contains 8 steps","steps":[{"id":"inspect","title":"Read Existing Test File","description":"Read the test file.","tool_ids":["read"],"deliverables":["test contents"],"acceptance_criteria":["test file is readable"],"verification_strategy":"evidence"}]}`}
	available := map[string]core.Tool{
		"read":  {Function: &llms.FunctionDefinition{Name: "read"}},
		"edit":  {Function: &llms.FunctionDefinition{Name: "edit"}},
		"write": {Function: &llms.FunctionDefinition{Name: "write"}},
	}
	access := state.NewEditingAccess(state.FromShared(map[string]any{
		"request": map[string]any{"input": objective},
	}))
	ctx := core.NewContext(core.WithTools(core.WithModel(context.Background(), model), available))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute generator: %v", err)
	}
	planner, _ := plancap.Bind(access, target.PlanPath)
	summary, _ := planner.Value()[plancap.FieldSummary].(string)
	if !strings.HasPrefix(summary, "1-step execution plan:") || strings.Contains(summary, "8 steps") {
		t.Fatalf("summary = %q", summary)
	}
	steps := planner.Steps()
	if len(steps) != 1 {
		t.Fatalf("steps = %#v", steps)
	}
	step := steps[0]
	if !sliceContainsText(step.Deliverables, "Objective outcome: "+objective) {
		t.Fatalf("deliverables = %#v", step.Deliverables)
	}
	if !sliceContainsText(step.AcceptanceCriteria, `The configured verifier "go-test" passes with successful evidence.`) {
		t.Fatalf("acceptance criteria = %#v", step.AcceptanceCriteria)
	}
	if !sliceContainsText(step.ToolIDs, "edit") || !sliceContainsText(step.ToolIDs, "write") {
		t.Fatalf("tool IDs = %#v", step.ToolIDs)
	}
}

func TestGeneratorUsesSingleStepFastPathForSimpleDefinition(t *testing.T) {
	objective := "什么是 RWKV"
	target := NewGeneratorNode(core.WithID("generate_plan"))
	target.MaxSteps = 6
	model := staticContentPlanModel{content: `{"summary":"research and synthesize","steps":[{"id":"scope","title":"Scope","description":"Fetch authoritative definitions.","tool_ids":["read"],"deliverables":["definition"],"acceptance_criteria":["definition is supported"],"verification_strategy":"evidence"},{"id":"synthesize","title":"Synthesize findings and produce final answer","description":"Produce final answer.","tool_ids":[],"deliverables":["answer"],"acceptance_criteria":["answer complete"],"verification_strategy":"evidence"}]}`}
	available := map[string]core.Tool{"read": {Function: &llms.FunctionDefinition{Name: "read"}}}
	access := state.NewEditingAccess(state.FromShared(map[string]any{"request": map[string]any{"input": objective}}))
	ctx := core.NewContext(core.WithTools(core.WithModel(context.Background(), model), available))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute generator: %v", err)
	}
	planner, _ := plancap.Bind(access, target.PlanPath)
	steps := planner.Steps()
	if len(steps) != 1 || steps[0].ID != "scope" {
		t.Fatalf("fast-path steps = %#v", steps)
	}
}

func TestGeneratorRemovesRedundantFinalSynthesisStep(t *testing.T) {
	steps := []plancap.Step{
		{ID: "research", Title: "Research", Description: "Fetch sources."},
		{ID: "synthesize", Title: "Synthesize findings and produce final answer", Description: "Write the final response."},
	}
	got := removeRedundantSynthesisSteps(steps)
	if len(got) != 1 || got[0].ID != "research" {
		t.Fatalf("steps = %#v", got)
	}
}

func TestGeneratorBuildsModelAndVerificationControls(t *testing.T) {
	target, err := GeneratorNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "generate", Config: map[string]any{
			"verification_strategy": "go-test", "max_tokens": 1200, "temperature": 0.4, "thinking": "low",
		}},
		State: map[string]registry.ResolvedStateBinding{
			"objective": {Path: state.Shared("request", "input")},
			"plan":      {Path: state.Shared("custom", "plan")},
			"execution": {Path: state.Shared("custom", "execution")},
		},
	})
	if err != nil {
		t.Fatalf("build generator: %v", err)
	}
	generator := target.(*GeneratorNode)
	if generator.DefaultVerificationStrategy != "go-test" || generator.MaxTokens != 1200 || generator.Temperature != 0.4 || generator.Thinking != llms.ThinkingModeLow {
		t.Fatalf("generator controls = %#v", generator)
	}
}

type staticContentPlanModel struct {
	content string
}

func (model staticContentPlanModel) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: model.content}}}, nil
}

func sliceContainsText(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
