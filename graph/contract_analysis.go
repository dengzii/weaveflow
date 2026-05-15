package graph

import (
	"weaveflow/builder"
	"weaveflow/core"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

type ContractDiagnosticSeverity = core.ContractDiagnosticSeverity

const (
	ContractDiagnosticSeverityError   = core.ContractDiagnosticSeverityError
	ContractDiagnosticSeverityWarning = core.ContractDiagnosticSeverityWarning
)

type ContractDiagnostic = core.ContractDiagnostic

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

func (g *Graph) contractAnalysisGraph() builder.ContractAnalysisGraph {
	if g == nil {
		return builder.ContractAnalysisGraph{}
	}

	conditionalEdges := make(map[string][]string, len(g.conditionalEdges))
	for from, edges := range g.conditionalEdges {
		targets := make([]string, 0, len(edges))
		for _, edge := range edges {
			targets = append(targets, edge.to)
		}
		conditionalEdges[from] = targets
	}

	return builder.ContractAnalysisGraph{
		EntryPoint:        g.entryPoint,
		EndNode:           langgraph.END,
		InitialStatePaths: append([]string(nil), g.initialStatePaths...),
		Edges:             g.edges,
		ConditionalEdges:  conditionalEdges,
		NodeContracts:     g.nodeContracts,
	}
}
