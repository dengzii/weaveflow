package builtin

import (
	"fmt"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
)

func RegisterCoreNodeTypes(r *registry.Registry) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if err := node.RegisterCoreNodeTypes(r); err != nil {
		return err
	}
	return registerCoreConditions(r)
}

func registerCoreConditions(r *registry.Registry) error {
	if err := r.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "last_message_has_tool_calls",
			Title:       "Last Message Has Tool Calls",
			Description: "Routes when the last AI message includes tool calls.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"state_scope": dsl.JSONSchema{"type": "string"}},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			return LastMessageHasToolCalls(conditionStateScope(spec.Config)), nil
		},
	}); err != nil {
		return fmt.Errorf("register condition %q: %w", "last_message_has_tool_calls", err)
	}

	if err := r.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "has_final_answer",
			Title:       "Has Final Answer",
			Description: "Routes when the current state already contains a final answer.",
			ConfigSchema: dsl.JSONSchema{
				"type":                 "object",
				"properties":           dsl.JSONSchema{"state_scope": dsl.JSONSchema{"type": "string"}},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			return HasFinalAnswer(conditionStateScope(spec.Config)), nil
		},
	}); err != nil {
		return fmt.Errorf("register condition %q: %w", "has_final_answer", err)
	}

	if err := r.RegisterCondition(registry.ConditionDefinition{
		ConditionSchema: dsl.ConditionSchema{
			Type:        "expression_conditions",
			Title:       "Expression Conditions",
			Description: "Routes by evaluating serializable expressions against the current state.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"state_scope": dsl.JSONSchema{"type": "string"},
					"match":       dsl.JSONSchema{"type": "string", "enum": []string{ExpressionMatchAll, ExpressionMatchAny}},
					"expressions": dsl.JSONSchema{
						"type": "array",
						"items": dsl.JSONSchema{
							"type": "object",
							"properties": dsl.JSONSchema{
								"value1": dsl.JSONSchema{"type": "string"},
								"op": dsl.JSONSchema{"type": "string", "enum": []string{
									OperationEqual,
									OperationNotEqual,
									OperationContains,
									OperationNotContain,
								}},
								"value2": dsl.JSONSchema{"type": "string"},
								"logic": dsl.JSONSchema{"type": "string", "enum": []string{
									LogicAnd,
									LogicOr,
									LogicNot,
								}},
								"children": dsl.JSONSchema{
									"type":  "array",
									"items": dsl.JSONSchema{"type": "object"},
								},
							},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"expressions"},
				"additionalProperties": false,
			},
		},
		Resolve: func(spec dsl.GraphConditionSpec) (registry.EdgeCondition, error) {
			cfg, err := ParseExpressionConditionConfig(spec.Config)
			if err != nil {
				return registry.EdgeCondition{}, fmt.Errorf("resolve expression condition: %w", err)
			}
			return ExpressionConditions(cfg)
		},
	}); err != nil {
		return fmt.Errorf("register condition %q: %w", "expression_conditions", err)
	}
	return nil
}

func conditionStateScope(configMap map[string]any) string {
	if _, ok := configMap["state_scope"]; ok {
		return config.String(configMap, "state_scope")
	}
	return node.DefaultScope
}
