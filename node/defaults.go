package node

import (
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/state/accessors"
)

const DefaultScope = accessors.KeyAgent

func NewDefaultRegistry() (*state.Registry, error) {
	registry := state.NewRegistry()
	if err := accessors.InstallDefaultAccessors(registry); err != nil {
		return nil, err
	}
	return registry, nil
}
