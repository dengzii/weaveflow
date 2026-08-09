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
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	ToolIDs     []string `json:"tool_ids,omitempty"`
	Status      string   `json:"status,omitempty"`
	Result      string   `json:"result,omitempty"`
	Error       string   `json:"error,omitempty"`
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
		steps = append(steps, Step{
			ID:          stringValue(item["id"]),
			Title:       stringValue(item["title"]),
			Description: stringValue(item["description"]),
			ToolIDs:     stringSliceValue(item["tool_ids"]),
			Status:      stringValue(item["status"]),
			Result:      stringValue(item["result"]),
			Error:       stringValue(item["error"]),
		})
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
			"id":          step.ID,
			"title":       step.Title,
			"description": step.Description,
			"tool_ids":    append([]string(nil), step.ToolIDs...),
			"status":      step.Status,
			"result":      step.Result,
		}
		if step.Error != "" {
			item["error"] = step.Error
		}
		items = append(items, item)
	}
	return items
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
