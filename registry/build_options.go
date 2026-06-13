package registry

import (
	"context"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type SubgraphRunner = func(context.Context, *state.State) (*state.State, error)

type SubgraphBuilder func(graphRef string) (SubgraphRunner, error)

type BuildOptions struct {
	InstanceConfig       *dsl.GraphInstanceConfig
	GraphResolver        GraphResolver
	SubgraphBuilder      SubgraphBuilder
	OnContractDiagnostic func(core.ContractDiagnostic)
}

type NodeBuildContext interface {
	BuildOptions() BuildOptions
}
