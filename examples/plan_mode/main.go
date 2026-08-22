package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/node"
	plannode "github.com/dengzii/weaveflow/node/plan"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

var (
	planObjectivePath    = state.Shared("request", "input")
	planStatePath        = state.Shared("plan")
	planExecutionPath    = state.Shared("execution")
	planConversationPath = state.Scope("plan_worker", "conversation")
	planResultPath       = state.Shared("final", "answer")
)

type cliOptions struct {
	ProfileID string
	Objective string
	DataDir   string
	Timeout   time.Duration
}

func main() {
	options, err := parseCLI(os.Args[1:])
	if err != nil {
		panic(err)
	}
	profile, err := profileByID(options.ProfileID)
	if err != nil {
		panic(err)
	}
	if options.Objective == "" {
		options.Objective = profile.DefaultObjective
	}
	if options.Timeout > 0 {
		profile.TotalTimeout = options.Timeout
		if profile.ModelTimeout > options.Timeout {
			profile.ModelTimeout = options.Timeout
		}
	}
	if err := profile.Validate(); err != nil {
		panic(err)
	}
	if err := loadRootDotEnv(); err != nil {
		panic(err)
	}
	if err := normalizeOpenAIBaseURL(); err != nil {
		panic(err)
	}
	model, err := openai.New(openai.WithToken(strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))))
	if err != nil {
		panic(err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	availableTools, err := toolsForProfile(profile, workspace)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), profile.TotalTimeout)
	defer cancel()
	finalState, run, err := runPlan(ctx, retryingModel{model: model, attempts: profile.ModelRetries, timeout: profile.ModelTimeout}, profile, availableTools, options.Objective, options.DataDir)
	if err != nil {
		panic(err)
	}
	printPlanResult(finalState, run)
}

func parseCLI(arguments []string) (cliOptions, error) {
	flags := flag.NewFlagSet("plan_mode", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var options cliOptions
	flags.StringVar(&options.ProfileID, "profile", "tiny-script", "task profile")
	flags.StringVar(&options.Objective, "objective", "", "task objective")
	flags.StringVar(&options.DataDir, "data", ".local/plan_mode", "persistent runner data directory")
	flags.DurationVar(&options.Timeout, "timeout", 0, "total run timeout override")
	if err := flags.Parse(arguments); err != nil {
		return cliOptions{}, err
	}
	positional := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if options.Objective != "" && positional != "" {
		return cliOptions{}, errors.New("objective must be provided with either -objective or positional arguments, not both")
	}
	if options.Objective == "" {
		options.Objective = positional
	}
	options.DataDir = strings.TrimSpace(options.DataDir)
	if options.DataDir == "" {
		return cliOptions{}, errors.New("data directory is required")
	}
	return options, nil
}

func newPlanGraph(profile TaskProfile) (*wfgraph.Graph, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	graph := weaveflow.NewGraph()

	generator := plannode.NewGeneratorNode(node.WithID("generate_plan"))
	generator.ToolIDs = append([]string(nil), profile.ToolIDs...)
	generator.VerifierID = profile.VerifierID
	generator.DefaultVerificationStrategy = profile.VerifierID
	generator.SystemPrompt = profile.PlannerPrompt
	generator.MaxSteps = profile.MaxSteps
	generator.MaxReplans = profile.MaxReplans
	generator.ObjectivePath, generator.PlanPath, generator.ExecutionPath = planObjectivePath, planStatePath, planExecutionPath

	step := plannode.NewStepNode(node.WithID("prepare_step"))
	step.SystemPrompt = profile.WorkerPrompt
	step.MaxIterations = profile.MaxIterations
	step.PlanPath, step.ExecutionPath, step.ConversationPath = planStatePath, planExecutionPath, planConversationPath

	execute := node.NewLLMTurnNode(node.WithID("execute_step"))
	execute.ToolIDs = append([]string(nil), profile.ToolIDs...)
	execute.ReasoningEffort = "low"
	execute.ConversationPath = planConversationPath

	executeTools := node.NewToolExecutionNode(node.WithID("execute_tools"))
	executeTools.ToolIDs = append([]string(nil), profile.ToolIDs...)
	executeTools.Parallel = true
	executeTools.ConversationPath = planConversationPath

	finalizeStep := node.NewLLMTurnNode(node.WithID("finalize_step"))
	finalizeStep.SystemPrompt = profile.FinalizerPrompt
	finalizeStep.ReasoningEffort = "low"
	finalizeStep.ConversationPath = planConversationPath

	verifier := plannode.NewVerifierNode(node.WithID("verify_step"))
	verifier.VerifierID = profile.VerifierID
	verifier.VerifierConfig = verifierConfigMap(profile.VerifierConfig)
	verifier.MaxAttempts = profile.MaxStepAttempts
	verifier.CriticEnabled = profile.GroundedCritic
	verifier.CriticPrompt = profile.CriticPrompt
	verifier.PlanPath, verifier.ExecutionPath, verifier.ConversationPath = planStatePath, planExecutionPath, planConversationPath

	review := plannode.NewReviewNode(node.WithID("review_step"))
	review.MaxAttempts = profile.MaxStepAttempts
	review.PlanPath, review.ExecutionPath, review.ConversationPath = planStatePath, planExecutionPath, planConversationPath

	synthesis := plannode.NewSynthesisNode(node.WithID("synthesize_plan"))
	synthesis.SystemPrompt = profile.SynthesisPrompt
	synthesis.RequireEvidenceRefs = profile.RequireEvidenceRefs
	synthesis.PlanPath, synthesis.ResultPath = planStatePath, planResultPath

	for _, target := range []node.Node{generator, step, execute, executeTools, finalizeStep, verifier, review, synthesis} {
		if err := graph.AddNode(target); err != nil {
			return nil, err
		}
	}
	if err := graph.SetEntryPoint(generator.ID()); err != nil {
		return nil, err
	}
	if err := graph.SetFinishPoint(synthesis.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(generator.ID(), step.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(step.ID(), execute.ID(), plannode.StatusEquals(planStatePath, plannode.PlanStatusExecuting)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(step.ID(), synthesis.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(execute.ID(), executeTools.ID(), builtin.ConversationHasToolCalls(planConversationPath)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(execute.ID(), verifier.ID()); err != nil {
		return nil, err
	}
	iterationCondition, iterationContract := hasPlanStepIterationsRemaining(planConversationPath)
	if err := graph.AddResolvedConditionalEdge(executeTools.ID(), execute.ID(), iterationCondition, iterationContract); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(executeTools.ID(), finalizeStep.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(finalizeStep.ID(), verifier.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(verifier.ID(), review.ID()); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(review.ID(), generator.ID(), plannode.StatusEquals(planStatePath, plannode.PlanStatusReplan)); err != nil {
		return nil, err
	}
	if err := graph.AddConditionalEdge(review.ID(), step.ID(), plannode.StatusEquals(planStatePath, plannode.PlanStatusExecuting)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(review.ID(), synthesis.ID()); err != nil {
		return nil, err
	}
	return graph, nil
}

func hasPlanStepIterationsRemaining(conversationPath state.Path) (registry.EdgeCondition, state.Contract) {
	return plannode.IterationsRemaining(conversationPath)
}

func runPlan(ctx context.Context, model llms.Model, profile TaskProfile, availableTools map[string]core.Tool, objective, dataDir string) (*state.State, runtime.RunRecord, error) {
	if model == nil {
		return nil, runtime.RunRecord{}, errors.New("plan mode: model is required")
	}
	if err := profile.Validate(); err != nil {
		return nil, runtime.RunRecord{}, err
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, runtime.RunRecord{}, errors.New("plan mode: objective is required")
	}
	graph, err := newPlanGraph(profile)
	if err != nil {
		return nil, runtime.RunRecord{}, err
	}
	workspace, err := filepath.Abs(".")
	if err != nil {
		return nil, runtime.RunRecord{}, err
	}
	verifier, err := newVerifier(profile.VerifierID, profile.VerifierConfig)
	if err != nil {
		return nil, runtime.RunRecord{}, err
	}
	ctx = core.WithModel(ctx, model)
	ctx = core.WithTools(ctx, availableTools)
	ctx = core.WithEnvironment(ctx, map[string]string{"WEAVEFLOW_TOOL_WORKDIR": workspace})
	ctx = core.WithToolPermissions(ctx, profile.Permissions...)
	ctx = core.WithToolApprover(ctx, profileApprover(profile))
	ctx = plannode.WithVerifier(ctx, verifier.Verify)
	ctx = withPlanObservers(ctx)
	runner, err := weaveflow.NewLocalRunner(graph, dataDir,
		weaveflow.WithGraphID("examples.plan_mode."+profile.ID),
		weaveflow.WithGraphVersion("1.0"),
	)
	if err != nil {
		return nil, runtime.RunRecord{}, err
	}
	defer runner.Close()
	if existing, err := latestContinuablePlanRun(ctx, runner, profile.ID); err != nil {
		return nil, runtime.RunRecord{}, err
	} else if existing != nil {
		if existing.Status == runtime.RunStatusRunning || existing.Status == runtime.RunStatusPending {
			if _, err := runner.MarkRunExecutionLost(ctx, existing.RunID); err != nil {
				return nil, runtime.RunRecord{}, fmt.Errorf("mark interrupted run %s: %w", existing.RunID, err)
			}
		}
		fmt.Fprintf(os.Stderr, "run: resuming %s\n", existing.RunID)
		run, finalState, err := runner.Resume(ctx, existing.RunID, nil)
		return finalState, run, err
	}
	initial := state.FromShared(map[string]any{"request": map[string]any{"input": objective}})
	run, finalState, err := runner.Start(ctx, initial)
	return finalState, run, err
}

func latestContinuablePlanRun(ctx context.Context, runner *runtime.GraphRunner, profileID string) (*runtime.RunRecord, error) {
	runs, err := runner.ListRuns(ctx, runtime.RunFilter{Statuses: []runtime.RunStatus{
		runtime.RunStatusPending, runtime.RunStatusRunning, runtime.RunStatusPaused, runtime.RunStatusFailed, runtime.RunStatusCanceled,
	}})
	if err != nil {
		return nil, err
	}
	graphID := "examples.plan_mode." + profileID
	var latest *runtime.RunRecord
	for index := range runs {
		run := runs[index]
		if run.GraphID != graphID || run.GraphVersion != "1.0" || run.LastCheckpointID == "" {
			continue
		}
		if latest == nil || latest.UpdatedAt.Before(run.UpdatedAt) {
			candidate := run
			latest = &candidate
		}
	}
	return latest, nil
}

func profileApprover(profile TaskProfile) core.ToolApprover {
	approved := stringSet(profile.ApprovedTools)
	return core.ToolApproverFunc(func(_ context.Context, request core.ToolApprovalRequest) (core.ToolApprovalDecision, error) {
		name := ""
		if request.ToolCall.FunctionCall != nil {
			name = request.ToolCall.FunctionCall.Name
		}
		_, allowed := approved[name]
		if profile.ApprovalPolicy != ApprovalConfigured {
			allowed = false
		}
		reason := "tool is not approved by the selected profile"
		if allowed {
			reason = "tool is explicitly approved by the selected profile"
		}
		return core.ToolApprovalDecision{Approved: allowed, Actor: "plan-mode:" + profile.ID, Reason: reason}, nil
	})
}

func withPlanObservers(ctx context.Context) context.Context {
	ctx = core.WithModelCallObserver(ctx, func(_ context.Context, event core.ModelCallEvent) error {
		fmt.Fprintf(os.Stderr, "model: %s %s\n", event.Stage, event.Request.ModelID)
		return nil
	})
	return core.WithToolExecutionObserver(ctx, func(_ context.Context, event core.ToolExecutionEvent) {
		fmt.Fprintf(os.Stderr, "tool: %s %s\n", event.Stage, event.Tool.Name())
	})
}

func verifierConfigMap(config VerifierConfig) map[string]any {
	return map[string]any{
		"packages": config.Packages, "allowed_packages": config.AllowedPackages, "files": config.Files,
		"contains": config.Contains, "absent": config.Absent, "analysis_only": config.AnalysisOnly,
	}
}

type retryingModel struct {
	model    llms.Model
	attempts int
	timeout  time.Duration
}

func (model retryingModel) Generate(ctx context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	attempts := max(model.attempts, 1)
	if request.MaxTokens <= 0 {
		request.MaxTokens = 4096
	}
	var lastResponse *llms.ModelResponse
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, model.timeout)
		lastResponse, lastErr = model.model.Generate(attemptCtx, request)
		cancel()
		if lastErr == nil {
			return lastResponse, nil
		}
		class := core.ClassifyError(lastErr)
		if class != core.ErrorTimeout && class != core.ErrorUnavailable && class != core.ErrorRateLimited {
			return lastResponse, lastErr
		}
	}
	return lastResponse, lastErr
}

func loadRootDotEnv() error {
	data, err := os.ReadFile(".env")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("plan mode: read .env: %w", err)
	}
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("plan mode: invalid .env entry on line %d", lineNumber+1)
		}
		if strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("plan mode: set .env variable %q: %w", key, err)
		}
	}
	return nil
}

func normalizeOpenAIBaseURL() error {
	raw := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("plan mode: parse OPENAI_BASE_URL: %w", err)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1"
		parsed.RawPath = ""
		if err := os.Setenv("OPENAI_BASE_URL", parsed.String()); err != nil {
			return fmt.Errorf("plan mode: normalize OPENAI_BASE_URL: %w", err)
		}
	}
	return nil
}

func printPlanResult(finalState *state.State, run runtime.RunRecord) {
	fmt.Printf("Run ID: %s\nStatus: %s\n", run.RunID, run.Status)
	answer, _ := state.ReadPath(finalState, planResultPath.String())
	fmt.Printf("Final answer:\n%s\n", answer)
	planValue, _ := state.ReadPath(finalState, planStatePath.String())
	encoded, err := json.MarshalIndent(planValue, "", "  ")
	if err == nil {
		fmt.Printf("\nPlan state:\n%s\n", encoded)
	}
}
