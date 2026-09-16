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
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "no claim-supporting tool evidence") {
		t.Fatalf("verification = %s %q", step.VerificationStatus, step.VerificationSummary)
	}
}

func TestVerifierDefaultsToGroundedCritic(t *testing.T) {
	target := NewVerifierNode(core.WithID("verify"))
	if !target.CriticEnabled {
		t.Fatal("grounded critic is disabled by default")
	}
	properties := VerifierNodeTypeDefinition().ConfigSchema["properties"].(dsl.JSONSchema)
	criticSchema := properties["critic_enabled"].(dsl.JSONSchema)
	if criticSchema["default"] != true {
		t.Fatalf("critic_enabled schema default = %#v", criticSchema["default"])
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
	target.MinimumEvidence = 1
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
		llms.ToolResult{ToolCallID: "write-1", Name: "write", IsError: true, ErrorMessage: "permission denied"},
		llms.ToolResult{ToolCallID: "read-1", Name: "read", Content: "existing file contents"},
	}}}); err != nil {
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

func TestVerifierRejectsNonSuccessfulHTTPEvidence(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "research", Title: "Research", Description: "Fetch a source.",
		Deliverables: []string{"source-backed result"}, AcceptanceCriteria: []string{"source is reachable"}, VerificationStrategy: "evidence",
	})
	target.MinimumEvidence = 1
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolResult{
		ToolCallID: "fetch-403", Name: "web_fetch", Value: map[string]any{"url": "https://example.com/blocked", "status": 403, "title": "Blocked"},
	}}}}); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("The source confirms the result."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "HTTP status 403") {
		t.Fatalf("verification = %s %q; evidence = %#v", step.VerificationStatus, step.VerificationSummary, step.Evidence)
	}
	if len(step.Evidence) != 1 || step.Evidence[0].URL != "https://example.com/blocked" || step.Evidence[0].HTTPStatus != 403 {
		t.Fatalf("structured evidence = %#v", step.Evidence)
	}
}

func TestVerifierAllowsRetryToSupersedeUnavailableWebSource(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "research", Title: "Research", Description: "Fetch independent sources.",
		Deliverables: []string{"source-backed result"}, AcceptanceCriteria: []string{"two independent sources agree"}, VerificationStrategy: "evidence",
	})
	target.MinimumEvidence = 2
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
		llms.ToolResult{ToolCallID: "fetch-403", Name: "web_fetch", Value: map[string]any{"url": "https://blocked.example.com/source", "status": 403}},
	}}}); err != nil {
		t.Fatalf("set first messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("The first source was unavailable."); err != nil {
		t.Fatalf("set first final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("first verification: %v", err)
	}

	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
		llms.ToolResult{ToolCallID: "fetch-1", Name: "web_fetch", Value: map[string]any{"url": "https://one.example.com/source", "status": 200}},
		llms.ToolResult{ToolCallID: "fetch-2", Name: "web_fetch", Value: map[string]any{"url": "https://two.example.com/source", "status": 200}},
	}}}); err != nil {
		t.Fatalf("set retry messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("Two independent sources support the result."); err != nil {
		t.Fatalf("set retry final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("retry verification: %v", err)
	}

	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusPassed || step.VerificationAttempts != 2 {
		t.Fatalf("verification = %s attempts = %d summary = %q", step.VerificationStatus, step.VerificationAttempts, step.VerificationSummary)
	}
	if len(step.Evidence) != 3 || len(step.AttemptHistory) != 2 {
		t.Fatalf("evidence = %#v; history = %#v", step.Evidence, step.AttemptHistory)
	}
	if !strings.Contains(step.VerificationSummary, "1 unavailable source attempt") {
		t.Fatalf("verification summary = %q", step.VerificationSummary)
	}
}

func TestVerifierRequiresIndependentSourceURLs(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "research", Title: "Research", Description: "Fetch independent sources.",
		Deliverables: []string{"source-backed result"}, AcceptanceCriteria: []string{"two sources agree"}, VerificationStrategy: "evidence",
	})
	target.MinimumEvidence = 2
	messages := []llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
		llms.ToolResult{ToolCallID: "fetch-1", Name: "web_fetch", Value: map[string]any{"url": "https://example.com/source", "status": 200}},
		llms.ToolResult{ToolCallID: "fetch-2", Name: "web_fetch", Value: map[string]any{"url": "https://example.com/source/", "status": 200}},
	}}}
	if err := conversation.SetMessages(messages); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("Two sources support the result."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "at least 2 claim-supporting evidence") {
		t.Fatalf("verification = %s %q", step.VerificationStatus, step.VerificationSummary)
	}
	if len(step.Evidence) != 1 {
		t.Fatalf("canonical URL duplicates were retained: %#v", step.Evidence)
	}
}

func TestVerifierDoesNotCountSearchOrClockMetadataAsEvidence(t *testing.T) {
	target, access, conversation := verifierFixture(t, plancap.Step{
		ID: "research", Title: "Research", Description: "Explain a topic from fetched sources.",
		Deliverables: []string{"source-backed answer"}, AcceptanceCriteria: []string{"material claims cite fetched source URLs"}, VerificationStrategy: "evidence",
	})
	target.MinimumEvidence = 1
	if err := conversation.SetMessages([]llms.MessageContent{{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
		llms.ToolResult{ToolCallID: "search-1", Name: "web_search", Content: "Title: uninspected result URL: https://example.com"},
		llms.ToolResult{ToolCallID: "time-1", Name: "current_time", Content: "2026-09-11T07:42:03Z"},
	}}}); err != nil {
		t.Fatalf("set messages: %v", err)
	}
	if err := conversation.SetFinalAnswer("The snippets prove the answer."); err != nil {
		t.Fatalf("set final answer: %v", err)
	}
	if _, err := target.Execute(core.NewContext(context.Background()), access); err != nil {
		t.Fatalf("execute verifier: %v", err)
	}
	step := currentVerifierStep(t, target, access)
	if step.VerificationStatus != VerificationStatusRetry || !strings.Contains(step.VerificationSummary, "claim-supporting") {
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

func TestGroundedCriticPreservesEvidencePositionsAfterFiltering(t *testing.T) {
	payload, validRefs, err := groundedCriticPayload(VerificationRequest{
		Objective: "explain the architecture",
		Step:      plancap.Step{ID: "research", Title: "Research"},
		Evidence: []plancap.Evidence{
			{ToolID: "web_search", Status: "succeeded", Summary: "search lead"},
			{ToolID: "web_fetch", Status: "succeeded", Summary: "official source", URL: "https://example.com/official", HTTPStatus: 200},
			{ToolID: "web_fetch", Status: "succeeded", Summary: "duplicate official source", URL: "https://EXAMPLE.com/official#copy", HTTPStatus: 200},
		},
	})
	if err != nil {
		t.Fatalf("build critic payload: %v", err)
	}
	if _, ok := validRefs["E2"]; !ok {
		t.Fatalf("valid refs = %#v", validRefs)
	}
	if _, ok := validRefs["E3"]; ok || !strings.Contains(payload, `"ref": "E2"`) || strings.Contains(payload, "search lead") || strings.Contains(payload, "duplicate official source") {
		t.Fatalf("payload = %s; valid refs = %#v", payload, validRefs)
	}
	if !strings.Contains(payload, `"url": "https://example.com/official"`) || !strings.Contains(payload, `"http_status": 200`) {
		t.Fatalf("payload lacks explicit source metadata: %s", payload)
	}
}

func TestMergeEvidenceReplacesCanonicalURLRetryWithoutRenumbering(t *testing.T) {
	existing := []plancap.Evidence{
		{ToolID: "web_search", ToolCallID: "search-1", Status: "succeeded", Summary: "lead"},
		{ToolID: "web_fetch", ToolCallID: "fetch-1", Status: "failed", Error: "timeout", URL: "https://Example.com/source#old"},
	}
	incoming := []plancap.Evidence{
		{ToolID: "web_fetch", ToolCallID: "fetch-2", Status: "succeeded", Summary: "recovered", URL: "https://example.com/source", HTTPStatus: 200},
		{ToolID: "web_fetch", ToolCallID: "fetch-3", Status: "succeeded", Summary: "second", URL: "https://example.com/other", HTTPStatus: 200},
	}
	got := mergeEvidenceLimit(existing, incoming, 8)
	if len(got) != 3 {
		t.Fatalf("evidence = %#v", got)
	}
	if got[1].ToolCallID != "fetch-2" || got[1].Summary != "recovered" || got[2].ToolCallID != "fetch-3" {
		t.Fatalf("evidence = %#v", got)
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
	target.CriticEnabled = false
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
