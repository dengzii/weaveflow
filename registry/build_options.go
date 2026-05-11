package registry

import (
	"context"
	"weaveflow/core"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
)

type SubgraphRunner = func(context.Context, fruntime.State) (fruntime.State, error)

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
