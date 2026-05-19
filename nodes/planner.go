package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultPlannerStatePath = wfstate.StateKeyPlanner
	defaultPlannerMaxSteps  = 6
	plannerSystemPrompt     = `You are the planner node inside an agent workflow.
Return JSON only. Do not use markdown fences.

You plan work; you do not execute it. Never invent completed work, unavailable tools, or nonexistent nodes.

You will receive a JSON payload with:
- objective
- planner_state
- context
- max_steps
- step_kind_hints
- additional_rules

Return this top-level JSON shape:
{
  "objective": string,
  "status": "planned" | "needs_clarification" | "blocked" | "replanned",
  "summary": string,
  "replan_reason": string,
  "plan": [ ...steps ]
}

Each step must use this shape:
{
  "id": "step_1",
  "title": string,
  "description": string,
  "status": "ready" | "pending" | "blocked" | "completed",
  "kind": "research" | "transform" | "decision" | "action" | "validation" | "human_input",
  "node_type": string,
  "depends_on": [string],
  "inputs": [string],
  "outputs": [string],
  "acceptance_criteria": [string],
  "parallelizable": boolean
}

Rules:
- Use the fewest steps that still fully cover the objective.
- Respect real dependencies. No cycles and no missing prerequisites.
- Prefer clarification or information gathering before irreversible work.
- If key information is missing, use status "needs_clarification" or blocked steps instead of guessing.
- Acceptance criteria must be concrete and checkable.
- If step_kind_hints is non-empty, every step.kind must be chosen from it.
- Do not exceed max_steps.
- During replanning, preserve unaffected completed steps and keep their existing ids when possible.`
)

type PlannerNode struct {
	NodeInfo
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

func NewPlannerNode() *PlannerNode {
	id := uuid.New()
	return &PlannerNode{
		NodeInfo: NodeInfo{
			NodeID:          "Planner_" + id.String(),
			NodeName:        "Planner",
			NodeDescription: "Generate or refresh a structured execution plan from the current objective and context.",
		},
		PlannerStatePath: defaultPlannerStatePath,
		MaxSteps:         defaultPlannerMaxSteps,
	}
}

func (n *PlannerNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	svc := core.ServicesFrom(ctx)
	if svc == nil || svc.Model == nil {
		return state, errors.New("planner: model service not available")
	}
	if state == nil {
		state = wfstate.State{}
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

	resp, err := svc.Model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, plannerSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, buildPlannerPrompt(promptPayload)),
		},
		llms.WithJSONMode(),
		llms.WithThinkingMode(llms.ThinkingModeNone),
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
	_ = RecordChoiceUsage(ctx, state, Record{
		NodeID: n.ID(),
		Model:  modelLabel(svc.Model),
	}, resp.Choices[0])

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
	publishPlannerProgress(ctx, plannerPath, plannerState, "planned", parsed.Summary)
	_, _ = runtime.SaveJSONArtifactBestEffort(ctx, "planner.response", parsed)

	return state, nil
}

func (n *PlannerNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return n.execute(ctx, state)
	})
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

func (n *PlannerNode) resolveObjective(state wfstate.State, plannerState wfstate.State) (string, error) {
	if objective, ok := state.ResolvePath(n.effectiveObjectivePath()); ok {
		text := strings.TrimSpace(stringifyPlannerValue(objective))
		if text != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("planner objective not found at %q", n.effectiveObjectivePath())
}

func (n *PlannerNode) collectContext(state wfstate.State) map[string]any {
	if len(n.ContextPaths) == 0 {
		return nil
	}

	contextPayload := make(map[string]any, len(n.ContextPaths))
	for _, path := range n.ContextPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		value, ok := state.ResolvePath(path)
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
	case "planned", "needs_clarification", "blocked", "replanning", "replanned", "executing", "completed", "failed":
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

func applyPlannerResponse(target wfstate.State, parsed plannerResponse) {
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

func ensurePlannerStateAtPath(root wfstate.State, path string) (wfstate.State, error) {
	segments := wfstate.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, errors.New("planner state path is required")
	}

	current := root
	for _, segment := range segments {
		switch typed := current[segment].(type) {
		case nil:
			nested := wfstate.State{}
			current[segment] = nested
			current = nested
		case wfstate.State:
			current = typed
		case map[string]any:
			nested := wfstate.State(typed)
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
