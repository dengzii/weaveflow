package node

import (
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type ContractProvider = core.ContractProvider

func ContractFor(registry *state.Registry, node Node) (state.Contract, error) {
	return core.ContractFor(registry, node)
}

func contractFromAccessorUses(registry *state.Registry, nodeID string, nodeScope string, uses []AccessorUse) (state.Contract, error) {
	return core.ContractFromAccessorUses(registry, nodeID, nodeScope, uses)
}
