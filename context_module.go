package weaveflow

import (
	"weaveflow/dsl"
	"weaveflow/nodes"
)

func RegisterContextModule(registry *Registry) {
	if registry == nil {
		return
	}

	registry.RegisterNodeType(contextAssemblerNodeTypeDefinition())
}

func contextAssemblerNodeTypeDefinition() NodeTypeDefinition {
	return NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        "context_assembler",
			Title:       "Context Assembler Node",
			Description: "Inject recalled memory into the scoped conversation before the next model turn.",
			ConfigSchema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"state_scope":              JSONSchema{"type": "string"},
					"memory_state_path":        JSONSchema{"type": "string"},
					"orchestration_state_path": JSONSchema{"type": "string"},
					"planner_state_path":       JSONSchema{"type": "string"},
					"include_memory":           JSONSchema{"type": "boolean"},
					"include_orchestration":    JSONSchema{"type": "boolean"},
					"include_planner":          JSONSchema{"type": "boolean"},
					"memory_heading":           JSONSchema{"type": "string"},
					"orchestration_heading":    JSONSchema{"type": "string"},
					"planner_heading":          JSONSchema{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		Build: func(ctx *BuildContext, spec dsl.GraphNodeSpec) (nodes.Node[State], error) {
			_ = ctx
			node := nodes.NewContextAssemblerNode()
			node.NodeID = spec.ID
			if spec.Name != "" {
				node.NodeName = spec.Name
			}
			if spec.Description != "" {
				node.NodeDescription = spec.Description
			}
			node.StateScope = stringConfig(spec.Config, "state_scope")
			node.MemoryStatePath = stringConfig(spec.Config, "memory_state_path")
			node.OrchestrationStatePath = stringConfig(spec.Config, "orchestration_state_path")
			node.PlannerStatePath = stringConfig(spec.Config, "planner_state_path")
			node.MemoryHeading = stringConfig(spec.Config, "memory_heading")
			node.OrchestrationHeading = stringConfig(spec.Config, "orchestration_heading")
			node.PlannerHeading = stringConfig(spec.Config, "planner_heading")
			if value, ok := boolConfig(spec.Config, "include_memory"); ok {
				node.IncludeMemory = value
			}
			if value, ok := boolConfig(spec.Config, "include_orchestration"); ok {
				node.IncludeOrchestration = value
			}
			if value, ok := boolConfig(spec.Config, "include_planner"); ok {
				node.IncludePlanner = value
			}
			return node, nil
		},
		ResolveStateContract: resolveContextAssemblerStateContract,
	}
}
