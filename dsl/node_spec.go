package dsl

// GraphNodeSpec describes a static nodes inside GraphDefinition.
// Runtime-bound values such as model paths, secret references, and per-instance
// overrides should live in GraphInstanceConfig instead of Config.
type GraphNodeSpec struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
}
