package graph

import (
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/internal/graphbuild"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

type ContractDiagnosticSeverity = core.ContractDiagnosticSeverity

const (
	ContractDiagnosticSeverityError   = core.ContractDiagnosticSeverityError
	ContractDiagnosticSeverityWarning = core.ContractDiagnosticSeverityWarning
)

type ContractDiagnostic = core.ContractDiagnostic

type InitialStateRequirements = core.InitialStateRequirements
type InitialStateRequirement = core.InitialStateRequirement

func (g *Graph) ContractDiagnostics() []ContractDiagnostic {
	if g == nil || len(g.contractDiagnostics) == 0 {
		return nil
	}
	cloned := make([]ContractDiagnostic, len(g.contractDiagnostics))
	for i, diagnostic := range g.contractDiagnostics {
		cloned[i] = diagnostic
		if len(diagnostic.Sources) > 0 {
			cloned[i].Sources = append([]string(nil), diagnostic.Sources...)
		}
	}
	return cloned
}

func (g *Graph) InitialStateRequirements() InitialStateRequirements {
	if g == nil {
		return graphbuild.AnalyzeInitialStateRequirements(graphbuild.ContractAnalysisGraph{})
	}
	return graphbuild.AnalyzeInitialStateRequirements(g.contractAnalysisGraph())
}

func (g *Graph) contractAnalysisGraph() graphbuild.ContractAnalysisGraph {
	if g == nil {
		return graphbuild.ContractAnalysisGraph{}
	}

	conditionalEdges := make(map[string][]string, len(g.conditionalEdges))
	for from, edges := range g.conditionalEdges {
		targets := make([]string, 0, len(edges))
		for _, edge := range edges {
			targets = append(targets, edge.to)
		}
		conditionalEdges[from] = targets
	}

	defaultEdges := make(map[string][]string, len(g.defaultEdges))
	for from, targets := range g.defaultEdges {
		defaultEdges[from] = append([]string(nil), targets...)
	}

	return graphbuild.ContractAnalysisGraph{
		EntryPoint:        g.entryPoint,
		EndNode:           langgraph.END,
		InitialStatePaths: append([]string(nil), g.initialStatePaths...),
		Edges:             defaultEdges,
		ConditionalEdges:  conditionalEdges,
		NodeContracts:     g.nodeContracts,
	}
}
