// Package plan provides state-bound planning capabilities.
package plan

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

const (
	CapabilityID = "weaveflow.plan.v1"

	FieldObjective    = "objective"
	FieldStatus       = "status"
	FieldSummary      = "summary"
	FieldSteps        = "steps"
	FieldHistory      = "history"
	FieldCurrentIndex = "current_index"
	FieldReplanCount  = "replan_count"
	FieldMaxReplans   = "max_replans"
	FieldReplanReason = "replan_reason"
	FieldFinalAnswer  = "final_answer"
)

var fieldSchemas = map[string]dsl.JSONSchema{
	FieldObjective:    {"type": "string"},
	FieldStatus:       {"type": "string"},
	FieldSummary:      {"type": "string"},
	FieldSteps:        {"type": "array", "items": dsl.JSONSchema{"type": "object"}},
	FieldHistory:      {"type": "array", "items": dsl.JSONSchema{"type": "object"}},
	FieldCurrentIndex: {"type": "integer"},
	FieldReplanCount:  {"type": "integer"},
	FieldMaxReplans:   {"type": "integer"},
	FieldReplanReason: {"type": "string"},
	FieldFinalAnswer:  {"type": "string"},
}

type View struct {
	access *state.Access
	root   state.Path
}

type Step struct {
	ID                   string     `json:"id"`
	Title                string     `json:"title"`
	Description          string     `json:"description"`
	ToolIDs              []string   `json:"tool_ids,omitempty"`
	Deliverables         []string   `json:"deliverables,omitempty"`
	AcceptanceCriteria   []string   `json:"acceptance_criteria,omitempty"`
	VerificationStrategy string     `json:"verification_strategy,omitempty"`
	VerificationStatus   string     `json:"verification_status,omitempty"`
	VerificationSummary  string     `json:"verification_summary,omitempty"`
	VerificationAttempts int        `json:"verification_attempts,omitempty"`
	Evidence             []Evidence `json:"evidence,omitempty"`
	AttemptHistory       []Attempt  `json:"attempt_history,omitempty"`
	StartedAt            string     `json:"started_at,omitempty"`
	DurationMillis       int64      `json:"duration_ms,omitempty"`
	ModelCalls           int        `json:"model_calls,omitempty"`
	ToolCalls            int        `json:"tool_calls,omitempty"`
	Status               string     `json:"status,omitempty"`
	Result               string     `json:"result,omitempty"`
	Error                string     `json:"error,omitempty"`
}

type Evidence struct {
	ToolID     string `json:"tool_id"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
	Error      string `json:"error,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type Attempt struct {
	Number             int        `json:"number"`
	Result             string     `json:"result,omitempty"`
	VerificationStatus string     `json:"verification_status,omitempty"`
	Summary            string     `json:"summary,omitempty"`
	Evidence           []Evidence `json:"evidence,omitempty"`
}

func Definition() dsl.StateCapabilityDefinition {
	fields := make([]dsl.StateCapabilityFieldDefinition, 0, len(fieldSchemas))
	for _, name := range fieldOrder() {
		fields = append(fields, dsl.StateCapabilityFieldDefinition{Name: name, Schema: fieldSchemas[name].Clone(), MergeStrategy: dsl.StateMergeReplace})
	}
	return dsl.StateCapabilityDefinition{ID: CapabilityID, Schema: dsl.JSONSchema{"type": "object"}, Fields: fields}
}

func Bind(access *state.Access, root state.Path) (*View, error) {
	if access == nil {
		return nil, errors.New("state access is nil")
	}
	if root.Empty() {
		return nil, errors.New("plan root path is required")
	}
	return &View{access: access, root: root}, nil
}

func (v *View) Path() state.Path {
	if v == nil {
		return state.Path{}
	}
	return v.root
}

func (v *View) Value() map[string]any {
	if v == nil {
		return map[string]any{}
	}
	value, ok := v.access.ReadAny(v.root)
	if !ok {
		return map[string]any{}
	}
	mapped, _ := value.(map[string]any)
	if mapped == nil {
		return map[string]any{}
	}
	return mapped
}

func (v *View) Field(name string) (any, bool) {
	path, err := v.fieldPath(name)
	if err != nil {
		return nil, false
	}
	return v.access.ReadAny(path)
}

func (v *View) SetField(name string, value any) error {
	path, err := v.fieldPath(name)
	if err != nil {
		return err
	}
	return v.access.SetAny(path, value)
}

func (v *View) DeleteField(name string) error {
	path, err := v.fieldPath(name)
	if err != nil {
		return err
	}
	return v.access.Delete(path)
}

func (v *View) Merge(values map[string]any) error {
	for _, name := range fieldOrder() {
		value, ok := values[name]
		if !ok {
			continue
		}
		if err := v.SetField(name, value); err != nil {
			return err
		}
	}
	for name := range values {
		if _, ok := fieldSchemas[name]; !ok {
			return fmt.Errorf("unknown plan field %q", name)
		}
	}
	return nil
}

func (v *View) Steps() []Step {
	value, _ := v.Field(FieldSteps)
	return DecodeSteps(value)
}

func (v *View) SetSteps(steps []Step) error {
	return v.SetField(FieldSteps, EncodeSteps(steps))
}

func (v *View) History() []map[string]any {
	value, _ := v.Field(FieldHistory)
	return DecodeObjectList(value)
}

func (v *View) SetHistory(history []map[string]any) error {
	return v.SetField(FieldHistory, DecodeObjectList(history))
}

func DecodeSteps(value any) []Step {
	var raw []map[string]any
	switch typed := value.(type) {
	case []Step:
		return append([]Step(nil), typed...)
	case []map[string]any:
		raw = typed
	case []any:
		raw = make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				raw = append(raw, mapped)
			}
		}
	}
	if len(raw) == 0 {
		return nil
	}
	steps := make([]Step, 0, len(raw))
	for _, item := range raw {
		step := Step{
			ID:                   stringValue(item["id"]),
			Title:                stringValue(item["title"]),
			Description:          stringValue(item["description"]),
			ToolIDs:              stringSliceValue(item["tool_ids"]),
			Deliverables:         stringSliceValue(item["deliverables"]),
			AcceptanceCriteria:   stringSliceValue(item["acceptance_criteria"]),
			VerificationStrategy: stringValue(item["verification_strategy"]),
			VerificationStatus:   stringValue(item["verification_status"]),
			VerificationSummary:  stringValue(item["verification_summary"]),
			VerificationAttempts: intValue(item["verification_attempts"]),
			Evidence:             decodeEvidence(item["evidence"]),
			AttemptHistory:       decodeAttempts(item["attempt_history"]),
			StartedAt:            stringValue(item["started_at"]),
			DurationMillis:       int64Value(item["duration_ms"]),
			ModelCalls:           intValue(item["model_calls"]),
			ToolCalls:            intValue(item["tool_calls"]),
			Status:               stringValue(item["status"]),
			Result:               stringValue(item["result"]),
			Error:                stringValue(item["error"]),
		}
		if len(step.Deliverables) == 0 && step.Title != "" {
			step.Deliverables = []string{step.Title}
		}
		if len(step.AcceptanceCriteria) == 0 {
			step.AcceptanceCriteria = []string{"The step result must be supported by verification evidence."}
		}
		if step.VerificationStrategy == "" {
			step.VerificationStrategy = "evidence"
		}
		if step.VerificationStatus == "" {
			step.VerificationStatus = "pending"
		}
		steps = append(steps, step)
	}
	return steps
}

func EncodeSteps(steps []Step) []map[string]any {
	if len(steps) == 0 {
		return []map[string]any{}
	}
	items := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		item := map[string]any{
			"id":                    step.ID,
			"title":                 step.Title,
			"description":           step.Description,
			"tool_ids":              append([]string(nil), step.ToolIDs...),
			"deliverables":          append([]string(nil), step.Deliverables...),
			"acceptance_criteria":   append([]string(nil), step.AcceptanceCriteria...),
			"verification_strategy": step.VerificationStrategy,
			"verification_status":   step.VerificationStatus,
			"verification_summary":  step.VerificationSummary,
			"verification_attempts": step.VerificationAttempts,
			"evidence":              encodeEvidence(step.Evidence),
			"attempt_history":       encodeAttempts(step.AttemptHistory),
			"started_at":            step.StartedAt,
			"duration_ms":           step.DurationMillis,
			"model_calls":           step.ModelCalls,
			"tool_calls":            step.ToolCalls,
			"status":                step.Status,
			"result":                step.Result,
		}
		if step.Error != "" {
			item["error"] = step.Error
		}
		items = append(items, item)
	}
	return items
}

func decodeEvidence(value any) []Evidence {
	var raw []map[string]any
	switch typed := value.(type) {
	case []Evidence:
		return append([]Evidence(nil), typed...)
	case []map[string]any:
		raw = typed
	case []any:
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				raw = append(raw, mapped)
			}
		}
	}
	if len(raw) == 0 {
		return nil
	}
	result := make([]Evidence, 0, len(raw))
	for _, item := range raw {
		result = append(result, Evidence{
			ToolID:     stringValue(item["tool_id"]),
			Status:     stringValue(item["status"]),
			Summary:    stringValue(item["summary"]),
			Error:      stringValue(item["error"]),
			ToolCallID: stringValue(item["tool_call_id"]),
		})
	}
	return result
}

func encodeEvidence(evidence []Evidence) []map[string]any {
	if len(evidence) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(evidence))
	for _, item := range evidence {
		mapped := map[string]any{"tool_id": item.ToolID, "status": item.Status, "summary": item.Summary}
		if item.Error != "" {
			mapped["error"] = item.Error
		}
		if item.ToolCallID != "" {
			mapped["tool_call_id"] = item.ToolCallID
		}
		result = append(result, mapped)
	}
	return result
}

func decodeAttempts(value any) []Attempt {
	var raw []map[string]any
	switch typed := value.(type) {
	case []Attempt:
		return append([]Attempt(nil), typed...)
	case []map[string]any:
		raw = typed
	case []any:
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				raw = append(raw, mapped)
			}
		}
	}
	result := make([]Attempt, 0, len(raw))
	for _, item := range raw {
		result = append(result, Attempt{
			Number:             intValue(item["number"]),
			Result:             stringValue(item["result"]),
			VerificationStatus: stringValue(item["verification_status"]),
			Summary:            stringValue(item["summary"]),
			Evidence:           decodeEvidence(item["evidence"]),
		})
	}
	return result
}

func encodeAttempts(attempts []Attempt) []map[string]any {
	if len(attempts) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(attempts))
	for _, item := range attempts {
		result = append(result, map[string]any{
			"number": item.Number, "result": item.Result, "verification_status": item.VerificationStatus,
			"summary": item.Summary, "evidence": encodeEvidence(item.Evidence),
		})
	}
	return result
}

func DecodeObjectList(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		items := make([]map[string]any, len(typed))
		for index, item := range typed {
			items[index] = cloneMap(item)
		}
		return items
	case []any:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				items = append(items, cloneMap(mapped))
			}
		}
		return items
	default:
		return nil
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}

func (v *View) fieldPath(name string) (state.Path, error) {
	if v == nil {
		return state.Path{}, errors.New("plan view is nil")
	}
	if _, ok := fieldSchemas[name]; !ok {
		return state.Path{}, fmt.Errorf("unknown plan field %q", name)
	}
	return v.root.Child(name)
}

func fieldOrder() []string {
	return []string{FieldObjective, FieldStatus, FieldSummary, FieldSteps, FieldHistory, FieldCurrentIndex, FieldReplanCount, FieldMaxReplans, FieldReplanReason, FieldFinalAnswer}
}
