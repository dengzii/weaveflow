package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"weaveflow/dsl"
	"weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultPlannerStatePath = runtime.StateKeyPlanner
	defaultPlannerMaxSteps  = 6
	plannerSystemPrompt     = `你是 Agent 工作流中的 Planner 节点，负责把用户目标转换为可执行的工作流计划。

你的职责是规划，不是执行。
你不能伪造执行结果，不能假设不存在的节点、工具、API、数据源或权限。

你会收到以下信息：
1. 用户目标
2. 当前状态
3. 可用节点及其能力说明
4. 已完成步骤
5. 失败信息或重规划原因（如果有）

你的任务是输出“最小但完整”的执行计划，使后续节点可以按计划执行。

规划原则：
1. 只使用明确提供的节点能力。
2. 步骤必须清晰、可执行、可验证，避免空泛描述。
3. 每一步都要有明确目的、输入、输出和验收标准。
4. 有依赖关系的步骤必须写入 depends_on。
5. 无依赖的步骤可标记为可并行。
6. 优先安排信息收集、校验、澄清，再安排高成本或不可逆操作。
7. 如果关键信息缺失，不要猜测，应加入澄清或校验步骤。
8. 不要过度规划，保持步骤数量尽量少。
9. 如果是重规划，尽量复用已完成且未受影响的步骤。
10. 如果当前能力不足以完成目标，明确标记 blocked。

输出要求：
- 仅输出合法 JSON
- 不要输出 markdown 代码块
- 不要输出 JSON 之外的任何解释
- 顶层结构必须为：
{
  "objective": "string",
  "status": "planned | needs_clarification | blocked | replanned",
  "summary": "string",
  "replan_reason": "string",
  "plan": [
    {
      "id": "string",
      "title": "string",
      "description": "string",
      "status": "pending | ready | blocked | completed",
      "kind": "research | transform | decision | action | validation | human_input",
      "depends_on": [],
      "node_type": "string",
      "inputs": [],
      "outputs": [],
      "acceptance_criteria": [],
      "parallelizable": false
    }
  ]
}

补充要求：
- id 使用稳定短标识，例如 step_1、step_2
- summary 用简洁语言概括整体方案
- replan_reason 在非重规划时输出空字符串
- acceptance_criteria 必须具体且可检查
- 如果缺少用户输入才能继续，status 输出 needs_clarification
- 如果当前节点能力无法完成任务，status 输出 blocked`
)

type PlannerNode struct {
	NodeInfo
	model            llms.Model
	PlannerStatePath string
	ObjectivePath    string
	ContextPaths     []string
	MaxSteps         int
	StepKindHints    []string
	Instructions     string
}

type plannerResponse struct {
	Objective    string            `json:"objective"`
	Status       string            `json:"status"`
	Summary      string            `json:"summary"`
	ReplanReason string            `json:"replan_reason"`
	Plan         []plannerPlanStep `json:"plan"`
}

type plannerPlanStep struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Status             string   `json:"status"`
	Kind               string   `json:"kind"`
	DependsOn          []string `json:"depends_on"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Outputs            []string `json:"outputs"`
}

func NewPlannerNode(model llms.Model) *PlannerNode {
	id := uuid.New()
	return &PlannerNode{
		NodeInfo: NodeInfo{
			NodeID:          "Planner_" + id.String(),
			NodeName:        "Planner",
			NodeDescription: "Generate or refresh a structured execution plan from the current objective and context.",
		},
		model:            model,
		PlannerStatePath: defaultPlannerStatePath,
		MaxSteps:         defaultPlannerMaxSteps,
	}
}

func (n *PlannerNode) Invoke(ctx context.Context, state runtime.State) (runtime.State, error) {
	if n.model == nil {
		return state, errors.New("planner model is nil")
	}
	if state == nil {
		state = runtime.State{}
	}

	plannerPath := n.effectivePlannerStatePath()
	plannerState, err := ensurePlannerStateAtPath(state, plannerPath)
	if err != nil {
		return state, err
	}

	objective, err := n.resolveObjective(state, plannerState)
	if err != nil {
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{"error": err.Error()})
		return state, err
	}

	contextPayload := n.collectContext(state)
	promptPayload := map[string]any{
		"objective":        objective,
		"planner_state":    plannerState,
		"context":          contextPayload,
		"max_steps":        n.effectiveMaxSteps(),
		"step_kind_hints":  clonePlannerStrings(n.StepKindHints),
		"planner_path":     plannerPath,
		"additional_rules": strings.TrimSpace(n.Instructions),
	}
	_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.prompt", promptPayload)
	resp, err := n.model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, plannerSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, buildPlannerPrompt(promptPayload)),
		},
		runtime.WithLLMStreamingResponseEvent(),
		llms.WithThinkingMode(llms.ThinkingModeAuto),
		llms.WithReturnThinking(false),
		llms.WithTemperature(0),
	)
	if err != nil {
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err = errors.New("planner returned no choices")
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{"error": err.Error()})
		return state, err
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	parsed, err := parsePlannerResponse(content, objective)
	if err != nil {
		_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{
			"error":    err.Error(),
			"response": content,
		})
		return state, err
	}

	applyPlannerResponse(plannerState, parsed)
	_ = runtime.PublishRunnerContextEvent(ctx, runtime.EventNodeCustom, map[string]any{
		"kind":         "planner",
		"planner_path": plannerPath,
		"status":       parsed.Status,
		"step_count":   len(parsed.Plan),
	})
	_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.response", parsed)

	return state, nil
}

func (n *PlannerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"planner_state_path": n.effectivePlannerStatePath(),
		"max_steps":          n.effectiveMaxSteps(),
	}
	if objectivePath := n.effectiveObjectivePath(); objectivePath != "" {
		config["objective_path"] = objectivePath
	}
	if len(n.ContextPaths) > 0 {
		config["context_paths"] = clonePlannerStrings(n.ContextPaths)
	}
	if len(n.StepKindHints) > 0 {
		config["step_kind_hints"] = clonePlannerStrings(n.StepKindHints)
	}
	if instructions := strings.TrimSpace(n.Instructions); instructions != "" {
		config["instructions"] = instructions
	}

	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "planner",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *PlannerNode) effectivePlannerStatePath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return defaultPlannerStatePath
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *PlannerNode) effectiveObjectivePath() string {
	if n == nil || strings.TrimSpace(n.ObjectivePath) == "" {
		return n.effectivePlannerStatePath() + ".objective"
	}
	return strings.TrimSpace(n.ObjectivePath)
}

func (n *PlannerNode) effectiveMaxSteps() int {
	if n == nil || n.MaxSteps <= 0 {
		return defaultPlannerMaxSteps
	}
	return n.MaxSteps
}

func (n *PlannerNode) resolveObjective(state runtime.State, plannerState runtime.State) (string, error) {
	if objective, ok := runtime.ResolveStatePath(state, n.effectiveObjectivePath()); ok {
		text := strings.TrimSpace(stringifyPlannerValue(objective))
		if text != "" {
			return text, nil
		}
	}
	if plannerState != nil {
		if objective, ok := plannerState["objective"].(string); ok && strings.TrimSpace(objective) != "" {
			return strings.TrimSpace(objective), nil
		}
	}
	return "", fmt.Errorf("planner objective not found at %q", n.effectiveObjectivePath())
}

func (n *PlannerNode) collectContext(state runtime.State) map[string]any {
	if len(n.ContextPaths) == 0 {
		return nil
	}

	contextPayload := make(map[string]any, len(n.ContextPaths))
	for _, path := range n.ContextPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		value, ok := runtime.ResolveStatePath(state, path)
		if !ok {
			continue
		}
		contextPayload[path] = value
	}
	if len(contextPayload) == 0 {
		return nil
	}
	return contextPayload
}

func buildPlannerPrompt(payload map[string]any) string {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "Generate a plan from the provided objective and context."
	}
	return "Generate a plan from the following JSON payload.\n\n" + string(data)
}

func parsePlannerResponse(content string, fallbackObjective string) (plannerResponse, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return plannerResponse{}, errors.New("planner response is empty")
	}

	candidates := []string{content, stripPlannerCodeFence(content)}
	for _, candidate := range candidates {
		if parsed, err := decodePlannerResponse(candidate, fallbackObjective); err == nil {
			return parsed, nil
		}
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return decodePlannerResponse(content[start:end+1], fallbackObjective)
	}
	return plannerResponse{}, errors.New("planner response is not valid JSON")
}

func decodePlannerResponse(content string, fallbackObjective string) (plannerResponse, error) {
	var parsed plannerResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return plannerResponse{}, err
	}
	parsed = normalizePlannerResponse(parsed, fallbackObjective)
	if len(parsed.Plan) == 0 {
		return plannerResponse{}, errors.New("planner returned an empty plan")
	}
	return parsed, nil
}

func normalizePlannerResponse(parsed plannerResponse, fallbackObjective string) plannerResponse {
	parsed.Objective = strings.TrimSpace(parsed.Objective)
	if parsed.Objective == "" {
		parsed.Objective = strings.TrimSpace(fallbackObjective)
	}
	parsed.Status = normalizePlannerStatus(parsed.Status)
	parsed.Summary = strings.TrimSpace(parsed.Summary)
	parsed.ReplanReason = strings.TrimSpace(parsed.ReplanReason)

	normalizedPlan := make([]plannerPlanStep, 0, len(parsed.Plan))
	for i, step := range parsed.Plan {
		step.ID = strings.TrimSpace(step.ID)
		if step.ID == "" {
			step.ID = fmt.Sprintf("step_%d", i+1)
		}
		step.Title = strings.TrimSpace(step.Title)
		if step.Title == "" {
			step.Title = fmt.Sprintf("Step %d", i+1)
		}
		step.Description = strings.TrimSpace(step.Description)
		step.Status = normalizePlannerStepStatus(step.Status)
		step.Kind = strings.TrimSpace(step.Kind)
		step.DependsOn = compactPlannerStrings(step.DependsOn)
		step.AcceptanceCriteria = compactPlannerStrings(step.AcceptanceCriteria)
		step.Outputs = compactPlannerStrings(step.Outputs)
		normalizedPlan = append(normalizedPlan, step)
	}
	parsed.Plan = normalizedPlan
	return parsed
}

func normalizePlannerStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "planned", "executing", "blocked", "completed", "failed":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "planned"
	}
}

func normalizePlannerStepStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "in_progress", "completed", "blocked", "skipped":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "pending"
	}
}

func applyPlannerResponse(target runtime.State, parsed plannerResponse) {
	if target == nil {
		return
	}

	target["objective"] = parsed.Objective
	target["status"] = parsed.Status
	target["summary"] = parsed.Summary
	target["replan_reason"] = parsed.ReplanReason
	target["plan"] = serializePlannerPlan(parsed.Plan)
	if len(parsed.Plan) > 0 {
		target["current_step_id"] = parsed.Plan[0].ID
	}
}

func serializePlannerPlan(steps []plannerPlanStep) []map[string]any {
	if len(steps) == 0 {
		return nil
	}

	items := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		items = append(items, map[string]any{
			"id":                  step.ID,
			"title":               step.Title,
			"description":         step.Description,
			"status":              step.Status,
			"kind":                step.Kind,
			"depends_on":          clonePlannerStrings(step.DependsOn),
			"acceptance_criteria": clonePlannerStrings(step.AcceptanceCriteria),
			"outputs":             clonePlannerStrings(step.Outputs),
		})
	}
	return items
}

func ensurePlannerStateAtPath(root runtime.State, path string) (runtime.State, error) {
	segments := runtime.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, errors.New("planner state path is required")
	}

	current := root
	for _, segment := range segments {
		switch typed := current[segment].(type) {
		case nil:
			nested := runtime.State{}
			current[segment] = nested
			current = nested
		case runtime.State:
			current = typed
		case map[string]any:
			nested := runtime.State(typed)
			current[segment] = nested
			current = nested
		default:
			return nil, fmt.Errorf("planner state path %q contains non-object segment %q (%T)", path, segment, typed)
		}
	}
	return current, nil
}

func stringifyPlannerValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func stripPlannerCodeFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```JSON")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}

func clonePlannerStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]string, len(items))
	copy(cloned, items)
	return cloned
}

func compactPlannerStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
