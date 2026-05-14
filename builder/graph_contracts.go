package builder

import (
	"fmt"
	"strings"

	"weaveflow/builtin"
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
	wfstate "weaveflow/state"
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

		condition, err := builtin.ExpressionConditions(builtin.ExpressionConditionConfig{
			Expressions: []builtin.Expression{{
				Value1: nodes.IteratorStateRootKey + "." + nodeSpec.ID + ".done",
				Op:     builtin.OperationEqual,
				Value2: "false",
			}},
		})
		if err != nil {
			return fmt.Errorf("build iterator nodes %q built-in continue edge: %w", nodeSpec.ID, err)
		}
		if err := target.AddRuntimeConditionalEdge(nodeSpec.ID, continueTo, condition); err != nil {
			return fmt.Errorf("build iterator nodes %q built-in continue edge: %w", nodeSpec.ID, err)
		}
		if err := target.AddRuntimeEdge(nodeSpec.ID, doneTo); err != nil {
			return fmt.Errorf("build iterator nodes %q built-in done edge: %w", nodeSpec.ID, err)
		}
	}
	return nil
}

func ResolveNodeContracts(def dsl.GraphDefinition, reg *registry.Registry) (map[string]core.NodeIOContract, error) {
	if reg == nil {
		return nil, nil
	}
	contracts := make(map[string]core.NodeIOContract, len(def.Nodes))
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
		converted := ConvertStateContract(contract)
		if !converted.IsEmpty() {
			contracts[spec.ID] = converted
		}
	}
	if len(contracts) == 0 {
		return nil, nil
	}
	return contracts, nil
}

func ConvertStateContract(contract dsl.StateContract) core.NodeIOContract {
	result := core.NodeIOContract{}
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
		normalized := wfstate.NormalizeContractPath(path)
		switch field.Mode {
		case dsl.StateAccessRead:
			result.ReadPaths = append(result.ReadPaths, normalized)
			if field.Required {
				result.RequiredReadPaths = append(result.RequiredReadPaths, normalized)
			}
		case dsl.StateAccessWrite, dsl.StateAccessReadWrite:
			result.WritePaths = append(result.WritePaths, normalized)
			if field.Mode == dsl.StateAccessReadWrite {
				result.ReadPaths = append(result.ReadPaths, normalized)
			}
			if field.Required {
				result.RequiredWritePaths = append(result.RequiredWritePaths, normalized)
				if field.Mode == dsl.StateAccessReadWrite {
					result.RequiredReadPaths = append(result.RequiredReadPaths, normalized)
				}
			}
			if strategy := convertMergeStrategy(field.MergeStrategy); strategy != core.StateMergeDefault {
				if result.MergeStrategies == nil {
					result.MergeStrategies = map[string]core.StateMergeStrategy{}
				}
				result.MergeStrategies[normalized] = strategy
			}
		}
	}
	return result
}

func convertMergeStrategy(strategy dsl.StateMergeStrategy) core.StateMergeStrategy {
	switch strategy {
	case dsl.StateMergeReplace:
		return core.StateMergeReplace
	case dsl.StateMergeMerge:
		return core.StateMergeMerge
	case dsl.StateMergeAppend:
		return core.StateMergeAppend
	default:
		return core.StateMergeDefault
	}
}

func hasExplicitOutgoingEdge(edges []dsl.GraphEdgeSpec, from string) bool {
	for _, edge := range edges {
		if strings.TrimSpace(edge.From) == from {
			return true
		}
	}
	return false
}
