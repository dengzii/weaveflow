package graph

import (
	"fmt"
	"sort"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/internal/graphbuild"
)

func (g *Graph) Validate() error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	if err := g.executionPolicy.Validate(); err != nil {
		return err
	}
	for nodeID, policy := range g.nodePolicies {
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("node %q execution policy: %w", nodeID, err)
		}
	}
	if len(g.nodes) == 0 {
		return fmt.Errorf("graph has no nodes")
	}
	if g.entryPoint == "" {
		return fmt.Errorf("entry point is not set")
	}
	if _, ok := g.nodes[g.entryPoint]; !ok {
		return fmt.Errorf("entry point %q not found", g.entryPoint)
	}
	if g.finishPoint != "" {
		if _, ok := g.nodes[g.finishPoint]; !ok {
			return fmt.Errorf("finish point %q not found", g.finishPoint)
		}
	}

	for from, targets := range g.defaultEdges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("edge source %q not found", from)
		}
		seenTargets := map[string]struct{}{}
		for _, to := range targets {
			if _, exists := seenTargets[to]; exists {
				return fmt.Errorf("default edge %q -> %q is duplicated", from, g.serializeNodeRef(to))
			}
			seenTargets[to] = struct{}{}
			if to != endNodeID {
				if _, ok := g.nodes[to]; !ok {
					return fmt.Errorf("edge target %q not found", to)
				}
			}
		}
	}

	for from := range g.conditionalEdges {
		if len(g.defaultEdges[from]) > 1 {
			return fmt.Errorf("node %q cannot combine conditional edges with multiple default fallback edges", from)
		}
		if len(g.defaultEdges[from]) == 0 && from != g.finishPoint {
			return fmt.Errorf("node %q has conditional edges but no default fallback edge", from)
		}
	}
	for from, routes := range g.failureRoutes {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("failure route source %q not found", from)
		}
		for _, route := range routes {
			if err := route.route.Validate(); err != nil {
				return fmt.Errorf("failure route from %q: %w", from, err)
			}
			if route.to != endNodeID {
				if _, ok := g.nodes[route.to]; !ok {
					return fmt.Errorf("failure route target %q not found", route.to)
				}
			}
		}
	}

	for from, edges := range g.conditionalEdges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("conditional edge source %q not found", from)
		}
		for _, edge := range edges {
			if err := edge.condition.Validate(); err != nil {
				return fmt.Errorf("conditional edge from %q to %q: %w", from, edge.to, err)
			}
			if edge.to != endNodeID {
				if _, ok := g.nodes[edge.to]; !ok {
					return fmt.Errorf("conditional edge target %q not found", edge.to)
				}
			}
		}
	}
	if err := g.validateTopology(); err != nil {
		return err
	}

	var diagnostics []core.ContractDiagnostic
	if len(g.nodeContracts) > 0 || len(g.conditionContracts) > 0 {
		diagnostics = graphbuild.AnalyzeContractDiagnostics(g.contractAnalysisGraph())
		if err := graphbuild.ContractDiagnosticsError(diagnostics); err != nil {
			g.contractDiagnosticsMu.Lock()
			g.contractDiagnostics = diagnostics
			g.contractDiagnosticsMu.Unlock()
			return err
		}
	}
	g.contractDiagnosticsMu.Lock()
	g.contractDiagnostics = diagnostics
	g.contractDiagnosticsMu.Unlock()
	return nil
}

func (g *Graph) validateTopology() error {
	reachable := g.reachableNodes()
	for _, nodeID := range g.sortedNodeIDs() {
		if _, ok := reachable[nodeID]; !ok {
			return fmt.Errorf("node %q is unreachable from entry point %q", nodeID, g.entryPoint)
		}
	}
	if g.finishPoint != "" && (len(g.defaultEdges[g.finishPoint]) > 0 || len(g.conditionalEdges[g.finishPoint]) > 0) {
		return fmt.Errorf("finish point %q cannot have outgoing edges", g.finishPoint)
	}
	for _, nodeID := range g.sortedNodeIDs() {
		if _, ok := reachable[nodeID]; !ok || nodeID == g.finishPoint {
			continue
		}
		if len(g.defaultEdges[nodeID]) == 0 && len(g.conditionalEdges[nodeID]) == 0 {
			return fmt.Errorf("node %q has no outgoing edge", nodeID)
		}
	}
	terminalReachable := g.terminalReachableNodes()
	for _, nodeID := range g.sortedNodeIDs() {
		if _, ok := reachable[nodeID]; !ok {
			continue
		}
		if _, ok := terminalReachable[nodeID]; !ok {
			return fmt.Errorf("node %q cannot reach graph end", nodeID)
		}
	}
	return nil
}

func (g *Graph) sortedNodeIDs() []string {
	if g == nil || len(g.nodes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(g.nodes))
	for nodeID := range g.nodes {
		ids = append(ids, nodeID)
	}
	sort.Strings(ids)
	return ids
}

func (g *Graph) reachableNodes() map[string]struct{} {
	reachable := map[string]struct{}{}
	if g == nil || g.entryPoint == "" {
		return reachable
	}
	queue := []string{g.entryPoint}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if _, seen := reachable[nodeID]; seen {
			continue
		}
		reachable[nodeID] = struct{}{}
		targets := append([]string(nil), g.defaultEdges[nodeID]...)
		for _, edge := range g.conditionalEdges[nodeID] {
			targets = append(targets, edge.to)
		}
		for _, route := range g.failureRoutes[nodeID] {
			targets = append(targets, route.to)
		}
		for _, target := range targets {
			if target == endNodeID {
				continue
			}
			if _, exists := g.nodes[target]; !exists {
				continue
			}
			if _, seen := reachable[target]; !seen {
				queue = append(queue, target)
			}
		}
	}
	return reachable
}

func (g *Graph) terminalReachableNodes() map[string]struct{} {
	reachable := map[string]struct{}{}
	if g == nil {
		return reachable
	}
	reverseEdges := map[string][]string{}
	queue := []string{}
	addTerminal := func(nodeID string) {
		if nodeID == "" || nodeID == endNodeID {
			return
		}
		if _, exists := g.nodes[nodeID]; !exists {
			return
		}
		if _, seen := reachable[nodeID]; seen {
			return
		}
		reachable[nodeID] = struct{}{}
		queue = append(queue, nodeID)
	}
	addTerminal(g.finishPoint)
	for from, targets := range g.defaultEdges {
		for _, target := range targets {
			if target == endNodeID {
				addTerminal(from)
				continue
			}
			reverseEdges[target] = append(reverseEdges[target], from)
		}
	}
	for from, edges := range g.conditionalEdges {
		for _, edge := range edges {
			if edge.to == endNodeID {
				addTerminal(from)
				continue
			}
			reverseEdges[edge.to] = append(reverseEdges[edge.to], from)
		}
	}
	for from, routes := range g.failureRoutes {
		for _, route := range routes {
			if route.to == endNodeID {
				addTerminal(from)
				continue
			}
			reverseEdges[route.to] = append(reverseEdges[route.to], from)
		}
	}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, predecessor := range reverseEdges[nodeID] {
			if _, seen := reachable[predecessor]; seen {
				continue
			}
			reachable[predecessor] = struct{}{}
			queue = append(queue, predecessor)
		}
	}
	return reachable
}
