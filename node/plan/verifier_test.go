package plan

import (
	"context"
	"strings"
	"testing"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func TestVerifierRejectsUnsupportedCompletionClaim(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "test", Title: "Test", Description: "Run tests.",
		Deliverables: []string{"passing test suite"}, AcceptanceCriteria: []string{"tests pass"}, VerificationStrategy: "evidence",
	})
	if err := conversation.SetFinalAnswer("All tests passed."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "no tool evidence") {
		t.Fatalf("verification = %s %q", step.VerificationStatus, step.VerificationSummary)
	}
}

func TestVerifierPreservesFailedAttemptThenPasses(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "test", Title: "Test", Description: "Run tests.",
		Deliverables: []string{"passing test suite"}, AcceptanceCriteria: []string{"tests pass"}, VerificationStrategy: "fixed",
	})
	if err := conversation.SetFinalAnswer("Implemented and tested."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	ctx := WithVerifier(context.Background(), func(_ context.Context, _ VerificationRequest) (VerificationResult, error) {
		return VerificationResult{Status: VerificationStatusRetry, Summary: "first test failed", Retryable: true}, nil
	})
	if _, err := target.Execute(core.NewContext(ctx), access); err != nil {
		t.Fatalf("first verification: %v", err)
	}
	ctx = WithVerifier(context.Background(), func(_ context.Context, _ VerificationRequest) (VerificationResult, error) {
		return VerificationResult{
			Status: VerificationStatusPassed, Summary: "focused tests passed",
			Evidence: []plancap.Evidence{{ToolID: "fixed", Status: "succeeded", Summary: "go test succeeded"}},
		}, nil
	})
	if _, err := target.Execute(core.NewContext(ctx), access); err != nil {
		t.Fatalf("second verification: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusPassed || step.VerificationAttempts != 2 || len(step.AttemptHistory) != 2 {
		t.Fatalf("step = %#v", step)
	}
	if step.AttemptHistory[0].VerificationStatus != VerificationStatusRetry || step.AttemptHistory[1].VerificationStatus != VerificationStatusPassed {
		t.Fatalf("history = %#v", step.AttemptHistory)
	}
}

func TestVerifierRejectsFailedToolEvidence(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "write", Title: "Write", Description: "Write a file.",
		Deliverables: []string{"file"}, AcceptanceCriteria: []string{"file written"}, VerificationStrategy: "evidence",
	})
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolResult{
		ToolCallID: "write-1", Name: "write", IsError: true, ErrorMessage: "permission denied",
	}}}}); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	_ = conversation.SetFinalAnswer("The file was written.")
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "failure") {
		t.Fatalf("verification = %s %q", step.VerificationStatus, step.VerificationSummary)
	}
}

func TestVerifierSanitizesAndLimitsEvidence(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "inspect", Title: "Inspect", Description: "Inspect a file.",
		Deliverables: []string{"analysis"}, AcceptanceCriteria: []string{"file inspected"}, VerificationStrategy: "evidence",
	})
	secret := "Authorization: Bearer super-secret-token PRIVATE_VALUE=environment-secret"
	messages := []llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolResult{
		ToolCallID: "read-1", Name: "read", Content: secret + strings.Repeat("x", 9000),
	}}}}
	if err := conversation.SetMessages(messages); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("Inspected the file."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if len(step.Evidence) != 1 || strings.Contains(step.Evidence[0].Summary, "super-secret-token") || strings.Contains(step.Evidence[0].Summary, "environment-secret") || len(step.Evidence[0].Summary) > 8200 {
		t.Fatalf("evidence was not sanitized and limited: %#v", step.Evidence)
	}
}

func TestGroundedCriticRejectsUnsupportedClaim(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "analyze", Title: "Analyze", Description: "Analyze graph routing.",
		Deliverables: []string{"grounded analysis"}, AcceptanceCriteria: []string{"claims cite inspected source"}, VerificationStrategy: "fixed",
	})
	target.CriticEnabled = true
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolResult{
		ToolCallID: "read-1", Name: "read", Content: "191 graph.AddResolvedConditionalEdge(executeTools.ID(), execute.ID(), condition, contract)",
	}}}}); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("The graph is a directed acyclic graph."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	model := &staticCriticModel{content: `{"passed":false,"summary":"topology claim is contradicted","supported_claims":[],"unsupported_claims":["the graph is acyclic"]}`}
	ctx := WithVerifier(core.WithModel(context.Background(), model), func(_ context.Context, _ VerificationRequest) (VerificationResult, error) {
		return VerificationResult{Status: VerificationStatusPassed, Summary: "deterministic check passed"}, nil
	})
	if _, err := target.Execute(core.NewContext(ctx), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "acyclic") || model.calls != 1 {
		t.Fatalf("step = %#v, critic calls = %d", step, model.calls)
	}
}

func TestGroundedCriticAcceptsClaimsWithValidEvidenceRefs(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "analyze", Title: "Analyze", Description: "Analyze evidence limits.",
		Deliverables: []string{"grounded analysis"}, AcceptanceCriteria: []string{"claims cite inspected source"}, VerificationStrategy: "fixed",
	})
	target.CriticEnabled = true
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolResult{
		ToolCallID: "read-1", Name: "read", Content: "342 if len(value) > 8192 {",
	}}}}); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("Evidence summaries are limited to 8192 bytes."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	model := &staticCriticModel{content: `{"passed":true,"summary":"claim is grounded","supported_claims":[{"claim":"the limit is 8192 bytes","evidence_refs":["E1"]}],"unsupported_claims":[]}`}
	ctx := WithVerifier(core.WithModel(context.Background(), model), func(_ context.Context, _ VerificationRequest) (VerificationResult, error) {
		return VerificationResult{Status: VerificationStatusPassed, Summary: "deterministic check passed"}, nil
	})
	if _, err := target.Execute(core.NewContext(ctx), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusPassed || !strings.Contains(step.VerificationSummary, "E1") || step.ModelCalls != 1 {
		t.Fatalf("step = %#v", step)
	}
}

func TestDeterministicFailureSkipsGroundedCritic(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "verify", Title: "Verify", Description: "Verify the result.",
		Deliverables: []string{"verified result"}, AcceptanceCriteria: []string{"verification passes"}, VerificationStrategy: "fixed",
	})
	target.CriticEnabled = true
	_ = conversation.SetFinalAnswer("Claimed success.")
	model := &staticCriticModel{content: `{"passed":true,"summary":"ignored","supported_claims":[{"claim":"ignored","evidence_refs":["E1"]}],"unsupported_claims":[]}`}
	ctx := WithVerifier(core.WithModel(context.Background(), model), func(_ context.Context, _ VerificationRequest) (VerificationResult, error) {
		return VerificationResult{Status: VerificationStatusRetry, Summary: "deterministic verifier failed", Retryable: true}, nil
	})
	if _, err := target.Execute(core.NewContext(ctx), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || model.calls != 0 {
		t.Fatalf("step = %#v, critic calls = %d", step, model.calls)
	}
}

func TestVerifierBuildsFromDSLConfiguration(t *testing.T) {
	target, err := VerifierNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "verify", Config: map[string]any{
			"verifier_id": "fixed", "max_attempts": 3, "minimum_evidence": 2, "max_evidence": 8, "allow_no_op": false, "require_test_evidence": true, "config": map[string]any{"mode": "strict"},
			"critic_enabled": true, "critic_model_id": "critic", "critic_prompt": "ground every claim",
		}},
		State: map[string]registry.ResolvedStateBinding{
			"plan":         {Path: state.Shared("custom", "plan")},
			"execution":    {Path: state.Shared("custom", "execution")},
			"conversation": {Path: state.Scope("worker", "conversation")},
		},
	})
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	verifier := target.(*VerifierNode)
	if verifier.VerifierID != "fixed" || verifier.MaxAttempts != 3 || verifier.MinimumEvidence != 2 || verifier.MaxEvidence != 8 || verifier.AllowNoOp || !verifier.RequireTestEvidence || verifier.VerifierConfig["mode"] != "strict" || !verifier.CriticEnabled || verifier.CriticModelID != "critic" || verifier.CriticPrompt != "ground every claim" {
		t.Fatalf("verifier = %#v", verifier)
	}
	if verifier.PlanPath.String() != "shared.custom.plan" || verifier.ConversationPath.String() != "scopes.worker.conversation" {
		t.Fatalf("paths = %s / %s", verifier.PlanPath.String(), verifier.ConversationPath.String())
	}
}

type staticCriticModel struct {
	content string
	calls   int
}

func (model *staticCriticModel) Generate(_ context.Context, _ llms.ModelRequest) (*llms.ModelResponse, error) {
	model.calls++
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: model.content}}}, nil
}

func verifierFixture(t *testing.T, step plancap.Step) (*VerifierNode, *state.Access, *conversationcap.View) {
	t.Helper()
	target := NewVerifierNode(core.WithID("verify"))
	target.VerifierID = "fixed"
	access := state.NewEditingAccess(state.NewState())
	planner, err := plancap.Bind(access, target.PlanPath)
	if err != nil {
		t.Fatalf("bind plan: %v", err)
	}
	if err := planner.SetField(plancap.FieldObjective, "complete the task"); err != nil {
		t.Fatalf("set objective: %v", err)
	}
	if err := planner.SetField(plancap.FieldCurrentIndex, 0); err != nil {
		t.Fatalf("set current index: %v", err)
	}
	if err := planner.SetSteps([]plancap.Step{step}); err != nil {
		t.Fatalf("set steps: %v", err)
	}
	conversation, err := conversationcap.Bind(access, target.ConversationPath)
	if err != nil {
		t.Fatalf("bind conversation: %v", err)
	}
	return target, access, conversation
}

func currentVerifierStep(t *testing.T, target *VerifierNode, access *state.Access) plancap.Step {
	t.Helper()
	planner, _ := plancap.Bind(access, target.PlanPath)
	steps := planner.Steps()
	if len(steps) != 1 {
		t.Fatalf("steps = %#v", steps)
	}
	return steps[0]
}
