package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

const (
	CapabilityID = "weaveflow.supervisor.v1"

	FieldObjective  = "objective"
	FieldRoute      = "next_worker"
	FieldTask       = "task"
	FieldReason     = "reason"
	FieldStatus     = "status"
	FieldTurnCount  = "turn_count"
	FieldMaxTurns   = "max_turns"
	FieldHistory    = "history"
	FieldLastResult = "last_result"
)

var fieldSchemas = map[string]dsl.JSONSchema{
	FieldObjective:  {"type": "string"},
	FieldRoute:      {"type": "string"},
	FieldTask:       {"type": "string"},
	FieldReason:     {"type": "string"},
	FieldStatus:     {"type": "string"},
	FieldTurnCount:  {"type": "integer"},
	FieldMaxTurns:   {"type": "integer"},
	FieldHistory:    {"type": "array", "items": dsl.JSONSchema{"type": "object"}},
	FieldLastResult: {"type": "string"},
}

type View struct {
	access *state.Access
	root   state.Path
}

type Turn struct {
	Turn     int    `json:"turn"`
	WorkerID string `json:"worker_id"`
	Task     string `json:"task"`
	Result   string `json:"result"`
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
		return nil, errors.New("supervisor root path is required")
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
			return fmt.Errorf("unknown supervisor field %q", name)
		}
	}
	return nil
}

func (v *View) History() []Turn {
	value, _ := v.Field(FieldHistory)
	return DecodeHistory(value)
}

func (v *View) SetHistory(history []Turn) error {
	return v.SetField(FieldHistory, EncodeHistory(history))
}

func DecodeHistory(value any) []Turn {
	if typed, ok := value.([]Turn); ok {
		return append([]Turn(nil), typed...)
	}
	var items []any
	switch typed := value.(type) {
	case []any:
		items = typed
	case []map[string]any:
		items = make([]any, len(typed))
		for index := range typed {
			items[index] = typed[index]
		}
	}
	history := make([]Turn, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		history = append(history, Turn{
			Turn:     intValue(mapped["turn"]),
			WorkerID: stringValue(mapped["worker_id"]),
			Task:     stringValue(mapped["task"]),
			Result:   stringValue(mapped["result"]),
		})
	}
	return history
}

func EncodeHistory(history []Turn) []map[string]any {
	items := make([]map[string]any, 0, len(history))
	for _, turn := range history {
		items = append(items, map[string]any{
			"turn": turn.Turn, "worker_id": turn.WorkerID, "task": turn.Task, "result": turn.Result,
		})
	}
	return items
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func (v *View) fieldPath(name string) (state.Path, error) {
	if v == nil {
		return state.Path{}, errors.New("supervisor view is nil")
	}
	if _, ok := fieldSchemas[name]; !ok {
		return state.Path{}, fmt.Errorf("unknown supervisor field %q", name)
	}
	return v.root.Child(name)
}

func fieldOrder() []string {
	return []string{FieldObjective, FieldRoute, FieldTask, FieldReason, FieldStatus, FieldTurnCount, FieldMaxTurns, FieldHistory, FieldLastResult}
}
