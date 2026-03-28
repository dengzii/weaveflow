package falcon

import (
	"context"
	"fmt"

	fruntime "falcon/runtime"
	langgraph "github.com/smallnest/langgraphgo/graph"
)

type graphRunnerGraph struct {
	graph *Graph
}

func newRunnerGraph(graph *Graph) fruntime.RunnerGraph {
	if graph == nil {
		return nil
	}
	return &graphRunnerGraph{graph: graph}
}

func (g *graphRunnerGraph) Validate() error {
	if g == nil || g.graph == nil {
		return fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.Validate()
}

func (g *graphRunnerGraph) EntryPointName() string {
	if g == nil || g.graph == nil {
		return ""
	}
	return g.graph.entryPoint
}

func (g *graphRunnerGraph) CompileForRunner(execution fruntime.RunnerExecution) (*langgraph.StateRunnable[State], error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.compileForRunner(execution)
}

func (g *graphRunnerGraph) ResolveNodeRef(ref string) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNodeRef(ref)
}

func (g *graphRunnerGraph) ResolveNextNode(currentName string, state State) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	if conditional := g.graph.conditionalEdges[currentName]; len(conditional) > 0 {
		for _, edge := range conditional {
			if edge.when(context.Background(), state) {
				return edge.to, nil
			}
		}
		if target, ok := g.graph.edges[currentName]; ok {
			return target, nil
		}
		if currentName == g.graph.finishPoint {
			return langgraph.END, nil
		}
		return "", fmt.Errorf("node %q produced no matching conditional edge", currentName)
	}
	if target, ok := g.graph.edges[currentName]; ok {
		return target, nil
	}
	if currentName == g.graph.finishPoint {
		return langgraph.END, nil
	}
	return "", fmt.Errorf("node %q has no outgoing edge", currentName)
}

func (g *graphRunnerGraph) NodeID(nodeName string) string {
	if g == nil || g.graph == nil {
		return nodeName
	}
	if spec, ok := g.graph.nodeSpecs[nodeName]; ok && spec.ID != "" {
		return spec.ID
	}
	return nodeName
}

func (g *graphRunnerGraph) NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeName string, state State, err error) {
	if g == nil || g.graph == nil {
		return
	}
	for _, listener := range g.graph.globalListeners {
		listener.OnNodeEvent(ctx, event, nodeName, state, err)
	}
	for _, listener := range g.graph.nodeListeners[nodeName] {
		listener.OnNodeEvent(ctx, event, nodeName, state, err)
	}
}

func (g *graphRunnerGraph) AfterInterruptNodes(breakpoints []fruntime.Breakpoint) ([]string, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	nodes := make([]string, 0, len(breakpoints))
	for _, breakpoint := range breakpoints {
		if !breakpoint.Enabled || breakpoint.Stage != string(CheckpointAfterNode) {
			continue
		}
		name, err := g.graph.resolveNodeRef(breakpoint.NodeID)
		if err != nil {
			return nil, fmt.Errorf("resolve after-node breakpoint %q: %w", breakpoint.NodeID, err)
		}
		nodes = append(nodes, name)
	}
	return nodes, nil
}
