package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/internal/utilities"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/tools"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	planStateKey = "plan"

	statusPlanning  = "planning"
	statusExecuting = "executing"
	statusReplan    = "replan"
	statusDone      = "done"

	stepStatusPending = "pending"
	stepStatusRunning = "running"
	stepStatusDone    = "done"
	stepStatusFailed  = "failed"

	defaultMaxReplans      = 2
	defaultStepMaxToolIter = 6
)

func main() {
	must(os.Setenv("WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK", "true"))

	objective := strings.Join(os.Args[1:], " ")
	if strings.TrimSpace(objective) == "" {
		objective = "当前 graph 的运行可观测性有什么需要改进的吗"
	}

	model, err := openai.New()
	must(err)

	available := map[string]tools.Tool{
		"read":    tools.NewRead(),
		"glob":    tools.NewGlob(),
		"grep":    tools.NewGrep(),
		"write":   tools.NewWrite(),
		"bash":    tools.NewBash(),
		"outline": tools.NewOutline(),
		"tree":    tools.NewTree(),
	}

	prettySink := utilities.NewPrettyEventLogging(os.Stdout,
		utilities.WithDisabledEventTypes(
			fruntime.EventCheckpointCreated,
			fruntime.EventArtifactCreated,
		),
		utilities.WithToolCallDetails(false),
		//utilities.WithLLMTextTruncate(100),
	)
	analyzer := fruntime.NewEventAnalyzer()
	sink := fruntime.NewCombineEventSink(prettySink, analyzer)

	finalState, err := runPlan(context.Background(), objective, options{
		Model:                 model,
		Tools:                 available,
		MaxReplans:            defaultMaxReplans,
		StepMaxToolIterations: defaultStepMaxToolIter,
		EventSink:             sink,
	})
	must(err)

	printPlanState(finalState)
	printRunAnalysis(analyzer)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printPlanState(state *state.State) {
	plan := getPlanMap(state)
	fmt.Println()
	fmt.Println("=== plan state ===")
	fmt.Printf("  status:        %v\n", plan["status"])
	fmt.Printf("  replan_count:  %v\n", plan["replan_count"])
	fmt.Printf("  current_index: %v\n", plan["current_index"])
	for _, step := range stepsFromPlan(plan) {
		fmt.Printf("  - [%s] %s  (%s)\n",
			stringField(step, "id"),
			stringField(step, "title"),
			stringField(step, "status"),
		)
	}
	fmt.Println()
	fmt.Println("=== final summary ===")
	fmt.Println(stringField(plan, "summary"))
}

func printRunAnalysis(analyzer *fruntime.EventAnalyzer) {
	if analyzer == nil {
		return
	}
	analyses, err := analyzer.AnalyzeRuns()
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze run: %v\n", err)
		return
	}
	if len(analyses) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("=== run analysis ===")
	for _, analysis := range analyses {
		fmt.Printf("  run:      %s\n", analysis.RunID)
		fmt.Printf("  status:   %s\n", analysis.Status)
		fmt.Printf("  duration: %s\n", analysis.Duration)
		fmt.Printf("  events:   %d\n", analysis.EventCount)
		fmt.Printf("  llm:      %d calls, %d tokens (in:%d out:%d think:%d cached:%d)\n",
			analysis.LLM.Calls,
			analysis.LLM.TotalTokens,
			analysis.LLM.PromptTokens,
			analysis.LLM.CompletionTokens,
			analysis.LLM.ReasoningTokens,
			analysis.LLM.PromptCachedTokens,
		)
		fmt.Printf("  tools:    %d called, %d returned, %d failed, duration:%s\n",
			analysis.Tools.Called,
			analysis.Tools.Returned,
			analysis.Tools.Failed,
			analysis.Tools.Duration,
		)
		fmt.Println("  nodes:")
		for _, node := range analysis.Nodes {
			name := node.NodeName
			if name == "" {
				name = node.NodeID
			}
			fmt.Printf("    - %s attempts:%d retries:%d duration:%s llm:%d/%d tools:%d/%d\n",
				name,
				node.AttemptCount,
				node.RetryCount,
				node.Duration,
				node.LLM.Calls,
				node.LLM.TotalTokens,
				node.Tools.Called,
				node.Tools.Failed,
			)
		}
	}
}

// ===========================================================================
// 编排
// ===========================================================================

type options struct {
	Model                 llms.Model
	Tools                 map[string]tools.Tool
	MaxReplans            int
	StepMaxToolIterations int
	EventSink             fruntime.EventSink
}

func runPlan(ctx context.Context, objective string, opts options) (*state.State, error) {
	if strings.TrimSpace(objective) == "" {
		return nil, errors.New("plan: objective is required")
	}
	if opts.Model == nil {
		return nil, errors.New("plan: opts.Model is required")
	}
	if opts.MaxReplans <= 0 {
		opts.MaxReplans = defaultMaxReplans
	}
	if opts.StepMaxToolIterations <= 0 {
		opts.StepMaxToolIterations = defaultStepMaxToolIter
	}

	ctx = core.WithModel(ctx, opts.Model)
	ctx = core.WithTools(ctx, opts.Tools)

	planner := newPlanGeneratorNode(opts.MaxReplans)
	executor := newStepExecutorNode(opts.StepMaxToolIterations)
	reviewer := newStepReviewerNode(opts.MaxReplans)

	g := weaveflow.NewGraph()
	for _, n := range []node.Node{planner, executor, reviewer} {
		if err := g.AddNode(n); err != nil {
			return nil, err
		}
	}
	if err := g.SetEntryPoint(planner.ID()); err != nil {
		return nil, err
	}
	if err := g.AddEdge(planner.ID(), executor.ID()); err != nil {
		return nil, err
	}
	if err := g.AddEdge(executor.ID(), reviewer.ID()); err != nil {
		return nil, err
	}

	// reviewer 出边: 三选一。把 done 放最前以便提前停止。
	if err := g.AddConditionalEdge(reviewer.ID(), weaveflow.EndNodeRef,
		newStatusCondition("done", statusDone)); err != nil {
		return nil, err
	}
	if err := g.AddConditionalEdge(reviewer.ID(), planner.ID(),
		newStatusCondition("replan", statusReplan)); err != nil {
		return nil, err
	}
	if err := g.AddConditionalEdge(reviewer.ID(), executor.ID(),
		newStatusCondition("executing", statusExecuting)); err != nil {
		return nil, err
	}
	if err := g.AddEdge(reviewer.ID(), weaveflow.EndNodeRef); err != nil {
		return nil, err
	}

	sink := opts.EventSink
	if sink == nil {
		sink = fruntime.NoopEventSink{}
	}
	runner, err := weaveflow.NewRunner(
		g,
		weaveflow.WithExecutionStore(fruntime.NewNoopExecutionStore()),
		weaveflow.WithCheckpointStore(fruntime.NewNoopCheckpointStore()),
		weaveflow.WithEventSink(sink),
		weaveflow.WithArtifactStore(fruntime.NewNoopArtifactStore()),
		weaveflow.WithGraphID("plan-mode-proto"),
	)
	if err != nil {
		return nil, err
	}

	initial := state.FromShared(map[string]any{
		planStateKey: map[string]any{
			"objective":     strings.TrimSpace(objective),
			"status":        statusPlanning,
			"steps":         []map[string]any{},
			"current_index": 0,
			"summary":       "",
			"replan_count":  0,
		},
	})

	_, finalState, err := runner.Start(ctx, initial)
	return finalState, err
}

// ===========================================================================
// 节点 1: planGenerator — 生成 / 重新生成结构化计划
// ===========================================================================

type planGeneratorNode struct {
	node.Base
	maxReplans int
}

func newPlanGeneratorNode(maxReplans int) *planGeneratorNode {
	id := uuid.NewString()
	return &planGeneratorNode{
		Base: node.NewBase(node.Spec{
			ID:          "PlanGenerator_" + id,
			Name:        "PlanGenerator",
			Description: "Produce a structured step plan from the objective (and prior plan, if any).",
		}),
		maxReplans: maxReplans,
	}
}

func (n *planGeneratorNode) Execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model()
	if model == nil {
		return errors.New("plan generator: model service not available")
	}
	plan := getPlanMap(access)
	objective, _ := plan["objective"].(string)
	if strings.TrimSpace(objective) == "" {
		return errors.New("plan generator: objective is empty")
	}

	previousSteps := stepsFromPlan(plan)
	replanCount, _ := plan["replan_count"].(int)
	isReplan := len(previousSteps) > 0

	payload := map[string]any{
		"objective":       objective,
		"is_replan":       isReplan,
		"previous_steps":  previousSteps,
		"replan_reason":   plan["replan_reason"],
		"available_tools": describeTools(ctx.Tools()),
	}
	prompt, _ := json.MarshalIndent(payload, "", "  ")

	resp, err := model.GenerateContent(ctx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, planGeneratorSystemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman,
			"Generate a plan from the following JSON payload.\n\n"+string(prompt)),
	},
		llms.WithJSONMode(),
		llms.WithThinkingMode(llms.ThinkingModeNone),
		llms.WithTemperature(0.2),
	)
	if err != nil {
		return err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return errors.New("plan generator: llm returned no choices")
	}

	parsed, err := parsePlannerOutput(resp.Choices[0].Content)
	if err != nil {
		return fmt.Errorf("plan generator: parse output: %w", err)
	}
	if len(parsed.Steps) == 0 {
		return errors.New("plan generator: produced empty plan")
	}

	plan["steps"] = serializeSteps(parsed.Steps)
	plan["status"] = statusExecuting
	plan["current_index"] = 0
	plan["summary"] = parsed.Summary
	if isReplan {
		plan["replan_count"] = replanCount + 1
	}
	delete(plan, "replan_reason")
	return setPlanMap(access, plan)
}

const planGeneratorSystemPrompt = `You are the planner of a plan-mode agent prototype.
Return STRICT JSON only, no markdown fences.

Input JSON gives you objective, optional previous_steps (with their results) and an available_tools list. When is_replan is true, refine the plan based on the observed results.

Output shape:
{
  "summary": string,           // one-sentence summary of the plan
  "steps": [
    {
      "id":          "step_1",
      "title":       string,
      "description": string,    // concrete instruction for the executor LLM, in the same language as the user objective
      "tool_hints":  [string]   // optional tool names the executor should consider
    }
  ]
}

Rules:
- 3-7 focused steps. Each step has ONE deliverable.
- The final step MUST be a synthesis step that produces a user-facing answer to the objective; it should rely on prior step results, not new tool calls unless strictly necessary.
- Use the language of the objective for titles, descriptions and the final summary.
- Do not invent tools: only reference names from available_tools.`

// ===========================================================================
// 节点 2: stepExecutor — 跑当前 step 的小 tool-call 循环
// ===========================================================================

type stepExecutorNode struct {
	node.Base
	maxToolIterations int
}

func newStepExecutorNode(maxToolIterations int) *stepExecutorNode {
	id := uuid.NewString()
	return &stepExecutorNode{
		Base: node.NewBase(node.Spec{
			ID:          "StepExecutor_" + id,
			Name:        "StepExecutor",
			Description: "Run the current plan step with a small tool-call loop and record its result.",
		}),
		maxToolIterations: maxToolIterations,
	}
}

func (n *stepExecutorNode) Execute(ctx core.Context, access *state.Access) error {
	model := ctx.Model()
	if model == nil {
		return errors.New("step executor: model service not available")
	}
	plan := getPlanMap(access)
	steps := stepsFromPlan(plan)
	idx, _ := plan["current_index"].(int)
	if idx < 0 || idx >= len(steps) {
		// 没有更多 step，reviewer 会处理收尾。
		return nil
	}
	step := steps[idx]
	objective, _ := plan["objective"].(string)

	step["status"] = stepStatusRunning

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, stepExecutorSystemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, buildStepPrompt(objective, plan, step, steps[:idx])),
	}

	toolHints := stringSlice(step["tool_hints"])
	nodeTools := filterTools(ctx.Tools(), toolHints)
	toolSpecs := toolSpecsFrom(nodeTools)

	var finalText string
	for iter := 0; iter < n.maxToolIterations; iter++ {
		_ = iter
		callOpts := []llms.CallOption{
			llms.WithThinkingMode(llms.ThinkingModeLow),
		}
		if len(toolSpecs) > 0 {
			callOpts = append(callOpts, llms.WithTools(toolSpecs))
		}
		resp, err := model.GenerateContent(ctx, messages, callOpts...)
		if err != nil {
			step["status"] = stepStatusFailed
			step["error"] = err.Error()
			steps[idx] = step
			plan["steps"] = steps
			plan["current_index"] = idx + 1
			return setPlanMap(access, plan)
		}
		if resp == nil || len(resp.Choices) == 0 {
			break
		}
		choice := resp.Choices[0]

		ai := llms.MessageContent{Role: llms.ChatMessageTypeAI}
		if strings.TrimSpace(choice.Content) != "" {
			ai.Parts = append(ai.Parts, llms.TextPart(choice.Content))
			finalText = choice.Content
		}
		for _, tc := range choice.ToolCalls {
			if tc.Type == "" {
				continue
			}
			ai.Parts = append(ai.Parts, tc)
		}
		messages = append(messages, ai)

		if len(choice.ToolCalls) == 0 {
			break
		}

		for _, tc := range choice.ToolCalls {
			name := ""
			args := ""
			if tc.FunctionCall != nil {
				name = tc.FunctionCall.Name
				args = tc.FunctionCall.Arguments
			}
			_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolCalled, map[string]any{
				"tool_call_id": tc.ID,
				"name":         name,
				"arguments":    args,
			})
			result, toolErr := invokeTool(ctx, nodeTools, name, args)
			toolPayload := map[string]any{
				"tool_call_id": tc.ID,
				"name":         name,
				"arguments":    args,
			}
			if toolErr != nil {
				toolPayload["error"] = toolErr.Error()
				_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolFailed, toolPayload)
			} else {
				toolPayload["content"] = result
				_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventToolReturned, toolPayload)
			}
			messages = append(messages, llms.MessageContent{
				Role: llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.ToolCallResponse{
						ToolCallID: tc.ID,
						Name:       name,
						Content:    result,
					},
				},
			})
		}
	}

	step["status"] = stepStatusDone
	step["result"] = strings.TrimSpace(finalText)
	steps[idx] = step
	plan["steps"] = steps
	plan["current_index"] = idx + 1
	return setPlanMap(access, plan)
}

const stepExecutorSystemPrompt = `You are the executor for a single step of a plan-mode agent.
You will be given the overall objective, the step you should accomplish now, and a short summary of completed steps.

Operate using the provided tools when needed. When you have enough information to satisfy the step, stop calling tools and reply with a concise result for THIS step only — not the whole plan. Reply in the same language as the objective.`

func buildStepPrompt(objective string, plan map[string]any, step map[string]any, prior []map[string]any) string {
	var b strings.Builder
	b.WriteString("Overall objective:\n")
	b.WriteString(strings.TrimSpace(objective))
	b.WriteString("\n\nPlan summary:\n")
	if s, _ := plan["summary"].(string); s != "" {
		b.WriteString(strings.TrimSpace(s))
	} else {
		b.WriteString("(none)")
	}
	if len(prior) > 0 {
		b.WriteString("\n\nCompleted steps:\n")
		for _, p := range prior {
			fmt.Fprintf(&b, "- [%s] %s\n  result: %s\n",
				stringField(p, "id"),
				stringField(p, "title"),
				oneLine(stringField(p, "result")),
			)
		}
	}
	b.WriteString("\nCurrent step:\n")
	fmt.Fprintf(&b, "  id: %s\n  title: %s\n  description: %s\n",
		stringField(step, "id"),
		stringField(step, "title"),
		stringField(step, "description"),
	)
	if hints := stringSlice(step["tool_hints"]); len(hints) > 0 {
		fmt.Fprintf(&b, "  suggested tools: %s\n", strings.Join(hints, ", "))
	}
	b.WriteString("\nProduce the deliverable for the current step.")
	return b.String()
}

// ===========================================================================
// 节点 3: stepReviewer — 路由决策中心 (advance / replan / done)
// ===========================================================================

// 决策策略:
//   - 当前 step 失败 且 replan_count < max ⇒ status=replan
//   - 还有 step ⇒ status=executing
//   - 已经全部完成 ⇒ 跑一次合成 LLM 写 summary，status=done
//
// 把策略集中在一个节点，以后可以接更复杂的验收（例如 LLM 评估 acceptance criteria）。
type stepReviewerNode struct {
	node.Base
	maxReplans int
}

func newStepReviewerNode(maxReplans int) *stepReviewerNode {
	id := uuid.NewString()
	return &stepReviewerNode{
		Base: node.NewBase(node.Spec{
			ID:          "StepReviewer_" + id,
			Name:        "StepReviewer",
			Description: "Decide whether to continue executing, replan, or finish.",
		}),
		maxReplans: maxReplans,
	}
}

func (n *stepReviewerNode) Execute(ctx core.Context, access *state.Access) error {
	plan := getPlanMap(access)
	steps := stepsFromPlan(plan)
	idx, _ := plan["current_index"].(int)
	replanCount, _ := plan["replan_count"].(int)

	if idx > 0 {
		justRun := steps[idx-1]
		if stringField(justRun, "status") == stepStatusFailed && replanCount < n.maxReplans {
			plan["status"] = statusReplan
			plan["replan_reason"] = "previous step failed: " + stringField(justRun, "error")
			return setPlanMap(access, plan)
		}
	}

	if idx < len(steps) {
		plan["status"] = statusExecuting
		return setPlanMap(access, plan)
	}

	if final, err := n.synthesize(ctx, plan, steps); err == nil {
		plan["summary"] = final
	}
	plan["status"] = statusDone
	return setPlanMap(access, plan)
}

func (n *stepReviewerNode) synthesize(ctx core.Context, plan map[string]any, steps []map[string]any) (string, error) {
	model := ctx.Model()
	if model == nil {
		return "", errors.New("no model")
	}
	objective, _ := plan["objective"].(string)

	var b strings.Builder
	b.WriteString("Objective:\n")
	b.WriteString(strings.TrimSpace(objective))
	b.WriteString("\n\nStep results:\n")
	for _, s := range steps {
		fmt.Fprintf(&b, "- [%s] %s\n  status: %s\n  result: %s\n",
			stringField(s, "id"),
			stringField(s, "title"),
			stringField(s, "status"),
			stringField(s, "result"),
		)
	}

	resp, err := model.GenerateContent(ctx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem,
			"Synthesize a final user-facing answer for the objective using the provided step results. "+
				"Reply in the same language as the objective. No preamble, no markdown headers."),
		llms.TextParts(llms.ChatMessageTypeHuman, b.String()),
	},
		llms.WithThinkingMode(llms.ThinkingModeLow),
		llms.WithTemperature(0.3),
	)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("synthesizer: no choices")
	}
	return strings.TrimSpace(resp.Choices[0].Content), nil
}

// ===========================================================================
// 条件边
// ===========================================================================

func newStatusCondition(name, want string) registry.EdgeCondition {
	return registry.NewEdgeCondition(
		core.GraphConditionSpec{
			Type:   "plan_status_eq",
			Config: map[string]any{"want": want, "name": name},
		},
		func(_ context.Context, state *state.State) bool {
			plan := getPlanMap(state)
			got, _ := plan["status"].(string)
			return got == want
		},
	)
}

// ===========================================================================
// state / 工具辅助函数
// ===========================================================================

func getPlanMap(reader any) map[string]any {
	if reader == nil {
		return map[string]any{}
	}
	readable, ok := reader.(interface {
		ReadAny(state.Path) (any, bool)
	})
	if !ok {
		currentState, ok := reader.(*state.State)
		if !ok {
			return map[string]any{}
		}
		readable = state.NewAccess(nil, currentState)
	}
	value, ok := readable.ReadAny(state.Shared(planStateKey))
	if !ok {
		return map[string]any{}
	}
	switch typed := value.(type) {
	case map[string]any:
		return typed
	}
	return map[string]any{}
}

func setPlanMap(writer interface {
	SetAny(state.Path, any) error
}, plan map[string]any) error {
	return writer.SetAny(state.Shared(planStateKey), plan)
}

func stepsFromPlan(plan map[string]any) []map[string]any {
	switch typed := plan["steps"].(type) {
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		copy(out, typed)
		return out
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

type parsedPlan struct {
	Summary string       `json:"summary"`
	Steps   []parsedStep `json:"steps"`
}

type parsedStep struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	ToolHints   []string `json:"tool_hints"`
}

func parsePlannerOutput(content string) (parsedPlan, error) {
	content = strings.TrimSpace(stripFence(content))
	if content == "" {
		return parsedPlan{}, errors.New("empty content")
	}
	var p parsedPlan
	if err := json.Unmarshal([]byte(content), &p); err == nil {
		return normalizeParsed(p), nil
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(content[start:end+1]), &p); err == nil {
			return normalizeParsed(p), nil
		}
	}
	return parsedPlan{}, errors.New("not valid JSON")
}

func normalizeParsed(p parsedPlan) parsedPlan {
	out := parsedPlan{Summary: strings.TrimSpace(p.Summary)}
	for i, s := range p.Steps {
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" {
			s.ID = fmt.Sprintf("step_%d", i+1)
		}
		s.Title = strings.TrimSpace(s.Title)
		s.Description = strings.TrimSpace(s.Description)
		hints := make([]string, 0, len(s.ToolHints))
		for _, h := range s.ToolHints {
			if h = strings.TrimSpace(h); h != "" {
				hints = append(hints, h)
			}
		}
		s.ToolHints = hints
		out.Steps = append(out.Steps, s)
	}
	return out
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```")
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func serializeSteps(steps []parsedStep) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		out = append(out, map[string]any{
			"id":          s.ID,
			"title":       s.Title,
			"description": s.Description,
			"tool_hints":  s.ToolHints,
			"status":      stepStatusPending,
			"result":      "",
		})
	}
	return out
}

func describeTools(available map[string]tools.Tool) []map[string]any {
	if len(available) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(available))
	for name, t := range available {
		if t.Function == nil {
			continue
		}
		out = append(out, map[string]any{
			"name":        name,
			"description": firstLine(t.Function.Description),
		})
	}
	return out
}

func filterTools(available map[string]tools.Tool, hints []string) map[string]tools.Tool {
	if len(available) == 0 {
		return nil
	}
	if len(hints) == 0 {
		return available
	}
	filtered := make(map[string]tools.Tool, len(hints))
	for _, h := range hints {
		if t, ok := available[h]; ok {
			filtered[h] = t
		}
	}
	// hints 全没命中则退回全集，避免 LLM 抓瞎。
	if len(filtered) == 0 {
		return available
	}
	return filtered
}

func toolSpecsFrom(available map[string]tools.Tool) []llms.Tool {
	if len(available) == 0 {
		return nil
	}
	out := make([]llms.Tool, 0, len(available))
	for _, t := range available {
		if t.Function == nil {
			continue
		}
		out = append(out, t.NewTool())
	}
	return out
}

func invokeTool(ctx context.Context, available map[string]tools.Tool, name, args string) (string, error) {
	t, ok := available[strings.TrimSpace(name)]
	if !ok || t.Handler == nil {
		err := fmt.Errorf("tool %q is not available", name)
		return err.Error(), err
	}
	result, err := t.Handler(ctx, args)
	if err != nil {
		return "tool error: " + err.Error(), err
	}
	return result, nil
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func stringSlice(v any) []string {
	switch typed := v.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return s
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
