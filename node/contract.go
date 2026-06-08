package node

import (
	"fmt"

	"weaveflow/state"
)

type ContractProvider interface {
	Contract(*state.Registry) (state.Contract, error)
}

func ContractFor(registry *state.Registry, node Node) (state.Contract, error) {
	if registry == nil {
		return state.Contract{}, fmt.Errorf("state registry is required")
	}
	if node == nil {
		return state.Contract{}, fmt.Errorf("node node is nil")
	}
	if provider, ok := node.(ContractProvider); ok {
		return provider.Contract(registry)
	}
	return contractFromAccessorUses(registry, node.ID(), node.Scope(), node.AccessorUses())
}

func contractFromAccessorUses(registry *state.Registry, nodeID string, nodeScope string, uses []AccessorUse) (state.Contract, error) {
	fields := make([]state.FieldAccess, 0)
	wildcardRead := false
	wildcardWrite := false
	for _, use := range uses {
		if use.Name == "" {
			return state.Contract{}, fmt.Errorf("node %q declares an empty accessor use", nodeID)
		}
		contract, ok := registry.AccessorContract(use.Name, use.EffectiveScope(nodeScope))
		if !ok {
			return state.Contract{}, fmt.Errorf("node %q requires unregistered state accessor %q", nodeID, use.Name)
		}
		fields = append(fields, contract.Fields...)
		wildcardRead = wildcardRead || contract.WildcardRead
		wildcardWrite = wildcardWrite || contract.WildcardWrite
	}

	contract := state.NewContract(fields...)
	contract.WildcardRead = wildcardRead
	contract.WildcardWrite = wildcardWrite
	return contract, nil
}
