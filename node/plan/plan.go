// Package plan provides planning, review, and synthesis graph nodes.
package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

const (
	NodeTypePlanGenerator = "plan_generator"
	NodeTypePlanStep      = "plan_step"
	NodeTypePlanReview    = "plan_review"
	NodeTypePlanSynthesis = "plan_synthesis"

	ConditionTypePlanStatusEquals = "plan_status_equals"
)

const (
	PlanStatusPlanning   = "planning"
	PlanStatusExecuting  = "executing"
	PlanStatusReplan     = "replan"
	PlanStatusFinalizing = "finalizing"
	PlanStatusDone       = "done"

	PlanStepStatusPending = "pending"
	PlanStepStatusRunning = "running"
	PlanStepStatusDone    = "done"
	PlanStepStatusFailed  = "failed"
)

const (
	planFieldObjective    = plancap.FieldObjective
	planFieldStatus       = plancap.FieldStatus
	planFieldSummary      = plancap.FieldSummary
	planFieldSteps        = plancap.FieldSteps
	planFieldHistory      = plancap.FieldHistory
	planFieldCurrentIndex = plancap.FieldCurrentIndex
	planFieldReplanCount  = plancap.FieldReplanCount
	planFieldMaxReplans   = plancap.FieldMaxReplans
	planFieldReplanReason = plancap.FieldReplanReason
	planFieldFinalAnswer  = plancap.FieldFinalAnswer
)

type modelOutput struct {
	Summary string         `json:"summary"`
	Steps   []plancap.Step `json:"steps"`
}

func modelOutputSchema() state.JSONSchema {
	return state.JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{"type": "string"},
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string"},
						"title":       map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"tool_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required":             []string{"id", "title", "description", "tool_ids"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"summary", "steps"},
		"additionalProperties": false,
	}
}

func StatusEquals(planPath state.Path, status string) registry.EdgeCondition {
	status = strings.ToLower(strings.TrimSpace(status))
	return registry.NewEdgeCondition(dsl.GraphConditionSpec{
		Type:   ConditionTypePlanStatusEquals,
		Config: map[string]any{"status": status},
		State:  map[string]dsl.StateBinding{"plan": {Path: planPath.String()}},
	}, func(_ context.Context, current *state.State) (registry.RouteDecision, error) {
		value, ok := state.ReadPath(current, planPath.MustChild(plancap.FieldStatus).String())
		if !ok {
			return registry.RouteDecision{Reason: "plan status is missing"}, nil
		}
		actual, _ := value.(string)
		matched := strings.EqualFold(strings.TrimSpace(actual), status)
		return registry.RouteDecision{Matched: matched, Reason: "plan status compared"}, nil
	})
}

func parsePlanModelOutput(content string) (modelOutput, error) {
	content = strings.TrimSpace(stripPlanJSONFence(content))
	if content == "" {
		return modelOutput{}, errors.New("empty content")
	}

	var output modelOutput
	if err := json.Unmarshal([]byte(content), &output); err == nil {
		return output, nil
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(content[start:end+1]), &output); err == nil {
			return output, nil
		}
	}
	return modelOutput{}, errors.New("content is not valid plan JSON")
}

func normalizePlanSteps(steps []plancap.Step, maxSteps int, knownTools map[string]struct{}) []plancap.Step {
	if maxSteps <= 0 {
		maxSteps = len(steps)
	}
	normalized := make([]plancap.Step, 0, min(len(steps), maxSteps))
	seenIDs := map[string]int{}
	for _, step := range steps {
		if len(normalized) >= maxSteps {
			break
		}
		step.Title = strings.TrimSpace(step.Title)
		step.Description = strings.TrimSpace(step.Description)
		if step.Title == "" {
			step.Title = step.Description
		}
		if step.Description == "" {
			step.Description = step.Title
		}
		if step.Title == "" {
			continue
		}

		baseID := strings.TrimSpace(step.ID)
		if baseID == "" {
			baseID = fmt.Sprintf("step_%d", len(normalized)+1)
		}
		seenIDs[baseID]++
		step.ID = baseID
		if seenIDs[baseID] > 1 {
			step.ID = fmt.Sprintf("%s_%d", baseID, seenIDs[baseID])
		}

		toolIDs := make([]string, 0, len(step.ToolIDs))
		seenTools := map[string]struct{}{}
		for _, toolID := range step.ToolIDs {
			toolID = strings.TrimSpace(toolID)
			lookup := strings.ToLower(toolID)
			if toolID == "" {
				continue
			}
			if knownTools != nil {
				if _, ok := knownTools[lookup]; !ok {
					continue
				}
			}
			if _, ok := seenTools[lookup]; ok {
				continue
			}
			seenTools[lookup] = struct{}{}
			toolIDs = append(toolIDs, toolID)
		}
		step.ToolIDs = toolIDs
		step.Status = PlanStepStatusPending
		step.Result = ""
		step.Error = ""
		normalized = append(normalized, step)
	}
	return normalized
}

func stripPlanJSONFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```JSON")
	content = strings.TrimPrefix(content, "```")
	if index := strings.LastIndex(content, "```"); index >= 0 {
		content = content[:index]
	}
	return strings.TrimSpace(content)
}

func stepsFromValue(value any) []plancap.Step {
	return plancap.DecodeSteps(value)
}

func stepMaps(steps []plancap.Step) []map[string]any {
	return plancap.EncodeSteps(steps)
}

func mapSlice(value any) []map[string]any {
	return plancap.DecodeObjectList(value)
}

func toolDescriptions(available map[string]core.Tool) []map[string]any {
	if len(available) == 0 {
		return nil
	}
	ids := make([]string, 0, len(available))
	for id := range available {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	descriptions := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		tool := available[id]
		if tool.Function == nil {
			continue
		}
		description := strings.TrimSpace(tool.Function.Description)
		if index := strings.IndexByte(description, '\n'); index >= 0 {
			description = description[:index]
		}
		descriptions = append(descriptions, map[string]any{
			"id":          id,
			"name":        tool.Name(),
			"description": description,
		})
	}
	return descriptions
}

func toolNames(available map[string]core.Tool) map[string]struct{} {
	names := make(map[string]struct{}, len(available)*2)
	for id, tool := range available {
		names[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
		if name := strings.ToLower(strings.TrimSpace(tool.Name())); name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func textLimit(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}
