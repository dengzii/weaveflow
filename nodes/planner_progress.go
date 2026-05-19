package nodes

import (
	"context"
	"math"
	"strings"

	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
)

const plannerProgressEventKind = "planner_progress"

func publishPlannerProgress(ctx context.Context, plannerPath string, plannerState wfstate.State, phase string, message string) {
	if plannerState == nil {
		return
	}
	payload := buildPlannerProgressPayload(plannerPath, plannerState, phase, message)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventNodeCustom, payload)
	_, _ = fruntime.SaveJSONArtifactBestEffort(ctx, "planner.progress", payload)
}

func buildPlannerProgressPayload(plannerPath string, plannerState wfstate.State, phase string, message string) map[string]any {
	steps := plannerProgressSteps(plannerState)
	counts := plannerProgressCounts(steps)
	total, _ := counts["total"].(int)
	completed, _ := counts["completed"].(int)
	percent := 0
	if total > 0 {
		percent = int(math.Round(float64(completed) * 100 / float64(total)))
	}

	currentStepID, _ := plannerState["current_step_id"].(string)
	currentStep := findPlannerProgressStep(steps, currentStepID)
	if currentStep == nil {
		currentStep = firstPlannerProgressStepByStatus(steps, "in_progress")
	}
	if currentStep == nil {
		currentStep = firstPlannerProgressStepByStatus(steps, "ready")
	}
	if currentStep != nil && strings.TrimSpace(currentStepID) == "" {
		if id, _ := currentStep["id"].(string); id != "" {
			currentStepID = id
		}
	}

	return map[string]any{
		"kind":            plannerProgressEventKind,
		"phase":           strings.TrimSpace(phase),
		"message":         strings.TrimSpace(message),
		"planner_path":    strings.TrimSpace(plannerPath),
		"objective":       plannerStateString(plannerState, "objective"),
		"status":          plannerStateString(plannerState, "status"),
		"summary":         plannerStateString(plannerState, "summary"),
		"replan_reason":   plannerStateString(plannerState, "replan_reason"),
		"current_step_id": currentStepID,
		"current_step":    currentStep,
		"steps":           steps,
		"counts":          counts,
		"percent":         percent,
	}
}

func plannerProgressSteps(plannerState wfstate.State) []map[string]any {
	plan := extractPlanSteps(plannerState)
	if len(plan) == 0 {
		return nil
	}

	steps := make([]map[string]any, 0, len(plan))
	for _, step := range plan {
		if step == nil {
			continue
		}
		steps = append(steps, map[string]any{
			"id":          stringFromMap(step, "id"),
			"title":       stringFromMap(step, "title"),
			"description": stringFromMap(step, "description"),
			"status":      stringFromMap(step, "status"),
			"kind":        stringFromMap(step, "kind"),
		})
	}
	return steps
}

func plannerProgressCounts(steps []map[string]any) map[string]any {
	counts := map[string]any{
		"total":       len(steps),
		"pending":     0,
		"ready":       0,
		"in_progress": 0,
		"completed":   0,
		"blocked":     0,
		"skipped":     0,
	}
	for _, step := range steps {
		status, _ := step["status"].(string)
		status = strings.TrimSpace(strings.ToLower(status))
		if _, ok := counts[status]; ok {
			counts[status] = counts[status].(int) + 1
		}
	}
	return counts
}

func findPlannerProgressStep(steps []map[string]any, id string) map[string]any {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for _, step := range steps {
		if stepID, _ := step["id"].(string); stepID == id {
			return step
		}
	}
	return nil
}

func firstPlannerProgressStepByStatus(steps []map[string]any, status string) map[string]any {
	for _, step := range steps {
		if stepStatus, _ := step["status"].(string); strings.EqualFold(strings.TrimSpace(stepStatus), status) {
			return step
		}
	}
	return nil
}

func plannerStateString(state wfstate.State, key string) string {
	if state == nil {
		return ""
	}
	value, _ := state[key].(string)
	return strings.TrimSpace(value)
}

func stringFromMap(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}
