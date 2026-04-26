package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

const (
	defaultPlanStepExecutorScope = "default"

	ExecutionRouteLLM          = "llm"
	ExecutionRouteLLMWithTools = "llm_with_tools"
	ExecutionRouteVerifier     = "verifier"
	ExecutionRouteHuman        = "human"
	ExecutionRouteFinalize     = "finalize"
	ExecutionRouteBlocked      = "blocked"
)

type PlanStepExecutorNode struct {
	NodeInfo
	StateScope       string
	PlannerStatePath string
}

func NewPlanStepExecutorNode() *PlanStepExecutorNode {
	id := uuid.New()
	return &PlanStepExecutorNode{
		NodeInfo: NodeInfo{
			NodeID:          "PlanStepExecutor_" + id.String(),
			NodeName:        "PlanStepExecutor",
			NodeDescription: "Select the next ready step from the plan and route execution.",
		},
	}
}

func (n *PlanStepExecutorNode) effectiveScope() string {
	if n == nil || strings.TrimSpace(n.StateScope) == "" {
		return defaultPlanStepExecutorScope
	}
	return strings.TrimSpace(n.StateScope)
}

func (n *PlanStepExecutorNode) effectivePlannerPath() string {
	if n == nil || strings.TrimSpace(n.PlannerStatePath) == "" {
		return fruntime.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *PlanStepExecutorNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
	}

	plannerPath := n.effectivePlannerPath()
	plannerState := state.Get(fruntime.StateKeyPlanner)
	if plannerState == nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "plan_step_executor.error", map[string]any{
			"error": "planner state not found",
			"path":  plannerPath,
		})
		return state, fmt.Errorf("plan_step_executor: planner state not found at %q", plannerPath)
	}

	plan := extractPlanSteps(plannerState)
	if len(plan) == 0 {
		return n.routeFinalize(ctx, state, plannerState, "empty plan")
	}

	stepResults := fruntime.StepResults(state)

	selectedStep, reason := selectNextStep(plan, stepResults)
	if selectedStep == nil {
		if allStepsCompleted(plan) {
			return n.routeFinalize(ctx, state, plannerState, "all steps completed")
		}
		return n.routeBlocked(ctx, state, plannerState, reason)
	}

	selectedStep["status"] = "running"
	stepID, _ := selectedStep["id"].(string)
	kind, _ := selectedStep["kind"].(string)
	route := routeForKind(kind)

	plannerState["current_step_id"] = stepID

	exec := state.Ensure(fruntime.StateKeyExecution)
	exec["current_step"] = cloneStepMap(selectedStep)
	exec["route"] = route

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "plan_step_executor.selection", map[string]any{
		"step_id":     stepID,
		"step_kind":   kind,
		"route":       route,
		"reason":      "dependency satisfied, step ready",
		"selected_at": time.Now().Format(time.RFC3339),
	})

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":    "plan_step_selected",
		"step_id": stepID,
		"route":   route,
	})

	return state, nil
}

func (n *PlanStepExecutorNode) routeFinalize(ctx context.Context, state fruntime.State, plannerState fruntime.State, reason string) (fruntime.State, error) {
	exec := state.Ensure(fruntime.StateKeyExecution)
	exec["route"] = ExecutionRouteFinalize
	exec["current_step"] = nil
	plannerState["current_step_id"] = ""

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "plan_step_executor.selection", map[string]any{
		"route":  ExecutionRouteFinalize,
		"reason": reason,
	})

	return state, nil
}

func (n *PlanStepExecutorNode) routeBlocked(ctx context.Context, state fruntime.State, plannerState fruntime.State, reason string) (fruntime.State, error) {
	exec := state.Ensure(fruntime.StateKeyExecution)
	exec["route"] = ExecutionRouteBlocked
	exec["current_step"] = nil
	plannerState["current_step_id"] = ""

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "plan_step_executor.selection", map[string]any{
		"route":  ExecutionRouteBlocked,
		"reason": reason,
	})

	return state, nil
}

func (n *PlanStepExecutorNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{}
	if scope := n.effectiveScope(); scope != defaultPlanStepExecutorScope {
		config["state_scope"] = scope
	}
	if plannerPath := n.effectivePlannerPath(); plannerPath != fruntime.StateKeyPlanner {
		config["planner_state_path"] = plannerPath
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "plan_step_executor",
		Description: n.Description(),
		Config:      config,
	}
}

// --- helpers ---

func extractPlanSteps(plannerState fruntime.State) []map[string]any {
	raw, ok := plannerState["plan"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	default:
		return nil
	}
}

func selectNextStep(plan []map[string]any, stepResults map[string]any) (selected map[string]any, reason string) {
	completedIDs := completedStepIDs(plan, stepResults)

	for _, step := range plan {
		status, _ := step["status"].(string)
		switch status {
		case "completed", "blocked", "running":
			continue
		}

		if dependenciesMet(step, completedIDs) {
			return step, ""
		}
	}
	return nil, "no step with satisfied dependencies found"
}

func dependenciesMet(step map[string]any, completedIDs map[string]bool) bool {
	raw, ok := step["depends_on"]
	if !ok {
		return true
	}
	var deps []string
	switch typed := raw.(type) {
	case []string:
		deps = typed
	case []any:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				deps = append(deps, s)
			}
		}
	default:
		return true
	}

	for _, dep := range deps {
		if !completedIDs[dep] {
			return false
		}
	}
	return true
}

func completedStepIDs(plan []map[string]any, stepResults map[string]any) map[string]bool {
	ids := make(map[string]bool)
	for _, step := range plan {
		status, _ := step["status"].(string)
		if status == "completed" {
			if id, ok := step["id"].(string); ok {
				ids[id] = true
			}
		}
	}
	for id := range stepResults {
		ids[id] = true
	}
	return ids
}

func allStepsCompleted(plan []map[string]any) bool {
	for _, step := range plan {
		status, _ := step["status"].(string)
		if status != "completed" {
			return false
		}
	}
	return true
}

func routeForKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "research", "decision":
		return ExecutionRouteLLM
	case "transform", "action":
		return ExecutionRouteLLMWithTools
	case "validation":
		return ExecutionRouteVerifier
	case "human_input":
		return ExecutionRouteHuman
	default:
		return ExecutionRouteLLMWithTools
	}
}

func cloneStepMap(step map[string]any) map[string]any {
	if step == nil {
		return nil
	}
	cloned := make(map[string]any, len(step))
	for k, v := range step {
		cloned[k] = v
	}
	return cloned
}
