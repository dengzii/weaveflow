package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultPlannerStatePath = fruntime.StateKeyPlanner
	defaultPlannerMaxSteps  = 6
	plannerSystemPrompt     = "" +
		"You are a planning node inside a flexible agent framework. " +
		"Return only valid JSON and do not use markdown fences. " +
		"Produce a concise execution plan using the shape " +
		"{\"objective\":string,\"status\":string,\"summary\":string,\"replan_reason\":string,\"plan\":[...]}. " +
		"Each plan step must use the shape " +
		"{\"id\":string,\"title\":string,\"description\":string,\"status\":string,\"kind\":string," +
		"\"depends_on\":[],\"acceptance_criteria\":[],\"outputs\":[]}. " +
		"Use pending for unfinished steps and planned for the overall planner status."
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

func (n *PlannerNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if n.model == nil {
		return state, errors.New("planner model is nil")
	}
	if state == nil {
		state = fruntime.State{}
	}

	plannerPath := n.effectivePlannerStatePath()
	plannerState, err := ensurePlannerStateAtPath(state, plannerPath)
	if err != nil {
		return state, err
	}

	objective, err := n.resolveObjective(state, plannerState)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{"error": err.Error()})
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
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.prompt", promptPayload)

	resp, err := n.model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, plannerSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, buildPlannerPrompt(promptPayload)),
		},
		llms.WithTemperature(0),
	)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{"error": err.Error()})
		return state, err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		err = errors.New("planner returned no choices")
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{"error": err.Error()})
		return state, err
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	parsed, err := parsePlannerResponse(content, objective)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.error", map[string]any{
			"error":    err.Error(),
			"response": content,
		})
		return state, err
	}

	applyPlannerResponse(plannerState, parsed)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":         "planner",
		"planner_path": plannerPath,
		"status":       parsed.Status,
		"step_count":   len(parsed.Plan),
	})
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.response", parsed)

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

func (n *PlannerNode) resolveObjective(state fruntime.State, plannerState fruntime.State) (string, error) {
	if objective, ok := fruntime.ResolveStatePath(state, n.effectiveObjectivePath()); ok {
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

func (n *PlannerNode) collectContext(state fruntime.State) map[string]any {
	if len(n.ContextPaths) == 0 {
		return nil
	}

	contextPayload := make(map[string]any, len(n.ContextPaths))
	for _, path := range n.ContextPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		value, ok := fruntime.ResolveStatePath(state, path)
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

func applyPlannerResponse(target fruntime.State, parsed plannerResponse) {
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

func ensurePlannerStateAtPath(root fruntime.State, path string) (fruntime.State, error) {
	segments := fruntime.SplitStatePath(path)
	if len(segments) == 0 {
		return nil, errors.New("planner state path is required")
	}

	current := root
	for _, segment := range segments {
		switch typed := current[segment].(type) {
		case nil:
			nested := fruntime.State{}
			current[segment] = nested
			current = nested
		case fruntime.State:
			current = typed
		case map[string]any:
			nested := fruntime.State(typed)
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
