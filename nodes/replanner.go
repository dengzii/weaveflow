package nodes

import (
	"context"
	"fmt"
	"strings"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

type ReplannerNode struct {
	NodeInfo
	inner            *PlannerNode
	PlannerStatePath string
	ContextPaths     []string
	MaxSteps         int
	StepKindHints    []string
	Instructions     string
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

func (n *ReplannerNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if state == nil {
		state = fruntime.State{}
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

	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "replanner.context", map[string]any{
		"replan_reason": replanReason,
		"planner_path":  plannerPath,
	})

	n.configureInner()
	result, err := n.inner.Invoke(ctx, state)

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
	}

	return result, err
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
	return fruntime.StateKeyPlanner
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
		fruntime.StateKeyVerification,
		fruntime.StateKeyObservations,
		fruntime.StateKeyExecution + ".step_results",
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

func (n *ReplannerNode) buildReplanReason(state fruntime.State) string {
	var parts []string

	verification := state.Get(fruntime.StateKeyVerification)
	if verification != nil {
		if summary, ok := verification["summary"].(string); ok && summary != "" {
			parts = append(parts, "Verification: "+summary)
		}
		if issues := extractReplanIssues(verification); len(issues) > 0 {
			parts = append(parts, "Issues: "+strings.Join(issues, "; "))
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

func extractReplanIssues(verification fruntime.State) []string {
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
