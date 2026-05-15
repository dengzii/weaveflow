package builder

import (
	"fmt"
	"strings"

	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

type BuildContext struct {
	InstanceConfig       *dsl.GraphInstanceConfig
	GraphResolver        registry.GraphResolver
	SubgraphBuilder      registry.SubgraphBuilder
	OnContractDiagnostic func(core.ContractDiagnostic)
	internal             *buildContextState
}

type NodeBuilder func(*BuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error)

type buildContextState struct {
	graphBuildPath []string
}

func (ctx *BuildContext) Clone() *BuildContext {
	if ctx == nil {
		return &BuildContext{}
	}
	cloned := &BuildContext{
		InstanceConfig:       ctx.InstanceConfig,
		GraphResolver:        ctx.GraphResolver,
		SubgraphBuilder:      ctx.SubgraphBuilder,
		OnContractDiagnostic: ctx.OnContractDiagnostic,
	}
	if ctx.internal != nil {
		cloned.internal = &buildContextState{}
		if len(ctx.internal.graphBuildPath) > 0 {
			cloned.internal.graphBuildPath = append([]string(nil), ctx.internal.graphBuildPath...)
		}
	}
	return cloned
}

func (ctx *BuildContext) BuildOptions() registry.BuildOptions {
	return ctx.RegistryBuildContext().BuildOptions()
}

func (ctx *BuildContext) RegistryBuildContext() registry.BuildContext {
	if ctx == nil {
		return registry.BuildContext{}
	}
	return registry.BuildContext{
		InstanceConfig:       ctx.InstanceConfig,
		GraphResolver:        ctx.GraphResolver,
		SubgraphBuilder:      ctx.SubgraphBuilder,
		OnContractDiagnostic: ctx.OnContractDiagnostic,
	}
}

func FromNodeBuildContext(ctx registry.NodeBuildContext) *BuildContext {
	if ctx == nil {
		return nil
	}
	if concrete, ok := ctx.(*BuildContext); ok {
		return concrete
	}
	options := ctx.BuildOptions()
	return &BuildContext{
		InstanceConfig:       options.InstanceConfig,
		GraphResolver:        options.GraphResolver,
		SubgraphBuilder:      options.SubgraphBuilder,
		OnContractDiagnostic: options.OnContractDiagnostic,
	}
}

func AdaptNodeBuilder(build NodeBuilder) func(registry.NodeBuildContext, dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) {
	return func(ctx registry.NodeBuildContext, spec dsl.GraphNodeSpec) (core.Node[wfstate.State, wfstate.StatePatch], error) {
		if build == nil {
			return nil, fmt.Errorf("node builder is nil")
		}
		return build(FromNodeBuildContext(ctx), spec)
	}
}

func (ctx *BuildContext) GraphBuildPath() []string {
	if ctx == nil || ctx.internal == nil || len(ctx.internal.graphBuildPath) == 0 {
		return nil
	}
	return append([]string(nil), ctx.internal.graphBuildPath...)
}

func (ctx *BuildContext) PushGraphRef(graphRef string) {
	if ctx == nil {
		return
	}
	if ctx.internal == nil {
		ctx.internal = &buildContextState{}
	}
	ctx.internal.graphBuildPath = append(ctx.internal.graphBuildPath, graphRef)
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

func ValidateGraphBuildPath(path []string, next string) error {
	next = strings.TrimSpace(next)
	if next == "" {
		return fmt.Errorf("graph_ref is required")
	}
	for _, existing := range path {
		if existing == next {
			cycle := append(append([]string(nil), path...), next)
			return fmt.Errorf("cyclic graph_ref dependency detected: %s", strings.Join(cycle, " -> "))
		}
	}
	return nil
}
