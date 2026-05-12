package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

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
		return wfstate.StateKeyPlanner
	}
	return strings.TrimSpace(n.PlannerStatePath)
}

func (n *PlanStepExecutorNode) Invoke(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	if state == nil {
		state = wfstate.State{}
	}

	plannerPath := n.effectivePlannerPath()
	plannerState := stateObjectAtPath(state, plannerPath)
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

	stepResults := state.StepResults()

	selectedStep, reason := selectNextStep(plan, stepResults)
	if selectedStep == nil {
		if allStepsCompleted(plan) {
			return n.routeFinalize(ctx, state, plannerState, "all steps completed")
		}
		return n.routeBlocked(ctx, state, plannerState, reason, buildBlockedDiagnostics(plan, stepResults))
	}

	selectedStep["status"] = "in_progress"
	stepID, _ := selectedStep["id"].(string)
	kind, _ := selectedStep["kind"].(string)
	route := routeForKind(kind)

	plannerState["current_step_id"] = stepID

	exec := state.Ensure(wfstate.StateKeyExecution)
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

func (n *PlanStepExecutorNode) routeFinalize(ctx context.Context, state wfstate.State, plannerState wfstate.State, reason string) (wfstate.State, error) {
	exec := state.Ensure(wfstate.StateKeyExecution)
	exec["route"] = ExecutionRouteFinalize
	exec["current_step"] = nil
	plannerState["current_step_id"] = ""

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "plan_step_executor.selection", map[string]any{
		"route":  ExecutionRouteFinalize,
		"reason": reason,
	})

	return state, nil
}

func (n *PlanStepExecutorNode) routeBlocked(ctx context.Context, state wfstate.State, plannerState wfstate.State, reason string, diagnostics map[string]any) (wfstate.State, error) {
	exec := state.Ensure(wfstate.StateKeyExecution)
	exec["route"] = ExecutionRouteBlocked
	exec["current_step"] = nil
	plannerState["current_step_id"] = ""
	if diagnostics != nil {
		exec["blocked_diagnostics"] = diagnostics
	} else {
		delete(exec, "blocked_diagnostics")
	}

	artifact := map[string]any{
		"route":  ExecutionRouteBlocked,
		"reason": reason,
	}
	if diagnostics != nil {
		artifact["diagnostics"] = diagnostics
	}
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "plan_step_executor.selection", artifact)

	return state, nil
}

func (n *PlanStepExecutorNode) Execute(ctx context.Context, input wfstate.State) (wfstate.State, error) {
	return wfstate.LegacyNodeExecutor{Invoke: n.Invoke}.Execute(ctx, input)
}

func (n *PlanStepExecutorNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{}
	if scope := n.effectiveScope(); scope != defaultPlanStepExecutorScope {
		config["state_scope"] = scope
	}
	if plannerPath := n.effectivePlannerPath(); plannerPath != wfstate.StateKeyPlanner {
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

func extractPlanSteps(plannerState wfstate.State) []map[string]any {
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
		case "completed", "blocked", "running", "in_progress":
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
		id, _ := step["id"].(string)
		if id == "" {
			continue
		}
		status, _ := step["status"].(string)
		if status == "completed" {
			ids[id] = true
			continue
		}
		if (status == "in_progress" || status == "running") && stepResultExists(stepResults, id) {
			ids[id] = true
		}
	}
	return ids
}

func stepResultExists(stepResults map[string]any, stepID string) bool {
	if len(stepResults) == 0 || stepID == "" {
		return false
	}
	result, ok := stepResults[stepID]
	if !ok || result == nil {
		return false
	}
	return true
}

func buildBlockedDiagnostics(plan []map[string]any, stepResults map[string]any) map[string]any {
	completedIDs := completedStepIDs(plan, stepResults)
	stepStatuses := make(map[string]any, len(plan))
	stepStatusLookup := make(map[string]map[string]any, len(plan))
	waiting := make([]map[string]any, 0)

	for _, step := range plan {
		id, _ := step["id"].(string)
		if id == "" {
			continue
		}
		status, _ := step["status"].(string)
		statusInfo := map[string]any{
			"status":               status,
			"has_step_result":      stepResultExists(stepResults, id),
			"dependency_satisfied": completedIDs[id],
		}
		stepStatuses[id] = statusInfo
		stepStatusLookup[id] = statusInfo

		switch status {
		case "completed", "blocked", "running", "in_progress":
			continue
		}

		missing := missingDependencies(step, completedIDs, stepStatusLookup)
		if len(missing) == 0 {
			continue
		}

		waiting = append(waiting, map[string]any{
			"step_id":      id,
			"title":        stringifyStepField(step["title"]),
			"status":       status,
			"depends_on":   extractStepDependencyIDs(step),
			"missing_deps": missing,
		})
	}

	if len(waiting) == 0 && len(stepStatuses) == 0 {
		return nil
	}

	return map[string]any{
		"waiting_steps": waiting,
		"step_statuses": stepStatuses,
	}
}

func missingDependencies(step map[string]any, completedIDs map[string]bool, stepStatusLookup map[string]map[string]any) []map[string]any {
	deps := extractStepDependencyIDs(step)
	if len(deps) == 0 {
		return nil
	}

	missing := make([]map[string]any, 0)
	for _, dep := range deps {
		if completedIDs[dep] {
			continue
		}
		entry := map[string]any{"step_id": dep}
		if info := stepStatusLookup[dep]; info != nil {
			for k, v := range info {
				entry[k] = v
			}
		}
		missing = append(missing, entry)
	}
	return missing
}

func extractStepDependencyIDs(step map[string]any) []string {
	raw, ok := step["depends_on"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		deps := make([]string, 0, len(typed))
		for _, item := range typed {
			if dep, ok := item.(string); ok && strings.TrimSpace(dep) != "" {
				deps = append(deps, dep)
			}
		}
		return deps
	default:
		return nil
	}
}

func stringifyStepField(value any) string {
	text, _ := value.(string)
	return text
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
