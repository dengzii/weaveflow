// Package plan provides planning, review, and synthesis graph nodes.
package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	conversationcap "github.com/dengzii/weaveflow/capability/conversation"
	plancap "github.com/dengzii/weaveflow/capability/plan"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

func validPlanThinkingMode(value llms.ThinkingMode) bool {
	if value == "" {
		return true
	}
	switch value {
	case llms.ThinkingModeAuto, llms.ThinkingModeNone, llms.ThinkingModeMinimal, llms.ThinkingModeLow,
		llms.ThinkingModeMedium, llms.ThinkingModeHigh, llms.ThinkingModeXHigh, llms.ThinkingModeMax:
		return true
	default:
		return false
	}
}

const (
	NodeTypePlanGenerator = "plan_generator"
	NodeTypePlanStep      = "plan_step"
	NodeTypePlanReview    = "plan_review"
	NodeTypePlanSynthesis = "plan_synthesis"

	ConditionTypePlanStatusEquals        = "plan_status_equals"
	ConditionTypePlanIterationsRemaining = "plan_iterations_remaining"
)

const (
	PlanStatusPlanning   = "planning"
	PlanStatusExecuting  = "executing"
	PlanStatusReplan     = "replan"
	PlanStatusFinalizing = "finalizing"
	PlanStatusDone       = "done"
	PlanStatusFailed     = "failed"

	PlanStepStatusPending = "pending"
	PlanStepStatusRunning = "running"
	PlanStepStatusDone    = "done"
	PlanStepStatusFailed  = "failed"

	VerificationStatusPassed  = "passed"
	VerificationStatusRetry   = "retry_step"
	VerificationStatusReplan  = "replan"
	VerificationStatusFailed  = "failed"
	VerificationStatusPending = "pending"
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
						"id":                    map[string]any{"type": "string"},
						"title":                 map[string]any{"type": "string"},
						"description":           map[string]any{"type": "string"},
						"tool_ids":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"deliverables":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
						"acceptance_criteria":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
						"verification_strategy": map[string]any{"type": "string"},
					},
					"required":             []string{"id", "title", "description", "tool_ids", "deliverables", "acceptance_criteria", "verification_strategy"},
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

func IterationsRemaining(conversationPath state.Path) (registry.EdgeCondition, state.Contract) {
	iterationPath := conversationPath.MustChild(conversationcap.FieldIterationCount)
	maxIterationsPath := conversationPath.MustChild(conversationcap.FieldMaxIterations)
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{
		Type:  ConditionTypePlanIterationsRemaining,
		State: map[string]dsl.StateBinding{"conversation": {Path: conversationPath.String()}},
	}, func(_ context.Context, current *state.State) (registry.RouteDecision, error) {
		conversation, err := conversationcap.Bind(state.NewAccess(current), conversationPath)
		if err != nil {
			return registry.RouteDecision{}, err
		}
		matched := conversation.IterationCount() < conversation.MaxIterations()
		return registry.RouteDecision{Matched: matched, Reason: "plan step iteration limit checked"}, nil
	})
	contract := state.NewContract(
		state.FieldAccess{Path: iterationPath, Mode: state.AccessRead, Required: true},
		state.FieldAccess{Path: maxIterationsPath, Mode: state.AccessRead, Required: true},
	)
	return condition, contract
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
		if len(step.Deliverables) == 0 {
			step.Deliverables = []string{step.Title}
		}
		if len(step.AcceptanceCriteria) == 0 {
			step.AcceptanceCriteria = []string{"The step's deliverable is produced and supported by tool evidence."}
		}
		if strings.TrimSpace(step.VerificationStrategy) == "" {
			step.VerificationStrategy = "evidence"
		}
		step.VerificationStatus = VerificationStatusPending
		step.VerificationSummary = ""
		step.VerificationAttempts = 0
		step.Evidence = nil
		step.AttemptHistory = nil
		step.Result = ""
		step.Error = ""
		normalized = append(normalized, step)
	}
	return normalized
}

func enforcePlanInvariants(objective string, verifierID string, steps []plancap.Step, knownTools map[string]struct{}) []plancap.Step {
	if len(steps) == 0 {
		return steps
	}
	final := &steps[len(steps)-1]
	objective = strings.TrimSpace(objective)
	if objective != "" {
		appendUniquePlanText(&final.Deliverables, "Objective outcome: "+textLimit(objective, 500))
		appendUniquePlanText(&final.AcceptanceCriteria, "The completed result directly satisfies the objective and every material factual claim is supported by observable evidence.")
	}
	if verifierID != "" {
		appendUniquePlanText(&final.AcceptanceCriteria, fmt.Sprintf("The configured verifier %q passes with successful evidence.", verifierID))
	}
	if objectiveRequiresMutation(objective) {
		prefix := "Complete the requested mutation before verification."
		if !strings.Contains(strings.ToLower(final.Description), "mutation") {
			final.Description = strings.TrimSpace(prefix + " " + final.Description)
		}
		for _, toolID := range []string{"edit", "write"} {
			if _, available := knownTools[toolID]; available {
				appendUniquePlanText(&final.ToolIDs, toolID)
			}
		}
	}
	return steps
}

func canonicalPlanSummary(steps []plancap.Step) string {
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		parts = append(parts, fmt.Sprintf("[%s] %s", step.ID, step.Title))
	}
	return fmt.Sprintf("%d-step execution plan: %s", len(steps), strings.Join(parts, "; "))
}

func appendUniquePlanText(values *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *values {
		if strings.EqualFold(strings.TrimSpace(existing), value) {
			return
		}
	}
	*values = append(*values, value)
}

func objectiveRequiresMutation(objective string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	for _, negative := range []string{"do not modify", "do not edit", "do not write", "without modifying", "without editing", "read-only"} {
		objective = strings.ReplaceAll(objective, negative, "")
	}
	for _, word := range strings.Fields(objective) {
		word = strings.Trim(word, ".,:;!?()[]{}\"'")
		switch word {
		case "implement", "modify", "edit", "create", "write", "fix", "refactor", "update":
			return true
		}
	}
	return false
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
