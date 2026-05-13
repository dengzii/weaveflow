package weaveflow

import (
	"context"
	"fmt"
	"weaveflow/builder"
	"weaveflow/core"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

func NewGraphRunner(graph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec wfstate.StateCodec, eventSink fruntime.EventSink) *fruntime.GraphRunner {
	runner := fruntime.NewGraphRunner(newRunnerGraph(graph), executionStore, checkpointStore, codec, eventSink)
	if graph != nil {
		runner.NodeContracts = cloneNodeContracts(graph.nodeContracts)
		runner.StartupWarnings = buildRunnerWarnings(graph.ContractDiagnostics())
	}
	return runner
}

func cloneNodeContracts(contracts map[string]core.NodeIOContract) map[string]core.NodeIOContract {
	if len(contracts) == 0 {
		return nil
	}
	cloned := make(map[string]core.NodeIOContract, len(contracts))
	for key, contract := range contracts {
		cloned[key] = contract.Clone()
	}
	return cloned
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

func (g *graphRunnerGraph) CompileForRunner(execution fruntime.RunnerExecution) (*langgraph.StateRunnable[wfstate.State], error) {
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

func (g *graphRunnerGraph) ResolveNextNode(currentNodeID string, state wfstate.State) (string, error) {
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

func (g *graphRunnerGraph) NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state wfstate.State, err error) {
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

func buildRunnerWarnings(diagnostics []ContractDiagnostic) []fruntime.WarningRecord {
	if len(diagnostics) == 0 {
		return nil
	}
	warnings := make([]fruntime.WarningRecord, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != ContractDiagnosticSeverityWarning {
			continue
		}
		warning := fruntime.WarningRecord{
			Code:        diagnostic.Kind,
			NodeID:      diagnostic.NodeID,
			OtherNodeID: diagnostic.OtherNodeID,
			Path:        diagnostic.Path,
			Message:     diagnostic.Message,
		}
		if len(diagnostic.Sources) > 0 {
			warning.Sources = append([]string(nil), diagnostic.Sources...)
		}
		warnings = append(warnings, warning)
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func convertStateContract(contract dsl.StateContract) core.NodeIOContract {
	return builder.ConvertStateContract(contract)
}
