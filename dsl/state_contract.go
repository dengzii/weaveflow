package dsl

type StateAccessMode string

const (
	StateAccessRead      StateAccessMode = "read"
	StateAccessWrite     StateAccessMode = "write"
	StateAccessReadWrite StateAccessMode = "read_write"
)

type StateMergeStrategy string

const (
	StateMergeReplace StateMergeStrategy = "replace"
	StateMergeMerge   StateMergeStrategy = "merge"
	StateMergeAppend  StateMergeStrategy = "append"
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
	DefaultPath   string                `json:"default_path,omitempty"`
	Required      bool                  `json:"required,omitempty"`
	Schema        JSONSchema            `json:"schema,omitempty"`
	Mode          StateAccessMode       `json:"mode,omitempty"`
	Capability    string                `json:"capability,omitempty"`
	Contract      RelativeStateContract `json:"contract,omitempty"`
	MergeStrategy StateMergeStrategy    `json:"merge_strategy,omitempty"`
}

type DynamicStatePortDefinition struct {
	Description   string             `json:"description,omitempty"`
	NamePattern   string             `json:"name_pattern"`
	MinPorts      int                `json:"min_ports,omitempty"`
	MaxPorts      int                `json:"max_ports,omitempty"`
	Schema        JSONSchema         `json:"schema"`
	Mode          StateAccessMode    `json:"mode"`
	MergeStrategy StateMergeStrategy `json:"merge_strategy"`
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
