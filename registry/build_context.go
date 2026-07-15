package registry

import (
	"context"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type SubgraphRunner = func(context.Context, *state.State) (*state.State, error)

type SubgraphBuilder func(graphRef string) (SubgraphRunner, error)

type BuildContext struct {
	InstanceConfig       *dsl.GraphInstanceConfig
	GraphResolver        GraphResolver
	SubgraphBuilder      SubgraphBuilder
	OnContractDiagnostic func(core.ContractDiagnostic)
}

type ResolvedStateBinding struct {
	Path       state.Path
	Capability string
	Contract   state.Contract
}

type ResolvedNodeSpec struct {
	Spec  dsl.GraphNodeSpec
	State map[string]ResolvedStateBinding
}

type ResolvedConditionSpec struct {
	Spec  dsl.GraphConditionSpec
	State map[string]ResolvedStateBinding
}

type NodeBuilder func(*BuildContext, ResolvedNodeSpec) (core.Node, error)

func (ctx *BuildContext) Clone() *BuildContext {
	if ctx == nil {
		return &BuildContext{}
	}
	return &BuildContext{
		InstanceConfig:       ctx.InstanceConfig,
		GraphResolver:        ctx.GraphResolver,
		SubgraphBuilder:      ctx.SubgraphBuilder,
		OnContractDiagnostic: ctx.OnContractDiagnostic,
	}
}

func (ctx *BuildContext) EmitContractDiagnostics(diagnostics []core.ContractDiagnostic) {
	if ctx == nil || ctx.OnContractDiagnostic == nil || len(diagnostics) == 0 {
		return
	}
	for _, diagnostic := range diagnostics {
		cloned := diagnostic
		if len(diagnostic.Sources) > 0 {
			cloned.Sources = append([]string(nil), diagnostic.Sources...)
		}
		ctx.OnContractDiagnostic(cloned)
	}
}
