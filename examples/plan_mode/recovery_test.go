package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dengzii/weaveflow"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
	plannode "github.com/dengzii/weaveflow/node/plan"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestLocalRunnerRestoresAfterVerifierWithoutRepeatingStep(t *testing.T) {
	profile, err := profileByID("analysis")
	if err != nil {
		t.Fatalf("analysis profile: %v", err)
	}
	graph, err := newPlanGraph(profile)
	if err != nil {
		t.Fatalf("new graph: %v", err)
	}
	workspace := repositoryRoot(t)
	available, err := toolsForProfile(profile, workspace)
	if err != nil {
		t.Fatalf("profile tools: %v", err)
	}
	model := &recoveryPlanModel{}
	verifier, err := newVerifier(profile.VerifierID, profile.VerifierConfig)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, available)
	ctx = core.WithEnvironment(ctx, map[string]string{"WEAVEFLOW_TOOL_WORKDIR": workspace})
	ctx = core.WithToolPermissions(ctx, profile.Permissions...)
	ctx = core.WithToolApprover(ctx, profileApprover(profile))
	ctx = plannode.WithVerifier(ctx, verifier.Verify)
	dataDir := t.TempDir()
	options := []weaveflow.RunnerOption{
		weaveflow.WithGraphID("examples.plan_mode.analysis"),
		weaveflow.WithGraphVersion("1.0"),
		weaveflow.WithBreakpoints(runtime.Breakpoint{ID: "after-verifier", NodeID: "verify_step", Stage: string(runtime.CheckpointAfterNode), Enabled: true}),
	}
	runner, err := weaveflow.NewLocalRunner(graph, dataDir, options...)
	if err != nil {
		t.Fatalf("new local runner: %v", err)
	}
	run, pausedState, err := runner.Start(ctx, state.FromShared(map[string]any{"request": map[string]any{"input": profile.DefaultObjective}}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if run.Status != runtime.RunStatusPaused {
		t.Fatalf("status = %s, want paused", run.Status)
	}
	assertVerifiedAnalysisStep(t, pausedState, 1)
	if model.calls != 4 {
		t.Fatalf("model calls before restart = %d, want 4", model.calls)
	}
	if err := runner.Close(); err != nil {
		t.Fatalf("close first runner: %v", err)
	}

	restarted, err := weaveflow.NewLocalRunner(graph, dataDir, options...)
	if err != nil {
		t.Fatalf("restart local runner: %v", err)
	}
	defer restarted.Close()
	resumable, err := restarted.GetResumableRun(ctx)
	if err != nil {
		t.Fatalf("get resumable: %v", err)
	}
	if resumable == nil || resumable.RunID != run.RunID {
		t.Fatalf("resumable = %#v, want %s", resumable, run.RunID)
	}
	completed, finalState, err := restarted.Resume(ctx, run.RunID, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if completed.Status != runtime.RunStatusCompleted {
		t.Fatalf("status = %s, want completed", completed.Status)
	}
	assertVerifiedAnalysisStep(t, finalState, 1)
	if model.calls != 5 {
		t.Fatalf("model calls after resume = %d, want 5; verified step was repeated", model.calls)
	}
	planValue, _ := state.ReadPath(finalState, planStatePath.String())
	mapped, _ := planValue.(map[string]any)
	if mapped[plancap.FieldStatus] != plannode.PlanStatusDone {
		t.Fatalf("plan status = %v, want done", mapped[plancap.FieldStatus])
	}
}

func TestLocalRunnerDoesNotRepeatSuccessfulWriteAfterRestart(t *testing.T) {
	profile, _ := profileByID("documentation")
	profile.DefaultObjective = "Create result.md with the required recovery marker."
	profile.VerifierConfig = VerifierConfig{Files: []string{"result.md"}, Contains: []string{"recovery-marker"}}
	graph, err := newPlanGraph(profile)
	if err != nil {
		t.Fatalf("new graph: %v", err)
	}
	workspace := t.TempDir()
	available, err := toolsForProfile(profile, workspace)
	if err != nil {
		t.Fatalf("profile tools: %v", err)
	}
	writes := 0
	writeTool := available["write"]
	writeHandler := writeTool.Handler
	writeTool.Handler = func(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
		writes++
		return writeHandler(ctx, call)
	}
	available["write"] = writeTool
	model := &writeRecoveryModel{}
	verifier, _ := newVerifier(profile.VerifierID, profile.VerifierConfig)
	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, available)
	ctx = core.WithEnvironment(ctx, map[string]string{"WEAVEFLOW_TOOL_WORKDIR": workspace})
	ctx = core.WithToolPermissions(ctx, profile.Permissions...)
	ctx = core.WithToolApprover(ctx, profileApprover(profile))
	ctx = plannode.WithVerifier(ctx, verifier.Verify)
	dataDir := t.TempDir()
	options := []weaveflow.RunnerOption{
		weaveflow.WithGraphID("examples.plan_mode.documentation"), weaveflow.WithGraphVersion("1.0"),
		weaveflow.WithBreakpoints(runtime.Breakpoint{ID: "after-tools", NodeID: "execute_tools", Stage: string(runtime.CheckpointAfterNode), Enabled: true}),
	}
	runner, err := weaveflow.NewLocalRunner(graph, dataDir, options...)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	run, _, err := runner.Start(ctx, state.FromShared(map[string]any{"request": map[string]any{"input": profile.DefaultObjective}}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if run.Status != runtime.RunStatusPaused || writes != 1 {
		t.Fatalf("status = %s, writes = %d", run.Status, writes)
	}
	if err := runner.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	restarted, err := weaveflow.NewLocalRunner(graph, dataDir, options...)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer restarted.Close()
	completed, finalState, err := restarted.Resume(ctx, run.RunID, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if completed.Status != runtime.RunStatusCompleted || writes != 1 {
		t.Fatalf("status = %s, writes = %d; successful write repeated", completed.Status, writes)
	}
	content, err := os.ReadFile(filepath.Join(workspace, "result.md"))
	if err != nil || string(content) != "recovery-marker\n" {
		t.Fatalf("result.md = %q, err = %v", content, err)
	}
	assertVerifiedAnalysisStep(t, finalState, 1)
}

func TestLocalRunnerCanceledWhilePausedPersistsCanceledStatus(t *testing.T) {
	profile, _ := profileByID("analysis")
	graph, err := newPlanGraph(profile)
	if err != nil {
		t.Fatalf("new graph: %v", err)
	}
	workspace := repositoryRoot(t)
	available, _ := toolsForProfile(profile, workspace)
	model := &recoveryPlanModel{}
	verifier, _ := newVerifier(profile.VerifierID, profile.VerifierConfig)
	ctx := core.WithModel(context.Background(), model)
	ctx = core.WithTools(ctx, available)
	ctx = core.WithEnvironment(ctx, map[string]string{"WEAVEFLOW_TOOL_WORKDIR": workspace})
	ctx = core.WithToolPermissions(ctx, profile.Permissions...)
	ctx = core.WithToolApprover(ctx, profileApprover(profile))
	ctx = plannode.WithVerifier(ctx, verifier.Verify)
	runner, err := weaveflow.NewLocalRunner(graph, t.TempDir(),
		weaveflow.WithGraphID("examples.plan_mode.analysis"), weaveflow.WithGraphVersion("1.0"),
		weaveflow.WithBreakpoints(runtime.Breakpoint{ID: "after-generator", NodeID: "generate_plan", Stage: string(runtime.CheckpointAfterNode), Enabled: true}),
	)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer runner.Close()
	run, _, err := runner.Start(ctx, state.FromShared(map[string]any{"request": map[string]any{"input": profile.DefaultObjective}}))
	if err != nil || run.Status != runtime.RunStatusPaused {
		t.Fatalf("start status = %s, err = %v", run.Status, err)
	}
	if err := runner.Cancel(ctx, run.RunID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	canceled, err := runner.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("get canceled run: %v", err)
	}
	if canceled.Status != runtime.RunStatusCanceled {
		t.Fatalf("status = %s, want canceled", canceled.Status)
	}
}

type recoveryPlanModel struct {
	calls int
}

type writeRecoveryModel struct {
	calls int
}

func (model *writeRecoveryModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	model.calls++
	if request.ResponseName == "grounded_plan_critique" {
		return modelResponse(`{"passed":true,"summary":"document claim is grounded","supported_claims":[{"claim":"result.md was written","evidence_refs":["E1"]}],"unsupported_claims":[]}`), nil
	}
	switch model.calls {
	case 1:
		return modelResponse(`{"summary":"write and verify","steps":[{"id":"write","title":"Write document","description":"Create result.md.","tool_ids":["write"],"deliverables":["result.md"],"acceptance_criteria":["result.md contains recovery-marker"],"verification_strategy":"content-match"}]}`), nil
	case 2:
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{{
			ID: "write-result", Type: "function", FunctionCall: &llms.FunctionCall{Name: "write", Arguments: []byte(`{"file_path":"result.md","content":"recovery-marker\n"}`)},
		}}}}}, nil
	case 3:
		return modelResponse("Created result.md with the recovery marker."), nil
	case 5:
		return modelResponse("Verified documentation write completed."), nil
	default:
		return modelResponse("unexpected extra model call"), nil
	}
}

func (model *recoveryPlanModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	model.calls++
	if request.ResponseName == "grounded_plan_critique" {
		return modelResponse(`{"passed":true,"summary":"analysis claim is grounded","supported_claims":[{"claim":"go.mod was inspected","evidence_refs":["E1"]}],"unsupported_claims":[]}`), nil
	}
	switch model.calls {
	case 1:
		return modelResponse(`{"summary":"inspect the plan mode","steps":[{"id":"inspect","title":"Inspect implementation","description":"Read the plan-mode files and explain their verification flow.","tool_ids":["read"],"deliverables":["evidence-backed analysis"],"acceptance_criteria":["analysis cites an inspected file"],"verification_strategy":"no-op"}]}`), nil
	case 2:
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{{
			ID: "read-go-mod", Type: "function", FunctionCall: &llms.FunctionCall{Name: "read", Arguments: []byte(`{"file_path":"go.mod","limit":20}`)},
		}}}}}, nil
	case 3:
		return modelResponse("The graph uses a verifier node and persistent runner; go.mod was inspected as read-only evidence."), nil
	case 5:
		return modelResponse("Verified read-only analysis completed with recorded file evidence."), nil
	default:
		return modelResponse("unexpected extra model call"), nil
	}
}

func modelResponse(content string) *llms.ModelResponse {
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: content}}}
}

func assertVerifiedAnalysisStep(t *testing.T, current *state.State, wantAttempts int) {
	t.Helper()
	value, ok := state.ReadPath(current, planStatePath.MustChild(plancap.FieldSteps).String())
	if !ok {
		t.Fatal("plan steps missing")
	}
	steps := plancap.DecodeSteps(value)
	if len(steps) != 1 || steps[0].VerificationStatus != plannode.VerificationStatusPassed || steps[0].VerificationAttempts != wantAttempts || len(steps[0].Evidence) == 0 {
		t.Fatalf("steps = %#v", steps)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	return root
}
