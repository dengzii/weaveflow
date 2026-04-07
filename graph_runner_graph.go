package weaveflow

import (
	"context"
	"fmt"

	fruntime "weaveflow/runtime"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

func NewGraphRunner(graph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec fruntime.StateCodec, eventSink fruntime.EventSink) *fruntime.GraphRunner {
	return fruntime.NewGraphRunner(newRunnerGraph(graph), executionStore, checkpointStore, codec, eventSink)
}

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

func (g *graphRunnerGraph) EntryPointID() string {
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

func (g *graphRunnerGraph) ResolveNodeID(nodeID string) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNodeID(nodeID)
}

func (g *graphRunnerGraph) ResolveNextNode(currentNodeID string, state State) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	if conditional := g.graph.conditionalEdges[currentNodeID]; len(conditional) > 0 {
		for _, edge := range conditional {
			if edge.condition.Match(context.Background(), state) {
				return edge.to, nil
			}
		}
		if target, ok := g.graph.edges[currentNodeID]; ok {
			return target, nil
		}
		if currentNodeID == g.graph.finishPoint {
			return langgraph.END, nil
		}
		return "", fmt.Errorf("nodes %q produced no matching conditional edge", currentNodeID)
	}
	if target, ok := g.graph.edges[currentNodeID]; ok {
		return target, nil
	}
	if currentNodeID == g.graph.finishPoint {
		return langgraph.END, nil
	}
	return "", fmt.Errorf("nodes %q has no outgoing edge", currentNodeID)
}

func (g *graphRunnerGraph) NodeName(nodeID string) string {
	if g == nil || g.graph == nil {
		return nodeID
	}
	return g.graph.nodeDisplayName(nodeID)
}

func (g *graphRunnerGraph) NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state State, err error) {
	if g == nil || g.graph == nil {
		return
	}
	displayName := g.graph.nodeDisplayName(nodeID)
	for _, listener := range g.graph.globalListeners {
		listener.OnNodeEvent(ctx, event, displayName, state, err)
	}
	for _, listener := range g.graph.nodeListeners[nodeID] {
		listener.OnNodeEvent(ctx, event, displayName, state, err)
	}
}

func (g *graphRunnerGraph) AfterInterruptNodes(breakpoints []fruntime.Breakpoint) ([]string, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	nodes := make([]string, 0, len(breakpoints))
	for _, breakpoint := range breakpoints {
		if !breakpoint.Enabled || breakpoint.Stage != string(fruntime.CheckpointAfterNode) {
			continue
		}
		nodeID, err := g.graph.resolveNodeID(breakpoint.NodeID)
		if err != nil {
			return nil, fmt.Errorf("resolve after-nodes breakpoint %q: %w", breakpoint.NodeID, err)
		}
		nodes = append(nodes, nodeID)
	}
	return nodes, nil
}
