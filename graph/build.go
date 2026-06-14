package graph

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/registry"
)

func BuildGraph(reg *registry.Registry, def dsl.GraphDefinition, ctx *registry.BuildContext) (*Graph, error) {
	return buildGraph(reg, def, nil, ctx, nil)
}

func BuildGraphInstance(reg *registry.Registry, def dsl.GraphDefinition, instance dsl.GraphInstanceConfig, ctx *registry.BuildContext) (*Graph, error) {
	return buildGraph(reg, def, &instance, ctx, nil)
}

func buildGraph(reg *registry.Registry, def dsl.GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *registry.BuildContext, buildPath []string) (*Graph, error) {
	if reg == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	var err error
	def, ctx, err = graphbuild.PrepareDefinition(def, instance, ctx)
	if err != nil {
		return nil, err
	}
	ctx.SubgraphBuilder = makeSubgraphBuilder(reg, ctx, buildPath)

	graph := NewGraph()
	graph.setInitialStatePaths(graphbuild.InitialContractPathsFromStateFields(reg.StateFields))

	contracts, err := graphbuild.ResolveNodeContracts(def, reg)
	if err != nil {
		return nil, err
	}
	graph.setNodeContracts(contracts)

	if err := graphbuild.PopulateGraph(graph, reg, def, ctx, func(def dsl.GraphDefinition) error {
		return applyBuiltInNodeEdges(graph, def)
	}); err != nil {
		return nil, err
	}
	if err := graph.Validate(); err != nil {
		ctx.EmitContractDiagnostics(graph.ContractDiagnostics())
		return nil, err
	}
	ctx.EmitContractDiagnostics(graph.ContractDiagnostics())
	return graph, nil
}

func makeSubgraphBuilder(reg *registry.Registry, parentCtx *registry.BuildContext, buildPath []string) registry.SubgraphBuilder {
	return func(graphRef string) (registry.SubgraphRunner, error) {
		graphRef = strings.TrimSpace(graphRef)
		if graphRef == "" {
			return nil, fmt.Errorf("graph_ref is required")
		}
		if parentCtx == nil || parentCtx.GraphResolver == nil {
			return nil, fmt.Errorf("graph resolver is required")
		}
		if err := graphbuild.ValidateGraphBuildPath(buildPath, graphRef); err != nil {
			return nil, err
		}
		def, err := parentCtx.GraphResolver(graphRef)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", graphRef, err)
		}
		subgraphCtx := parentCtx.Clone()
		subgraphCtx.InstanceConfig = nil
		nextPath := append(append([]string(nil), buildPath...), graphRef)
		graph, err := buildGraph(reg, def, nil, subgraphCtx, nextPath)
		if err != nil {
			return nil, fmt.Errorf("build graph %q: %w", graphRef, err)
		}
		return graph.Run, nil
	}
}

func applyBuiltInNodeEdges(target *Graph, def dsl.GraphDefinition) error {
	if target == nil {
		return fmt.Errorf("graph is nil")
	}
	for _, nodeSpec := range def.Nodes {
		if nodeSpec.Type != "iterator" {
			continue
		}
		continueTo := config.String(nodeSpec.Config, "continue_to")
		doneTo := config.String(nodeSpec.Config, "done_to")
		if continueTo == "" && doneTo == "" {
			continue
		}
		if continueTo == "" || doneTo == "" {
			return fmt.Errorf("build iterator nodes %q: continue_to and done_to must be configured together", nodeSpec.ID)
		}
		if hasExplicitOutgoingEdge(def.Edges, nodeSpec.ID) {
			return fmt.Errorf("build iterator nodes %q: built-in iterator edges cannot be combined with explicit outgoing edges", nodeSpec.ID)
		}
		if err := target.addRuntimeEdge(nodeSpec.ID, doneTo); err != nil {
			return fmt.Errorf("build iterator nodes %q built-in done edge: %w", nodeSpec.ID, err)
		}
	}
	return nil
}

func hasExplicitOutgoingEdge(edges []dsl.GraphEdgeSpec, from string) bool {
	for _, edge := range edges {
		if strings.TrimSpace(edge.From) == from {
			return true
		}
	}
	return false
}
