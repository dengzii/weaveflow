package graph

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

func NewGraphRunner(graph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec state.StateCodec, eventSink fruntime.EventSink) *fruntime.GraphRunner {
	runner := fruntime.NewGraphRunner(newRunnerGraph(graph), executionStore, checkpointStore, codec, eventSink)
	if graph != nil {
		runner.NodeContracts = cloneNodeContracts(graph.nodeContracts)
		if len(graph.nodeContracts) > 0 {
			runner.ContractValidation = core.ContractValidationStrict
		}
		runner.StartupWarnings = buildRunnerWarnings(graph.ContractDiagnostics())
		runner.GraphHash, _ = graph.SemanticHash()
		runner.GraphSnapshotHash, _ = graph.SnapshotHash()
	}
	return runner
}

func cloneNodeContracts(contracts map[string]state.Contract) map[string]state.Contract {
	if len(contracts) == 0 {
		return nil
	}
	cloned := make(map[string]state.Contract, len(contracts))
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

func (g *graphRunnerGraph) CompileForRunner(execution fruntime.RunnerExecution) (*langgraph.StateRunnable[*state.State], error) {
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

func (g *graphRunnerGraph) ResolveNextNode(ctx context.Context, currentNodeID string, state *state.State) (string, error) {
	next, err := g.ResolveNextNodes(ctx, currentNodeID, state)
	if err != nil {
		return "", err
	}
	if len(next) != 1 {
		return "", fmt.Errorf("nodes %q resolved %d next nodes; use ResolveNextNodes for fan-out", currentNodeID, len(next))
	}
	return next[0], nil
}

func (g *graphRunnerGraph) ResolveNextNodes(ctx context.Context, currentNodeID string, state *state.State) ([]string, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNextNodes(ctx, currentNodeID, state)
}

func (g *graphRunnerGraph) IsParallelBranchTarget(nodeID string) bool {
	if g == nil || g.graph == nil {
		return false
	}
	return g.graph.isParallelBranchTarget(nodeID)
}

func (g *graphRunnerGraph) NodeName(nodeID string) string {
	if g == nil || g.graph == nil {
		return nodeID
	}
	return g.graph.nodeDisplayName(nodeID)
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
		if g.graph.isParallelBranchTarget(nodeID) {
			return nil, fmt.Errorf("after_node breakpoint for parallel branch node %q is not supported; use before_node or resume from after_parallel_wave", nodeID)
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
