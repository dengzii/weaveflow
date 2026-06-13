package registry

import (
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
)

type BuildContext struct {
	InstanceConfig       *dsl.GraphInstanceConfig
	GraphResolver        GraphResolver
	SubgraphBuilder      SubgraphBuilder
	OnContractDiagnostic func(core.ContractDiagnostic)
}

func (ctx BuildContext) BuildOptions() BuildOptions {
	return BuildOptions{
		InstanceConfig:       ctx.InstanceConfig,
		GraphResolver:        ctx.GraphResolver,
		SubgraphBuilder:      ctx.SubgraphBuilder,
		OnContractDiagnostic: ctx.OnContractDiagnostic,
	}
}
