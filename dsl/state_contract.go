package dsl

import state "github.com/dengzii/weaveflow/state"

type StateAccessMode = state.AccessMode

const (
	StateAccessRead      StateAccessMode = state.AccessRead
	StateAccessWrite     StateAccessMode = state.AccessWrite
	StateAccessReadWrite StateAccessMode = state.AccessReadWrite
)

type StateMergeStrategy = state.MergeStrategy

const (
	StateMergeReplace StateMergeStrategy = state.MergeReplace
	StateMergeMerge   StateMergeStrategy = state.MergeMerge
	StateMergeAppend  StateMergeStrategy = state.MergeAppend
)

type StateFieldRef struct {
	Path          string             `json:"path,omitempty"`
	Mode          StateAccessMode    `json:"mode"`
	Required      bool               `json:"required,omitempty"`
	Description   string             `json:"description,omitempty"`
	Schema        JSONSchema         `json:"schema,omitempty"`
	MergeStrategy StateMergeStrategy `json:"merge_strategy,omitempty"`
}

type StateContract struct {
	Fields []StateFieldRef `json:"fields,omitempty"`
}

type RelativeStateFieldRef struct {
	Path     string          `json:"path"`
	Mode     StateAccessMode `json:"mode"`
	Required bool            `json:"required,omitempty"`
}

type RelativeStateContract struct {
	Fields []RelativeStateFieldRef `json:"fields"`
}

type StatePortDefinition struct {
	Name          string                `json:"name"`
	Description   string                `json:"description,omitempty"`
	Required      bool                  `json:"required,omitempty"`
	Schema        JSONSchema            `json:"schema,omitempty"`
	Mode          StateAccessMode       `json:"mode,omitempty"`
	Capability    string                `json:"capability,omitempty"`
	Contract      RelativeStateContract `json:"contract,omitempty"`
	MergeStrategy StateMergeStrategy    `json:"merge_strategy,omitempty"`
}

func (s JSONSchema) Clone() JSONSchema {
	return cloneJSONSchema(s)
}

func (c StateContract) Clone() StateContract {
	if len(c.Fields) == 0 {
		return StateContract{}
	}

	cloned := StateContract{
		Fields: make([]StateFieldRef, len(c.Fields)),
	}
	for i, field := range c.Fields {
		cloned.Fields[i] = field.Clone()
	}
	return cloned
}

func (f StateFieldRef) Clone() StateFieldRef {
	cloned := f
	if len(f.Schema) > 0 {
		cloned.Schema = cloneJSONSchema(f.Schema)
	}
	return cloned
}

func cloneJSONSchema(input JSONSchema) JSONSchema {
	if len(input) == 0 {
		return nil
	}
	cloned := make(JSONSchema, len(input))
	for key, value := range input {
		cloned[key] = cloneJSONSchemaValue(value)
	}
	return cloned
}

func cloneJSONSchemaValue(value any) any {
	switch typed := value.(type) {
	case JSONSchema:
		return cloneJSONSchema(typed)
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneJSONSchemaValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneJSONSchemaValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
