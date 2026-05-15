package neo

import (
	"net/http"
	"sort"
	"strings"

	"weaveflow/builtin"
	"weaveflow/dsl"
	wfregistry "weaveflow/registry"

	"github.com/gin-gonic/gin"
)

type RegistryController struct {
	registry *wfregistry.Registry
}

func NewRegistryController(registry *wfregistry.Registry) *RegistryController {
	if registry == nil {
		registry = builtin.NewDefaultRegistry()
	}
	return &RegistryController{registry: registry}
}

type RegistryResponse struct {
	StateFields []RegistryStateFieldInfo `json:"state_fields"`
	NodeTypes   []RegistryNodeTypeInfo   `json:"node_types"`
	Conditions  []dsl.ConditionSchema    `json:"conditions"`
	GraphSchema dsl.JSONSchema           `json:"graph_schema"`
}

type RegistryStateFieldInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      dsl.JSONSchema `json:"schema"`
}

type RegistryNodeTypeInfo struct {
	Schema                dsl.NodeTypeSchema `json:"schema"`
	ExampleConfig         map[string]any     `json:"example_config,omitempty"`
	ResolvedStateContract *dsl.StateContract `json:"resolved_state_contract,omitempty"`
	ResolveError          string             `json:"resolve_error,omitempty"`
}

func (ctrl *RegistryController) Get(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "ok",
		"data": ctrl.buildResponse(),
	})
}

func (ctrl *RegistryController) buildResponse() RegistryResponse {
	registry := ctrl.registry
	if registry == nil {
		registry = builtin.NewDefaultRegistry()
	}

	stateFields := make([]RegistryStateFieldInfo, 0, len(registry.StateFields))
	for _, key := range sortedStateFieldKeys(registry.StateFields) {
		field := registry.StateFields[key]
		stateFields = append(stateFields, RegistryStateFieldInfo{
			Name:        field.Name,
			Description: field.Description,
			Schema:      field.Schema,
		})
	}

	nodeTypes := make([]RegistryNodeTypeInfo, 0, len(registry.NodeTypes))
	for _, key := range sortedNodeTypeKeys(registry.NodeTypes) {
		nodeDef := registry.NodeTypes[key]
		info := RegistryNodeTypeInfo{
			Schema:        nodeDef.NodeTypeSchema,
			ExampleConfig: buildExampleConfig(nodeDef.NodeTypeSchema.ConfigSchema),
		}
		spec := dsl.GraphNodeSpec{
			ID:     "example",
			Type:   nodeDef.Type,
			Config: cloneJSONMap(info.ExampleConfig),
		}
		if contract, err := registry.ResolveNodeStateContract(spec); err != nil {
			info.ResolveError = err.Error()
		} else if len(contract.Fields) > 0 {
			info.ResolvedStateContract = &contract
		}
		nodeTypes = append(nodeTypes, info)
	}

	conditions := make([]dsl.ConditionSchema, 0, len(registry.Conditions))
	for _, key := range sortedConditionKeys(registry.Conditions) {
		conditions = append(conditions, registry.Conditions[key].ConditionSchema)
	}

	return RegistryResponse{
		StateFields: stateFields,
		NodeTypes:   nodeTypes,
		Conditions:  conditions,
		GraphSchema: registry.JSONSchema(),
	}
}

func sortedStateFieldKeys(input map[string]dsl.StateFieldDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeTypeKeys(input map[string]wfregistry.NodeTypeDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedConditionKeys(input map[string]wfregistry.ConditionDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func buildExampleConfig(schema dsl.JSONSchema) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return nil
	}

	config := map[string]any{}
	for key := range properties {
		if value, ok := wellKnownExampleValue(key); ok {
			config[key] = value
		}
	}

	for _, required := range requiredSchemaKeys(schema["required"]) {
		if _, ok := config[required]; ok {
			continue
		}
		propertySchema, _ := properties[required].(map[string]any)
		if value, ok := exampleValueForSchema(propertySchema); ok {
			config[required] = value
		}
	}

	if len(config) == 0 {
		return nil
	}
	return config
}

func requiredSchemaKeys(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func wellKnownExampleValue(key string) (any, bool) {
	switch key {
	case "state_scope":
		return "agent", true
	case "graph_ref":
		return "example_graph", true
	case "state_key":
		return "payload.items", true
	case "max_iterations":
		return 2, true
	case "continue_to":
		return "body", true
	case "done_to":
		return "__end__", true
	case "input_path", "request_input_path":
		return "request.input", true
	case "intent_state_path":
		return "intent", true
	case "orchestration_state_path":
		return "orchestration", true
	case "memory_state_path":
		return "memory", true
	case "planner_state_path":
		return "planner", true
	case "objective_path":
		return "planner.objective", true
	case "query_path":
		return "memory.query", true
	case "final_answer_path":
		return "scopes.agent.final_answer", true
	case "context_paths":
		return []string{"request.metadata"}, true
	case "tool_ids":
		return []string{"web_fetch"}, true
	case "limit":
		return 5, true
	case "interrupt_message":
		return "Approval required", true
	default:
		return nil, false
	}
}

func exampleValueForSchema(schema map[string]any) (any, bool) {
	if len(schema) == 0 {
		return nil, false
	}
	if enumValues, ok := schema["enum"].([]any); ok && len(enumValues) > 0 {
		return enumValues[0], true
	}
	if constValue, ok := schema["const"]; ok {
		return constValue, true
	}

	typeName, _ := schema["type"].(string)
	switch typeName {
	case "string":
		return "example", true
	case "integer", "number":
		return 1, true
	case "boolean":
		return true, true
	case "array":
		items, _ := schema["items"].(map[string]any)
		if value, ok := exampleValueForSchema(items); ok {
			return []any{value}, true
		}
		return []any{}, true
	case "object":
		return map[string]any{}, true
	default:
		return nil, false
	}
}

func cloneJSONMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
