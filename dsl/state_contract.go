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
	Dynamic       bool               `json:"dynamic,omitempty"`
	PathConfigKey string             `json:"path_config_key,omitempty"`
	MergeStrategy StateMergeStrategy `json:"merge_strategy,omitempty"`
}

type StateContract struct {
	Fields []StateFieldRef `json:"fields,omitempty"`
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
	default:
		return value
	}
}
