package node

import (
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type ContractProvider = core.ContractProvider

func ContractFor(node Node) (state.Contract, error) {
	return core.ContractFor(node)
}
