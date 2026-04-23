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

你的职责是规划，不是执行。不能伪造执行结果，不能假设不存在的节点、工具或数据源。

## 输入说明

你会收到一个 JSON payload，包含以下字段：
- objective：本次 agent run 的目标
- planner_state：当前规划状态（含已有计划、已完成步骤、上次失败或重规划原因）
- context：外部注入的上下文（可能包含可用节点列表、工具清单等，由调用方配置）
- max_steps：计划最大步骤数上限
- step_kind_hints：调用方建议的步骤类型范围（为空时不限制）
- additional_rules：调用方追加的规划规则（为空时忽略）

## 规划原则

1. 最小完整：步骤数量尽量少，但必须覆盖完成目标所需的全部关键动作。
2. 可执行：每步都有明确的 node_type、输入期望和可验证的验收标准。
3. 依赖正确：depends_on 必须准确，不能出现循环依赖，不能遗漏真实依赖。
4. 信息优先：先收集信息和澄清疑问，再执行高成本或不可逆操作。
5. 不猜测：缺少关键信息或节点能力不足时，用 needs_clarification 或 blocked 标记，不编造解决方案。
6. 重规划复用：如果是重规划（planner_state.replan_reason 不为空），复用已 completed 且未受影响的步骤，只重新规划受影响部分，保持原步骤 id 不变。

## 输出格式

仅输出合法 JSON，不要输出 markdown 代码块，不要输出任何解释文字。

顶层结构：
{
  “objective”: “本次目标的一句话描述”,
  “status”: “planned | needs_clarification | blocked | replanned”,
  “summary”: “整体方案的简洁说明（2-3 句）”,
  “replan_reason”: “重规划原因，非重规划时为空字符串”,
  “plan”: [ <步骤列表> ]
}

每个步骤：
{
  “id”: “step_1”,
  “title”: “简洁步骤名”,
  “description”: “具体做什么，为什么”,
  “status”: “pending | ready | completed | blocked”,
  “kind”: “research | transform | decision | action | validation | human_input”,
  “node_type”: “对应 graph 节点类型，如 llm / tools / planner / human_message，不确定则留空”,
  “depends_on”: [“被依赖步骤的 id”],
  “inputs”: [“来自上一步的哪些输出，或外部数据源”],
  “outputs”: [“本步骤产生的结果描述”],
  “acceptance_criteria”: [“具体、可由代码或 LLM 判断的完成条件”],
  “parallelizable”: false
}

## 状态说明

顶层 status：
- planned：正常规划完成
- needs_clarification：缺少用户输入或关键信息才能继续
- blocked：当前节点能力不足以完成目标
- replanned：这是一次重规划，修改了上次的计划

步骤 status（初始规划时）：
- ready：无前置依赖，可立即执行
- pending：有前置依赖尚未完成
- blocked：无法执行，需要外部解除（需在 description 说明原因）

步骤 status（重规划时还可使用）：
- completed：已完成，直接复用

## 补充要求

- id 使用稳定短标识（step_1、step_2），重规划时保持已有步骤的 id 不变
- acceptance_criteria 必须具体可检查，不能只写”完成”或”成功”
- 如果 step_kind_hints 不为空，step 的 kind 必须在此范围内选择
- parallelizable 为 true 时，该步骤与同层无依赖的步骤可并行执行
- 计划步骤数不得超过 max_steps`
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
	NodeType           string   `json:"node_type"`
	DependsOn          []string `json:"depends_on"`
	Inputs             []string `json:"inputs"`
	Outputs            []string `json:"outputs"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Parallelizable     bool     `json:"parallelizable"`
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
		llms.WithThinkingMode(llms.ThinkingModeNone),
		llms.WithReturnThinking(false),
		llms.WithTemperature(0.3),
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
		step.NodeType = strings.TrimSpace(step.NodeType)
		step.DependsOn = compactPlannerStrings(step.DependsOn)
		step.Inputs = compactPlannerStrings(step.Inputs)
		step.Outputs = compactPlannerStrings(step.Outputs)
		step.AcceptanceCriteria = compactPlannerStrings(step.AcceptanceCriteria)
		normalizedPlan = append(normalizedPlan, step)
	}
	parsed.Plan = normalizedPlan
	return parsed
}

func normalizePlannerStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "planned", "needs_clarification", "blocked", "replanned", "executing", "completed", "failed":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "planned"
	}
}

func normalizePlannerStepStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "ready", "in_progress", "completed", "blocked", "skipped":
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
			"node_type":           step.NodeType,
			"depends_on":          clonePlannerStrings(step.DependsOn),
			"inputs":              clonePlannerStrings(step.Inputs),
			"outputs":             clonePlannerStrings(step.Outputs),
			"acceptance_criteria": clonePlannerStrings(step.AcceptanceCriteria),
			"parallelizable":      step.Parallelizable,
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
