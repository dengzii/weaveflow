package builder

import (
	"fmt"
	"strings"

	"weaveflow/dsl"
	"weaveflow/registry"
	"weaveflow/state"
)

type RuntimeEdgeGraph interface {
	AddRuntimeEdge(from, to string) error
	AddRuntimeConditionalEdge(from, to string, condition registry.EdgeCondition) error
}

func ApplyBuiltInNodeEdges(target RuntimeEdgeGraph, def dsl.GraphDefinition) error {
	if target == nil {
		return fmt.Errorf("graph is nil")
	}
	for _, nodeSpec := range def.Nodes {
		if nodeSpec.Type != "iterator" {
			continue
		}
		continueTo := registry.StringConfig(nodeSpec.Config, "continue_to")
		doneTo := registry.StringConfig(nodeSpec.Config, "done_to")
		if continueTo == "" && doneTo == "" {
			continue
		}
		if continueTo == "" || doneTo == "" {
			return fmt.Errorf("build iterator nodes %q: continue_to and done_to must be configured together", nodeSpec.ID)
		}
		if hasExplicitOutgoingEdge(def.Edges, nodeSpec.ID) {
			return fmt.Errorf("build iterator nodes %q: built-in iterator edges cannot be combined with explicit outgoing edges", nodeSpec.ID)
		}

		if err := target.AddRuntimeEdge(nodeSpec.ID, doneTo); err != nil {
			return fmt.Errorf("build iterator nodes %q built-in done edge: %w", nodeSpec.ID, err)
		}
	}
	return nil
}

func ResolveNodeContracts(def dsl.GraphDefinition, reg *registry.Registry) (map[string]state.Contract, error) {
	if reg == nil {
		return nil, nil
	}
	contracts := make(map[string]state.Contract, len(def.Nodes))
	for _, spec := range def.Nodes {
		nodeDef, ok := reg.NodeTypes[spec.Type]
		if !ok {
			return nil, fmt.Errorf("node type %q is not registered", spec.Type)
		}
		if nodeDef.ResolveStateContract == nil && nodeDef.StateContract == nil {
			return nil, fmt.Errorf("node type %q must declare a state contract", spec.Type)
		}
		contract, err := reg.ResolveNodeStateContract(spec)
		if err != nil {
			return nil, err
		}
		converted, err := ConvertStateContract(contract)
		if err != nil {
			return nil, fmt.Errorf("node %q contract: %w", spec.ID, err)
		}
		if len(converted.Fields) > 0 || converted.WildcardRead || converted.WildcardWrite {
			contracts[spec.ID] = converted
		}
	}
	if len(contracts) == 0 {
		return nil, nil
	}
	return contracts, nil
}

func ConvertStateContract(contract dsl.StateContract) (state.Contract, error) {
	result := state.Contract{}
	for _, field := range contract.Fields {
		path := strings.TrimSpace(field.Path)
		if path == "*" {
			switch field.Mode {
			case dsl.StateAccessRead:
				result.WildcardRead = true
			case dsl.StateAccessWrite:
				result.WildcardWrite = true
			case dsl.StateAccessReadWrite:
				result.WildcardRead = true
				result.WildcardWrite = true
			}
			continue
		}
		if path == "" {
			continue
		}
		parsed, err := state.ParsePath(path)
		if err != nil {
			return state.Contract{}, fmt.Errorf("invalid state path %q: %w", path, err)
		}
		result.Fields = append(result.Fields, state.FieldAccess{
			Path:        parsed,
			Mode:        field.Mode,
			Required:    field.Required,
			Merge:       field.MergeStrategy,
			Type:        schemaType(field.Schema),
			Description: field.Description,
		})
	}
	return result, nil
}

func hasExplicitOutgoingEdge(edges []dsl.GraphEdgeSpec, from string) bool {
	for _, edge := range edges {
		if strings.TrimSpace(edge.From) == from {
			return true
		}
	}
	return false
}

func schemaType(schema dsl.JSONSchema) string {
	if len(schema) == 0 {
		return ""
	}
	if text, ok := schema["type"].(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}
