package graph

import (
	"fmt"
	"strings"

	"weaveflow/builder"
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/registry"
)

func BuildGraph(reg *registry.Registry, def dsl.GraphDefinition, ctx *builder.BuildContext) (*Graph, error) {
	return buildGraph(reg, def, nil, ctx)
}

func BuildGraphInstance(reg *registry.Registry, def dsl.GraphDefinition, instance dsl.GraphInstanceConfig, ctx *builder.BuildContext) (*Graph, error) {
	return buildGraph(reg, def, &instance, ctx)
}

func buildGraph(reg *registry.Registry, def dsl.GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *builder.BuildContext) (*Graph, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	var err error
	def, ctx, err = builder.PrepareDefinition(def, instance, ctx)
	if err != nil {
		return nil, err
	}
	ctx.SubgraphBuilder = makeSubgraphBuilder(reg, ctx)
	return builder.BuildFinalizedGraph(
		reg,
		def,
		ctx,
		NewGraph,
		builder.InitialContractPathsFromStateFields(reg.StateFields),
		func(g *Graph, def dsl.GraphDefinition) error {
			return builder.ApplyBuiltInNodeEdges(g, def)
		},
		func(def dsl.GraphDefinition, _ *registry.Registry) (map[string]core.NodeIOContract, error) {
			return builder.ResolveNodeContracts(def, reg)
		},
	)
}

func makeSubgraphBuilder(reg *registry.Registry, parentCtx *builder.BuildContext) registry.SubgraphBuilder {
	return func(graphRef string) (registry.SubgraphRunner, error) {
		graphRef = strings.TrimSpace(graphRef)
		if graphRef == "" {
			return nil, fmt.Errorf("graph_ref is required")
		}
		if parentCtx == nil || parentCtx.GraphResolver == nil {
			return nil, fmt.Errorf("graph resolver is required")
		}
		if err := builder.ValidateGraphBuildPath(parentCtx.GraphBuildPath(), graphRef); err != nil {
			return nil, err
		}
		def, err := parentCtx.GraphResolver(graphRef)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", graphRef, err)
		}
		subgraphCtx := parentCtx.Clone()
		subgraphCtx.InstanceConfig = nil
		subgraphCtx.PushGraphRef(graphRef)
		graph, err := buildGraph(reg, def, nil, subgraphCtx)
		if err != nil {
			return nil, fmt.Errorf("build graph %q: %w", graphRef, err)
		}
		return graph.Run, nil
	}
}
