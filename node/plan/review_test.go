package plan

import (
	"context"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestReviewRetriesCurrentStepWithoutAdvancing(t *testing.T) {
	target := NewReviewNode(core.WithID("review"))
	target.MaxAttempts = 2
	access := state.NewEditingAccess(state.NewState())
	planner, _ := plancap.Bind(access, target.PlanPath)
	if err := planner.Merge(map[string]any{
		plancap.FieldStatus:       PlanStatusExecuting,
		plancap.FieldCurrentIndex: 0,
		plancap.FieldReplanCount:  0,
		plancap.FieldMaxReplans:   1,
	}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := planner.SetSteps([]plancap.Step{{
		ID: "step_1", Title: "Test", Status: PlanStepStatusRunning,
		VerificationStatus: VerificationStatusRetry, VerificationSummary: "tests failed", VerificationAttempts: 1,
	}}); err != nil {
		t.Fatalf("seed steps: %v", err)
	}
	conversation, _ := conversationcap.Bind(access, target.ConversationPath)
	if err := conversation.SetFinalAnswer("implemented"); err != nil {
		t.Fatalf("seed result: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute review: %v", err)
	}
	if value, _ := planner.Field(plancap.FieldCurrentIndex); intValue(value) != 0 {
		t.Fatalf("current index = %v, want 0", value)
	}
	if value, _ := planner.Field(plancap.FieldStatus); value != PlanStatusExecuting {
		t.Fatalf("status = %v, want executing", value)
	}
}

func TestReviewAdvancesOnlyPassedStep(t *testing.T) {
	target := NewReviewNode(core.WithID("review"))
	access := state.NewEditingAccess(state.NewState())
	planner, _ := plancap.Bind(access, target.PlanPath)
	if err := planner.Merge(map[string]any{plancap.FieldCurrentIndex: 0, plancap.FieldReplanCount: 0, plancap.FieldMaxReplans: 1}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := planner.SetSteps([]plancap.Step{
		{ID: "step_1", Title: "First", VerificationStatus: VerificationStatusPassed, VerificationAttempts: 2},
		{ID: "step_2", Title: "Second", VerificationStatus: VerificationStatusPending},
	}); err != nil {
		t.Fatalf("seed steps: %v", err)
	}
	conversation, _ := conversationcap.Bind(access, target.ConversationPath)
	_ = conversation.SetFinalAnswer("verified")
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute review: %v", err)
	}
	steps := planner.Steps()
	if steps[0].Status != PlanStepStatusDone || steps[0].VerificationAttempts != 2 {
		t.Fatalf("first step = %#v", steps[0])
	}
	if value, _ := planner.Field(plancap.FieldCurrentIndex); intValue(value) != 1 {
		t.Fatalf("current index = %v, want 1", value)
	}
}

func TestReviewFinalizesWhenVerificationAttemptBudgetIsExhausted(t *testing.T) {
	target := NewReviewNode(core.WithID("review"))
	target.MaxAttempts = 2
	access := state.NewEditingAccess(state.NewState())
	planner, _ := plancap.Bind(access, target.PlanPath)
	if err := planner.Merge(map[string]any{
		plancap.FieldCurrentIndex: 0, plancap.FieldReplanCount: 0, plancap.FieldMaxReplans: 0,
	}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := planner.SetSteps([]plancap.Step{{
		ID: "step_1", Title: "Test", VerificationStatus: VerificationStatusRetry,
		VerificationSummary: "still failing", VerificationAttempts: 2,
	}}); err != nil {
		t.Fatalf("seed steps: %v", err)
	}
	conversation, _ := conversationcap.Bind(access, target.ConversationPath)
	_ = conversation.SetFinalAnswer("claimed success")
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute review: %v", err)
	}
	if value, _ := planner.Field(plancap.FieldStatus); value != PlanStatusFinalizing {
		t.Fatalf("status = %v, want finalizing", value)
	}
	if value, _ := planner.Field(plancap.FieldCurrentIndex); intValue(value) != 0 {
		t.Fatalf("index = %v, want 0", value)
	}
}

func TestReviewCanFinalizeRetryWithoutReplanning(t *testing.T) {
	target := NewReviewNode(core.WithID("review"))
	target.MaxAttempts = 1
	target.RetryExhaustedAction = ReviewActionFinalize
	access := state.NewEditingAccess(state.NewState())
	planner, _ := plancap.Bind(access, target.PlanPath)
	if err := planner.Merge(map[string]any{
		plancap.FieldCurrentIndex: 0, plancap.FieldReplanCount: 0, plancap.FieldMaxReplans: 3,
	}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := planner.SetSteps([]plancap.Step{{ID: "step_1", Title: "Check", VerificationStatus: VerificationStatusRetry, VerificationAttempts: 1}}); err != nil {
		t.Fatalf("seed steps: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute review: %v", err)
	}
	if value, _ := planner.Field(plancap.FieldStatus); value != PlanStatusFinalizing {
		t.Fatalf("status = %v, want finalizing", value)
	}
}

func TestReviewBuildsFailureActions(t *testing.T) {
	target, err := ReviewNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "review", Config: map[string]any{
			"max_attempts": 3, "retry_exhausted_action": ReviewActionFinalize, "failure_action": ReviewActionReplan,
		}},
		State: map[string]registry.ResolvedStateBinding{
			"plan":         {Path: state.Shared("plan")},
			"execution":    {Path: state.Shared("execution")},
			"conversation": {Path: state.Scope("review", "conversation")},
		},
	})
	if err != nil {
		t.Fatalf("build review: %v", err)
	}
	review := target.(*ReviewNode)
	if review.MaxAttempts != 3 || review.RetryExhaustedAction != ReviewActionFinalize || review.FailureAction != ReviewActionReplan {
		t.Fatalf("review controls = %#v", review)
	}
}
