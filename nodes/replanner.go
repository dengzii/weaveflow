package nodes

import (
	"context"
	"fmt"
	"strings"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
)

const defaultReplannerMaxLoopForSameTarget = 2

type ReplannerNode struct {
	NodeInfo
	inner            *PlannerNode
	PlannerStatePath string
	ContextPaths     []string
	MaxSteps         int
	StepKindHints    []string
	Instructions     string
	// MaxLoopForSameTarget bounds how many times the replanner is willing
	// to regenerate a plan that targets the same step.inputs signature
	// before declaring the planner blocked. Zero means use the default (2).
	MaxLoopForSameTarget int
}

func NewReplannerNode() *ReplannerNode {
	id := uuid.New()
	return &ReplannerNode{
		NodeInfo: NodeInfo{
			NodeID:          "Replanner_" + id.String(),
			NodeName:        "Replanner",
			NodeDescription: "Replan based on verification failures, preserving completed steps.",
		},
		inner: NewPlannerNode(),
	}
}

func (n *ReplannerNode) execute(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	if state == nil {
		state = wfstate.State{}
	}

	replanReason := n.buildReplanReason(state)
	plannerPath := n.effectivePlannerStatePath()

	plannerState, err := ensurePlannerStateAtPath(state, plannerPath)
	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "replanner.error", map[string]any{
			"error":        err.Error(),
			"planner_path": plannerPath,
		})
		return state, fmt.Errorf("replanner: %w", err)
	}

	plannerState["replan_reason"] = replanReason
	plannerState["status"] = "replanning"
	publishPlannerProgress(ctx, plannerPath, plannerState, "replanning", replanReason)

	// Anti-loop: if we have re-planned the same step-input signature too
	// many times already, stop calling the inner planner. Mark the current
	// step blocked and let downstream PlanStepExecutor route the run to
	// finalize, so the user gets a partial answer instead of an unbounded
	// replan loop.
	signature := currentStepReplanSignature(plannerState)
	if n.recordAndCheckReplanLoop(plannerState, signature) {
		blockReason := fmt.Sprintf("replanner gave up after %d attempts on the same target (%s); marking step blocked to surface partial findings", n.effectiveMaxLoopForSameTarget(), signature)
		markCurrentStepBlockedInPlanner(plannerState)
		plannerState["status"] = "blocked"
		publishPlannerProgress(ctx, plannerPath, plannerState, "blocked", blockReason)
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "replanner.loop_guard", map[string]any{
			"signature":     signature,
			"replan_reason": replanReason,
			"block_reason":  blockReason,
		})
		return state, nil
	}

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "replanner.context", map[string]any{
		"replan_reason": replanReason,
		"planner_path":  plannerPath,
	})

	n.configureInner()
	result, err := n.inner.execute(ctx, state)

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, map[string]any{
		"kind":          "replanner",
		"planner_path":  plannerPath,
		"replan_reason": replanReason,
	})

	if err != nil {
		_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "replanner.error", map[string]any{
			"error":         err.Error(),
			"replan_reason": replanReason,
		})
	} else {
		publishPlannerProgress(ctx, plannerPath, stateObjectAtPath(result, plannerPath), "replanned", replanReason)
	}

	return result, err
}

func (n *ReplannerNode) Execute(ctx context.Context, input wfstate.State) (wfstate.StatePatch, error) {
	return executeStatePatch(input, func(state wfstate.State) (wfstate.State, error) {
		return n.execute(ctx, state)
	})
}

func (n *ReplannerNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"planner_state_path": n.effectivePlannerStatePath(),
	}
	if n.MaxSteps > 0 {
		config["max_steps"] = n.MaxSteps
	}
	if len(n.ContextPaths) > 0 {
		config["context_paths"] = append([]string(nil), n.ContextPaths...)
	}
	if len(n.StepKindHints) > 0 {
		config["step_kind_hints"] = append([]string(nil), n.StepKindHints...)
	}
	if instructions := strings.TrimSpace(n.Instructions); instructions != "" {
		config["instructions"] = instructions
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "replanner",
		Description: n.Description(),
		Config:      config,
	}
}

func (n *ReplannerNode) effectivePlannerStatePath() string {
	if path := strings.TrimSpace(n.PlannerStatePath); path != "" {
		return path
	}
	return wfstate.StateKeyPlanner
}

func (n *ReplannerNode) effectiveMaxLoopForSameTarget() int {
	if n == nil || n.MaxLoopForSameTarget <= 0 {
		return defaultReplannerMaxLoopForSameTarget
	}
	return n.MaxLoopForSameTarget
}

// currentStepReplanSignature returns a stable key identifying what the
// current step is trying to accomplish: kind + inputs. We use this to
// detect "we're replanning the same target over and over" loops.
func currentStepReplanSignature(plannerState wfstate.State) string {
	if plannerState == nil {
		return ""
	}
	stepID, _ := plannerState["current_step_id"].(string)
	if stepID == "" {
		return ""
	}
	step := findStepByID(plannerState, stepID)
	if step == nil {
		return ""
	}
	kind := strings.TrimSpace(stringFromMap(step, "kind"))
	inputs := extractStringSlice(step, "inputs")
	return kind + "::" + strings.Join(inputs, "|")
}

// recordAndCheckReplanLoop bumps the counter for `signature` inside the
// planner state's "replan_history" array, and returns true when the new
// count strictly exceeds the configured budget — meaning we've already
// re-planned this same target N+1 times and should stop.
func (n *ReplannerNode) recordAndCheckReplanLoop(plannerState wfstate.State, signature string) bool {
	if plannerState == nil || signature == "" {
		return false
	}
	historyAny, _ := plannerState["replan_history"].([]any)
	updated := make([]any, 0, len(historyAny)+1)
	matched := false
	count := 1
	for _, entry := range historyAny {
		row, ok := entry.(map[string]any)
		if !ok {
			updated = append(updated, entry)
			continue
		}
		if sig, _ := row["signature"].(string); sig == signature {
			if existing, ok := row["count"].(int); ok {
				count = existing + 1
			} else if existing, ok := row["count"].(float64); ok {
				count = int(existing) + 1
			}
			row["count"] = count
			matched = true
		}
		updated = append(updated, row)
	}
	if !matched {
		updated = append(updated, map[string]any{
			"signature": signature,
			"count":     count,
		})
	}
	plannerState["replan_history"] = updated
	return count > n.effectiveMaxLoopForSameTarget()
}

// markCurrentStepBlockedInPlanner is a tiny helper local to the
// replanner so we don't import the verifier just to flip a step status.
func markCurrentStepBlockedInPlanner(plannerState wfstate.State) {
	if plannerState == nil {
		return
	}
	stepID, _ := plannerState["current_step_id"].(string)
	if stepID == "" {
		return
	}
	step := findStepByID(plannerState, stepID)
	if step != nil {
		step["status"] = "blocked"
	}
}

func (n *ReplannerNode) configureInner() {
	n.inner.PlannerStatePath = n.effectivePlannerStatePath()
	n.inner.ContextPaths = n.effectiveContextPaths()
	if n.MaxSteps > 0 {
		n.inner.MaxSteps = n.MaxSteps
	}
	n.inner.StepKindHints = n.StepKindHints
	if instructions := strings.TrimSpace(n.Instructions); instructions != "" {
		n.inner.Instructions = instructions
	}
}

func (n *ReplannerNode) effectiveContextPaths() []string {
	base := []string{
		wfstate.StateKeyVerification,
		wfstate.StateKeyObservations,
		wfstate.StateKeyExecution + ".step_results",
	}
	for _, path := range n.ContextPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !containsPath(base, path) {
			base = append(base, path)
		}
	}
	return base
}

func containsPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}

func (n *ReplannerNode) buildReplanReason(state wfstate.State) string {
	var parts []string

	verification := state.Get(wfstate.StateKeyVerification)
	if verification != nil {
		if summary, ok := verification["summary"].(string); ok && summary != "" {
			parts = append(parts, "Verification: "+summary)
		}
		if issues := extractReplanIssues(verification); len(issues) > 0 {
			parts = append(parts, "Issues: "+strings.Join(issues, "; "))
			if verificationIssuesSuggestBroadStep(issues) {
				parts = append(parts, "Replanning directive: split the failed broad step into smaller pending steps by package, directory, or concept. Preserve completed steps and do not recreate the same broad step.")
			}
		}
	}

	observations := state.Observations()
	errorObs := filterErrorObservations(observations)
	if len(errorObs) > 0 {
		parts = append(parts, fmt.Sprintf("Failed observations: %d", len(errorObs)))
		for _, obs := range errorObs {
			if summary, ok := obs["summary"].(string); ok && summary != "" {
				parts = append(parts, "  - "+summary)
			}
		}
	}

	if len(parts) == 0 {
		return "Verification triggered replan (no specific details available)"
	}
	return strings.Join(parts, "\n")
}

func verificationIssuesSuggestBroadStep(issues []string) bool {
	for _, issue := range issues {
		lower := strings.ToLower(strings.TrimSpace(issue))
		if lower == "" {
			continue
		}
		for _, marker := range broadStepMarkers() {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func extractReplanIssues(verification wfstate.State) []string {
	raw, ok := verification["issues"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func filterErrorObservations(observations []map[string]any) []map[string]any {
	if len(observations) == 0 {
		return nil
	}
	var errors []map[string]any
	for _, obs := range observations {
		if hasError, ok := obs["error"].(bool); ok && hasError {
			errors = append(errors, obs)
			continue
		}
		if errMsg, ok := obs["error"].(string); ok && errMsg != "" {
			errors = append(errors, obs)
		}
	}
	return errors
}
