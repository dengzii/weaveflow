package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/node/plan"
	"github.com/dengzii/weaveflow/state"
)

func TestPlanStepIterationConditionStopsAtLimit(t *testing.T) {
	condition, _ := hasPlanStepIterationsRemaining(planConversationPath)
	tests := []struct {
		name       string
		iterations int
		maximum    int
		want       bool
	}{
		{name: "below limit", iterations: 2, maximum: 3, want: true},
		{name: "at limit", iterations: 3, maximum: 3, want: false},
		{name: "above limit", iterations: 4, maximum: 3, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := state.NewEditingAccess(state.NewState())
			conversation, err := conversationcap.Bind(access, planConversationPath)
			if err != nil {
				t.Fatalf("bind conversation: %v", err)
			}
			if err := conversation.SetIterationCount(test.iterations); err != nil {
				t.Fatalf("set iterations: %v", err)
			}
			if err := conversation.SetMaxIterations(test.maximum); err != nil {
				t.Fatalf("set max iterations: %v", err)
			}
			decision, err := condition.EvaluateRoute(context.Background(), access.State())
			if err != nil {
				t.Fatalf("evaluate condition: %v", err)
			}
			if decision.Matched != test.want {
				t.Fatalf("matched = %t, want %t", decision.Matched, test.want)
			}
		})
	}
}

func TestNewPlanGraphUsesProfileConfiguration(t *testing.T) {
	tinyScript, _ := profileByID("tiny-script")
	analysis, _ := profileByID("analysis")
	tinyGraph, err := newPlanGraph(tinyScript)
	if err != nil {
		t.Fatalf("tiny graph: %v", err)
	}
	analysisGraph, err := newPlanGraph(analysis)
	if err != nil {
		t.Fatalf("analysis graph: %v", err)
	}
	if _, err := tinyGraph.Compile(); err != nil {
		t.Fatalf("compile tiny graph: %v", err)
	}
	if _, err := analysisGraph.Compile(); err != nil {
		t.Fatalf("compile analysis graph: %v", err)
	}
	tinyGenerator := tinyGraph.NodeSpecs()["generate_plan"].Config
	analysisGenerator := analysisGraph.NodeSpecs()["generate_plan"].Config
	if reflect.DeepEqual(tinyGenerator["tool_ids"], analysisGenerator["tool_ids"]) {
		t.Fatal("profiles have identical planner tools")
	}
	if tinyGenerator["max_steps"] != tinyScript.MaxSteps || analysisGenerator["max_steps"] != analysis.MaxSteps {
		t.Fatalf("max steps were not injected: tiny=%v analysis=%v", tinyGenerator["max_steps"], analysisGenerator["max_steps"])
	}
	if tinyGenerator["system_prompt"] == analysisGenerator["system_prompt"] && tinyScript.PlannerPrompt != analysis.PlannerPrompt {
		t.Fatal("profile planner prompts were not injected")
	}
	if _, ok := analysisGraph.NodeSpecs()["verify_step"]; !ok {
		t.Fatal("plan verifier node is missing")
	}
	analysisVerifier := analysisGraph.NodeSpecs()["verify_step"].Config
	if analysisVerifier["critic_enabled"] != true || strings.TrimSpace(analysisVerifier["critic_prompt"].(string)) == "" {
		t.Fatalf("analysis critic config = %#v", analysisVerifier)
	}
	if analysisGraph.NodeSpecs()["synthesize_plan"].Config["require_evidence_refs"] != true {
		t.Fatal("analysis synthesis does not require evidence refs")
	}
}

func TestProfileAndVerifierRegistriesAllowExtensions(t *testing.T) {
	verifiers := NewVerifierRegistry()
	if err := verifiers.Register("fixture", func(VerifierConfig) (Verifier, error) {
		return verifierFunc{id: "fixture", verify: func(context.Context, plan.VerificationRequest) (plan.VerificationResult, error) {
			return plan.VerificationResult{Status: plan.VerificationStatusPassed, Summary: "fixture passed"}, nil
		}}, nil
	}); err != nil {
		t.Fatalf("register verifier: %v", err)
	}
	if _, err := verifiers.Build("fixture", VerifierConfig{}); err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	if err := verifiers.Register("fixture", func(VerifierConfig) (Verifier, error) { return nil, nil }); err == nil {
		t.Fatal("duplicate verifier registration was accepted")
	}

	base, err := profileByID("analysis")
	if err != nil {
		t.Fatalf("analysis profile: %v", err)
	}
	base.ID = "fixture-profile"
	base.Description = "Registry test profile"
	base.VerifierID = "fixture"
	registry := NewProfileRegistry(verifiers)
	if err := registry.Register(base); err != nil {
		t.Fatalf("register profile: %v", err)
	}
	loaded, err := registry.Lookup("fixture-profile")
	if err != nil || loaded.VerifierID != "fixture" {
		t.Fatalf("loaded profile = %#v, err = %v", loaded, err)
	}
	if err := registry.Register(base); err == nil {
		t.Fatal("duplicate profile registration was accepted")
	}
}

func TestProfilesExposeDistinctAuthorizedToolsAndBudgetsToFakeModel(t *testing.T) {
	tinyScript, _ := profileByID("tiny-script")
	analysis, _ := profileByID("analysis")
	multiStep, _ := profileByID("multi-step")
	tinyRequest := executeGeneratorWithCapture(t, tinyScript)
	analysisRequest := executeGeneratorWithCapture(t, analysis)
	tinyPrompt := requestText(tinyRequest)
	analysisPrompt := requestText(analysisRequest)
	if !strings.Contains(tinyPrompt, `"id": "write"`) {
		t.Fatalf("tiny prompt lacks write tool: %s", tinyPrompt)
	}
	if strings.Contains(analysisPrompt, `"id": "write"`) || strings.Contains(analysisPrompt, `"id": "verify"`) {
		t.Fatalf("analysis prompt contains unauthorized tools: %s", analysisPrompt)
	}
	if tinyRequest.Messages[0].Parts[0].(llms.TextContent).Text != tinyScript.PlannerPrompt {
		t.Fatal("tiny planner prompt mismatch")
	}
	if analysisRequest.Messages[0].Parts[0].(llms.TextContent).Text != analysis.PlannerPrompt {
		t.Fatal("analysis planner prompt mismatch")
	}
	if tinyScript.MaxSteps == analysis.MaxSteps || tinyScript.MaxIterations == analysis.MaxIterations {
		t.Fatalf("profile budgets are not distinct: tiny=%d/%d analysis=%d/%d", tinyScript.MaxSteps, tinyScript.MaxIterations, analysis.MaxSteps, analysis.MaxIterations)
	}
	if multiStep.MaxSteps <= analysis.MaxSteps || multiStep.MaxIterations <= analysis.MaxIterations {
		t.Fatalf("multi-step profile does not expose a larger budget: multi=%d/%d analysis=%d/%d", multiStep.MaxSteps, multiStep.MaxIterations, analysis.MaxSteps, analysis.MaxIterations)
	}
}

func TestWorkerRequestOmitsUnauthorizedTools(t *testing.T) {
	analysis, _ := profileByID("analysis")
	tinyScript, _ := profileByID("tiny-script")
	tinyScript.AllowedPaths = []string{"."}
	allTools, err := toolsForProfile(tinyScript, t.TempDir())
	if err != nil {
		t.Fatalf("all tools: %v", err)
	}
	model := &capturingPlanModel{}
	target := basenode.NewLLMTurnNode(basenode.WithID("worker"))
	target.ToolIDs = append([]string(nil), analysis.ToolIDs...)
	access := state.NewEditingAccess(state.NewState())
	conversation, _ := conversationcap.Bind(access, target.ConversationPath)
	_ = conversation.SetMessages([]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "inspect")})
	_ = conversation.SetMaxIterations(analysis.MaxIterations)
	ctx := core.NewContext(core.WithTools(core.WithModel(context.Background(), model), allTools))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute worker: %v", err)
	}
	for _, definition := range model.request.Tools {
		if definition.Function != nil && (definition.Function.Name == "write" || definition.Function.Name == "edit" || definition.Function.Name == "verify") {
			t.Fatalf("worker request contains unauthorized tool %q", definition.Function.Name)
		}
	}
}

func TestTaskProfileRejectsUnsafeConfiguration(t *testing.T) {
	valid, _ := profileByID("analysis")
	tests := []struct {
		name   string
		mutate func(*TaskProfile)
	}{
		{name: "empty id", mutate: func(profile *TaskProfile) { profile.ID = "" }},
		{name: "unknown tool", mutate: func(profile *TaskProfile) { profile.ToolIDs = append(profile.ToolIDs, "bash") }},
		{name: "invalid budget", mutate: func(profile *TaskProfile) { profile.MaxIterations = 0 }},
		{name: "missing verifier", mutate: func(profile *TaskProfile) { profile.VerifierID = "" }},
		{name: "dangerous process permission", mutate: func(profile *TaskProfile) { profile.Permissions = append(profile.Permissions, "process.execute") }},
		{name: "no-op write", mutate: func(profile *TaskProfile) { profile.Permissions = append(profile.Permissions, "filesystem.write") }},
		{name: "missing critic prompt", mutate: func(profile *TaskProfile) { profile.CriticPrompt = "" }},
		{name: "invalid read limit", mutate: func(profile *TaskProfile) { profile.MaxReadLines = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := valid
			test.mutate(&profile)
			if err := profile.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAnalysisReadToolUsesProfileBoundsAndOutline(t *testing.T) {
	profile, _ := profileByID("analysis")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "long.txt"), []byte(strings.Repeat("value\n", 300)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	available, err := toolsForProfile(profile, workspace)
	if err != nil {
		t.Fatalf("profile tools: %v", err)
	}
	if _, ok := available["outline"]; !ok {
		t.Fatal("analysis profile lacks outline tool")
	}
	readTool := available["read"]
	properties := readTool.Function.Parameters["properties"].(map[string]any)
	limitSchema := properties["limit"].(map[string]any)
	if limitSchema["maximum"] != profile.MaxReadLines {
		t.Fatalf("read limit schema = %#v", limitSchema)
	}
	ctx := core.WithEnvironment(context.Background(), map[string]string{"WEAVEFLOW_TOOL_WORKDIR": workspace})
	ctx = core.WithToolPermissions(ctx, profile.Permissions...)
	result, err := core.ExecuteTool(ctx, readTool, toolCall("read", map[string]any{"file_path": "long.txt"}))
	if err != nil {
		t.Fatalf("bounded read: %v", err)
	}
	if !strings.Contains(result.Content, "240\tvalue") || strings.Contains(result.Content, "241\tvalue") {
		t.Fatalf("bounded read output = %q", result.Content)
	}
	if _, err := core.ExecuteTool(ctx, readTool, toolCall("read", map[string]any{"file_path": "long.txt", "limit": profile.MaxReadLines + 1})); err == nil {
		t.Fatal("read accepted a limit above the profile maximum")
	}
}

func TestParseCLIObjectiveCompatibility(t *testing.T) {
	options, err := parseCLI([]string{"-profile", "analysis", "inspect", "the", "graph"})
	if err != nil {
		t.Fatalf("parse CLI: %v", err)
	}
	if options.ProfileID != "analysis" || options.Objective != "inspect the graph" || options.DataDir != ".local/plan_mode" {
		t.Fatalf("options = %#v", options)
	}
	if _, err := parseCLI([]string{"-objective", "one", "two"}); err == nil {
		t.Fatal("expected conflicting objective error")
	}
}

func TestVerificationToolSchemas(t *testing.T) {
	profile, _ := profileByID("tiny-script")
	verifier, _ := newVerifier(profile.VerifierID, profile.VerifierConfig)
	tool := newVerificationTool(verifier)
	if err := state.ValidateJSONSchemaDefinition(tool.Function.Parameters); err != nil {
		t.Fatalf("input schema: %v", err)
	}
	if err := state.ValidateJSONSchemaDefinition(tool.Function.OutputSchema); err != nil {
		t.Fatalf("output schema: %v", err)
	}
}

func TestProfileAllowlistRejectsWorkspaceSiblingsAndUnscopedSearch(t *testing.T) {
	workspace := t.TempDir()
	allowed := filepath.Join(workspace, "allowed")
	blocked := filepath.Join(workspace, "blocked")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatalf("allowed directory: %v", err)
	}
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("blocked directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(allowed, "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("allowed fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("blocked fixture: %v", err)
	}
	profile, err := profileByID("analysis")
	if err != nil {
		t.Fatalf("analysis profile: %v", err)
	}
	profile.AllowedPaths = []string{"allowed"}
	available, err := toolsForProfile(profile, workspace)
	if err != nil {
		t.Fatalf("profile tools: %v", err)
	}
	ctx := core.WithEnvironment(context.Background(), map[string]string{"WEAVEFLOW_TOOL_WORKDIR": workspace})
	ctx = core.WithToolPermissions(ctx, profile.Permissions...)
	if _, err := core.ExecuteTool(ctx, available["read"], toolCall("read", map[string]any{"file_path": "blocked/outside.txt"})); err == nil {
		t.Fatal("read outside allowlist was accepted")
	}
	if _, err := core.ExecuteTool(ctx, available["read"], toolCall("read", map[string]any{"file_path": "allowed/inside.txt"})); err != nil {
		t.Fatalf("read inside allowlist: %v", err)
	}
	if _, err := core.ExecuteTool(ctx, available["glob"], toolCall("glob", map[string]any{"pattern": "*.txt"})); err == nil {
		t.Fatal("unscoped glob was accepted")
	}
}

func TestVerifierRejectsPackageInjection(t *testing.T) {
	_, err := newVerifier("go-test", VerifierConfig{Packages: []string{"./...; touch /tmp/bad"}, AllowedPackages: []string{"./...; touch /tmp/bad"}})
	if err == nil {
		t.Fatal("expected package injection rejection")
	}
}

func TestVerificationToolRejectsAdditionalArguments(t *testing.T) {
	profile, _ := profileByID("documentation")
	verifier, _ := newVerifier(profile.VerifierID, profile.VerifierConfig)
	tool := newVerificationTool(verifier)
	ctx := core.WithToolPermissions(context.Background(), "filesystem.read")
	ctx = core.WithToolApprover(ctx, core.ToolApproverFunc(func(context.Context, core.ToolApprovalRequest) (core.ToolApprovalDecision, error) {
		return core.ToolApprovalDecision{Approved: true}, nil
	}))
	if _, err := core.ExecuteTool(ctx, tool, toolCall("verify", map[string]any{"command": "go test ./..."})); err == nil {
		t.Fatal("verify accepted an extra command argument")
	}
}

func TestProfileApproverDeniesUnconfiguredTool(t *testing.T) {
	profile, _ := profileByID("documentation")
	decision, err := profileApprover(profile).Approve(context.Background(), core.ToolApprovalRequest{ToolCall: toolCall("unknown", map[string]any{})})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if decision.Approved {
		t.Fatal("unconfigured tool was approved")
	}
}

func TestNoOpVerifierRejectsCodingObjective(t *testing.T) {
	verifier, err := newVerifier("no-op", VerifierConfig{AnalysisOnly: true})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	result, err := verifier.Verify(context.Background(), plan.VerificationRequest{
		Objective: "Implement a new Go function.",
		Evidence:  []plancap.Evidence{{ToolID: "read", Status: "succeeded"}},
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Status != plan.VerificationStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
}

func TestRetryingModelEnforcesRetryAndTimeoutBudgets(t *testing.T) {
	transient := &budgetModel{failures: 1}
	model := retryingModel{model: transient, attempts: 2, timeout: time.Second}
	if _, err := model.Generate(context.Background(), llms.ModelRequest{}); err != nil {
		t.Fatalf("retrying model: %v", err)
	}
	if transient.calls != 2 {
		t.Fatalf("calls = %d, want 2", transient.calls)
	}
	blocked := &budgetModel{failures: 3}
	model = retryingModel{model: blocked, attempts: 2, timeout: time.Second}
	if _, err := model.Generate(context.Background(), llms.ModelRequest{}); err == nil {
		t.Fatal("expected exhausted retry error")
	}
	if blocked.calls != 2 {
		t.Fatalf("exhausted calls = %d, want 2", blocked.calls)
	}
	timed := &budgetModel{wait: 50 * time.Millisecond}
	model = retryingModel{model: timed, attempts: 1, timeout: time.Millisecond}
	if _, err := model.Generate(context.Background(), llms.ModelRequest{}); err == nil {
		t.Fatal("expected model call timeout")
	}
}

func executeGeneratorWithCapture(t *testing.T, profile TaskProfile) llms.ModelRequest {
	t.Helper()
	profile.AllowedPaths = []string{"."}
	model := &capturingPlanModel{}
	target := plan.NewGeneratorNode(core.WithID("generator"))
	target.ToolIDs = append([]string(nil), profile.ToolIDs...)
	target.SystemPrompt = profile.PlannerPrompt
	target.MaxSteps = profile.MaxSteps
	target.MaxReplans = profile.MaxReplans
	available, err := toolsForProfile(profile, t.TempDir())
	if err != nil {
		t.Fatalf("profile tools: %v", err)
	}
	access := state.NewEditingAccess(state.FromShared(map[string]any{"request": map[string]any{"input": "inspect"}}))
	ctx := core.NewContext(core.WithTools(core.WithModel(context.Background(), model), available))
	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute generator: %v", err)
	}
	return model.request
}

type capturingPlanModel struct {
	request llms.ModelRequest
}

type budgetModel struct {
	calls    int
	failures int
	wait     time.Duration
}

func (model *budgetModel) Generate(ctx context.Context, _ llms.ModelRequest) (*llms.ModelResponse, error) {
	model.calls++
	if model.wait > 0 {
		select {
		case <-time.After(model.wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if model.calls <= model.failures {
		return nil, core.NewExecutionError(core.ErrorUnavailable, "transient model failure", errors.New("provider unavailable"), nil)
	}
	return modelResponse("ok"), nil
}

func (model *capturingPlanModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	model.request = request
	content := `{"summary":"inspect","steps":[{"id":"step_1","title":"Inspect","description":"Inspect the files.","tool_ids":["read"],"deliverables":["analysis"],"acceptance_criteria":["analysis cites inspected files"],"verification_strategy":"evidence"}]}`
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: content}}}, nil
}

func requestText(request llms.ModelRequest) string {
	var builder strings.Builder
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				builder.WriteString(text.Text)
			}
		}
	}
	return builder.String()
}

func toolCall(name string, arguments map[string]any) llms.ToolCall {
	encoded, _ := json.Marshal(arguments)
	return llms.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: name, Arguments: encoded}}
}
