package falcon

import (
	fruntime "falcon/runtime"
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type BuildContext struct {
	Model llms.Model
	Tools map[string]Tool
}

type NodeTypeDefinition struct {
	Type         string                                                 `json:"type"`
	Title        string                                                 `json:"title,omitempty"`
	Description  string                                                 `json:"description,omitempty"`
	ConfigSchema JSONSchema                                             `json:"config_schema"`
	Build        func(BuildContext, GraphNodeSpec) (Node[State], error) `json:"-"`
}

type ConditionDefinition struct {
	Type         string                                          `json:"type"`
	Title        string                                          `json:"title,omitempty"`
	Description  string                                          `json:"description,omitempty"`
	ConfigSchema JSONSchema                                      `json:"config_schema"`
	Resolve      func(GraphConditionSpec) (EdgeCondition, error) `json:"-"`
}

type Registry struct {
	StateFields map[string]StateFieldDefinition `json:"state_fields"`
	NodeTypes   map[string]NodeTypeDefinition   `json:"node_types"`
	Conditions  map[string]ConditionDefinition  `json:"conditions"`
}

func NewRegistry() *Registry {
	return &Registry{
		StateFields: map[string]StateFieldDefinition{},
		NodeTypes:   map[string]NodeTypeDefinition{},
		Conditions:  map[string]ConditionDefinition{},
	}
}

func DefaultRegistry() *Registry {
	r := NewRegistry()

	for _, field := range defaultStateFieldDefinitions() {
		r.RegisterStateField(field)
	}

	r.RegisterNodeType(NodeTypeDefinition{
		Type:        "llm",
		Title:       "LLM Node",
		Description: "Built-in model inference node.",
		ConfigSchema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"tool_ids": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "string"},
				},
				"state_scope": JSONSchema{"type": "string"},
			},
			"additionalProperties": false,
		},
		Build: func(ctx BuildContext, spec GraphNodeSpec) (Node[State], error) {
			if ctx.Model == nil {
				return nil, fmt.Errorf("build llm node %q: model is required", spec.ID)
			}
			tools, err := resolveTools(ctx.Tools, stringSliceConfig(spec.Config, "tool_ids"))
			if err != nil {
				return nil, fmt.Errorf("build llm node %q: %w", spec.ID, err)
			}
			node := NewLLMNode(ctx.Model, tools)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			return node, nil
		},
	})

	r.RegisterNodeType(NodeTypeDefinition{
		Type:        "tools",
		Title:       "Tools Node",
		Description: "Built-in tool execution node.",
		ConfigSchema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"tool_ids": JSONSchema{
					"type":  "array",
					"items": JSONSchema{"type": "string"},
				},
				"state_scope": JSONSchema{"type": "string"},
			},
			"additionalProperties": false,
		},
		Build: func(ctx BuildContext, spec GraphNodeSpec) (Node[State], error) {
			toolIDs := stringSliceConfig(spec.Config, "tool_ids")
			tools, err := resolveTools(ctx.Tools, toolIDs)
			if err != nil {
				return nil, fmt.Errorf("build tools node %q: %w", spec.ID, err)
			}
			node := NewToolCallNode(tools)
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			return node, nil
		},
	})

	r.RegisterCondition(ConditionDefinition{
		Type:        "last_message_has_tool_calls",
		Title:       "Last Message Has Tool Calls",
		Description: "Routes when the last AI message includes tool calls.",
		ConfigSchema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"state_scope": JSONSchema{"type": "string"},
			},
			"additionalProperties": false,
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			return LastMessageHasToolCalls(stringConfig(spec.Config, "state_scope")), nil
		},
	})

	r.RegisterCondition(ConditionDefinition{
		Type:        "has_final_answer",
		Title:       "Has Final Answer",
		Description: "Routes when the current state already contains a final answer.",
		ConfigSchema: JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"state_scope": JSONSchema{"type": "string"},
			},
			"additionalProperties": false,
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			return HasFinalAnswer(stringConfig(spec.Config, "state_scope")), nil
		},
	})

	r.RegisterCondition(ConditionDefinition{
		Type:        "express_conditions",
		Title:       "Express Conditions",
		Description: "Reserved placeholder condition for future expression-based routing.",
		ConfigSchema: JSONSchema{
			"type":                 "object",
			"additionalProperties": false,
		},
		Resolve: func(spec GraphConditionSpec) (EdgeCondition, error) {
			_ = spec
			return ExpressConditions(), nil
		},
	})
	return r
}

func (r *Registry) RegisterStateField(def StateFieldDefinition) {
	r.StateFields[def.Name] = def
}

func (r *Registry) RegisterNodeType(def NodeTypeDefinition) {
	r.NodeTypes[def.Type] = def
}

func (r *Registry) RegisterCondition(def ConditionDefinition) {
	r.Conditions[def.Type] = def
}

func (r *Registry) ResolveCondition(spec GraphConditionSpec) (EdgeCondition, error) {
	if r == nil {
		return EdgeCondition{}, fmt.Errorf("registry is nil")
	}
	spec = normalizeGraphConditionSpec(spec)
	if spec.Type == "" {
		return EdgeCondition{}, fmt.Errorf("condition type is required")
	}
	conditionDef, ok := r.Conditions[spec.Type]
	if !ok {
		return EdgeCondition{}, fmt.Errorf("condition %q is not registered", spec.Type)
	}
	condition, err := conditionDef.Resolve(spec)
	if err != nil {
		return EdgeCondition{}, err
	}
	return condition.withSpec(spec), nil
}

func (r *Registry) AddConditionalEdge(g *Graph, from, to string, spec GraphConditionSpec) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	condition, err := r.ResolveCondition(spec)
	if err != nil {
		return err
	}
	return g.AddConditionalEdge(from, to, condition)
}

func (r *Registry) BuildGraph(def GraphDefinition, ctx BuildContext) (*Graph, error) {
	def = normalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return nil, err
	}
	if def.StateSchema != "" && def.StateSchema != fruntime.CommonStateSchemaID {
		return nil, fmt.Errorf("unsupported state schema %q", def.StateSchema)
	}

	g := NewGraph()
	for _, nodeSpec := range def.Nodes {
		nodeDef, ok := r.NodeTypes[nodeSpec.Type]
		if !ok {
			return nil, fmt.Errorf("node type %q is not registered", nodeSpec.Type)
		}
		node, err := nodeDef.Build(ctx, nodeSpec)
		if err != nil {
			return nil, err
		}
		if err := g.AddNode(node); err != nil {
			return nil, err
		}
	}
	for _, edge := range def.Edges {
		if edge.Condition == nil {
			if err := g.AddEdge(edge.From, edge.To); err != nil {
				return nil, err
			}
			continue
		}
		condition, err := r.ResolveCondition(*edge.Condition)
		if err != nil {
			return nil, err
		}
		if err := g.AddConditionalEdge(edge.From, edge.To, condition); err != nil {
			return nil, err
		}
	}

	if def.EntryPoint != "" {
		if err := g.SetEntryPoint(def.EntryPoint); err != nil {
			return nil, err
		}
	}
	if def.FinishPoint != "" {
		if err := g.SetFinishPoint(def.FinishPoint); err != nil {
			return nil, err
		}
	}

	if err := g.Validate(); err != nil {
		return nil, err
	}

	return g, nil
}

func (r *Registry) JSONSchema() JSONSchema {
	nodeVariants := make([]any, 0, len(r.NodeTypes))
	for _, key := range sortedNodeTypeKeys(r.NodeTypes) {
		nodeDef := r.NodeTypes[key]
		nodeVariants = append(nodeVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"id":          JSONSchema{"type": "string"},
				"name":        JSONSchema{"type": "string"},
				"type":        JSONSchema{"const": nodeDef.Type},
				"description": JSONSchema{"type": "string"},
				"config":      nodeDef.ConfigSchema,
			},
			"required":             []string{"id", "type"},
			"additionalProperties": false,
		})
	}

	conditionVariants := make([]any, 0, len(r.Conditions))
	for _, key := range sortedConditionKeys(r.Conditions) {
		conditionDef := r.Conditions[key]
		conditionVariants = append(conditionVariants, JSONSchema{
			"type": "object",
			"properties": JSONSchema{
				"type":   JSONSchema{"const": conditionDef.Type},
				"config": conditionDef.ConfigSchema,
			},
			"required":             []string{"type"},
			"additionalProperties": false,
		})
	}

	stateFields := JSONSchema{}
	for _, key := range sortedStateFieldKeys(r.StateFields) {
		field := r.StateFields[key]
		stateFields[field.Name] = field.Schema
	}

	return JSONSchema{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": JSONSchema{
			"version":      JSONSchema{"type": "string"},
			"name":         JSONSchema{"type": "string"},
			"description":  JSONSchema{"type": "string"},
			"state_schema": JSONSchema{"const": fruntime.CommonStateSchemaID},
			"entry_point":  JSONSchema{"type": "string"},
			"finish_point": JSONSchema{"type": "string"},
			"nodes": JSONSchema{
				"type":  "array",
				"items": JSONSchema{"oneOf": nodeVariants},
			},
			"edges": JSONSchema{
				"type": "array",
				"items": JSONSchema{
					"type": "object",
					"properties": JSONSchema{
						"from":      JSONSchema{"type": "string"},
						"to":        JSONSchema{"type": "string"},
						"condition": JSONSchema{"oneOf": conditionVariants},
					},
					"required":             []string{"from", "to"},
					"additionalProperties": false,
				},
			},
			"metadata": JSONSchema{"type": "object"},
		},
		"required": []string{"nodes"},
		"$defs": JSONSchema{
			"common_state": JSONSchema{
				"type":                 "object",
				"properties":           stateFields,
				"additionalProperties": true,
			},
		},
	}
}

func filterTools(all map[string]Tool, ids []string) map[string]Tool {
	if len(ids) == 0 {
		return cloneTools(all)
	}
	filtered := make(map[string]Tool, len(ids))
	for _, id := range ids {
		if tool, ok := all[id]; ok {
			filtered[id] = tool
		}
	}
	return filtered
}

func resolveTools(all map[string]Tool, ids []string) (map[string]Tool, error) {
	if len(ids) == 0 {
		return cloneTools(all), nil
	}

	filtered := make(map[string]Tool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("tool id is empty")
		}
		tool, ok := all[id]
		if !ok {
			return nil, fmt.Errorf("tool_id %q is not registered", id)
		}
		filtered[id] = tool
	}
	return filtered, nil
}

func stringSliceConfig(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	values, ok := raw.([]any)
	if ok {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}
	return nil
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func sortedStateFieldKeys(input map[string]StateFieldDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeTypeKeys(input map[string]NodeTypeDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedConditionKeys(input map[string]ConditionDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
