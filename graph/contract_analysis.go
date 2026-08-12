package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/state"
)

func (g *Graph) ContractDiagnostics() []core.ContractDiagnostic {
	if g == nil {
		return nil
	}
	g.contractDiagnosticsMu.RLock()
	defer g.contractDiagnosticsMu.RUnlock()
	if len(g.contractDiagnostics) == 0 {
		return nil
	}
	cloned := make([]core.ContractDiagnostic, len(g.contractDiagnostics))
	for i, diagnostic := range g.contractDiagnostics {
		cloned[i] = diagnostic
		if len(diagnostic.Sources) > 0 {
			cloned[i].Sources = append([]string(nil), diagnostic.Sources...)
		}
	}
	return cloned
}

func (g *Graph) InitialStateRequirements() core.InitialStateRequirements {
	return g.InitialStateRequirementsFor(nil)
}

func (g *Graph) InitialStateRequirementsFor(provider *core.EntryStateProvider) core.InitialStateRequirements {
	if g == nil {
		return graphbuild.AnalyzeInitialStateRequirements(graphbuild.ContractAnalysisGraph{})
	}
	analysis := g.contractAnalysisGraph()
	analysis.EntryProvider = provider
	return graphbuild.AnalyzeInitialStateRequirements(analysis)
}

// ValidateInitialState verifies that concrete invocation state satisfies every
// required read that is not produced by the Graph itself.
func (g *Graph) ValidateInitialState(initial *state.State) error {
	requirements := g.InitialStateRequirements()
	paths := make([]string, 0, len(requirements.Required)+len(requirements.Unresolved))
	seen := make(map[string]struct{}, cap(paths))
	for _, requirement := range append(requirements.Required, requirements.Unresolved...) {
		pathText := strings.TrimSpace(requirement.Path)
		if pathText == "" {
			continue
		}
		if _, exists := seen[pathText]; exists {
			continue
		}
		seen[pathText] = struct{}{}
		if _, exists := state.ReadPath(initial, pathText); !exists {
			paths = append(paths, pathText)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fmt.Errorf("graph initial state is missing required paths: %s", strings.Join(paths, ", "))
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
		EntryPoint:         g.entryPoint,
		EndNode:            endNodeID,
		InitialStatePaths:  append([]string(nil), g.initialStatePaths...),
		Edges:              defaultEdges,
		ConditionalEdges:   conditionalEdges,
		NodeContracts:      g.nodeContracts,
		ConditionContracts: g.conditionContracts,
	}
}
