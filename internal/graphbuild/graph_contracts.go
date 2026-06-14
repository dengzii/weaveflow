package graphbuild

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

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

func schemaType(schema dsl.JSONSchema) string {
	if len(schema) == 0 {
		return ""
	}
	if text, ok := schema["type"].(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}
