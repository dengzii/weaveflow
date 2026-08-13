package graph

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func NewGraphRunner(targetGraph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec state.Codec, eventSink fruntime.EventSink, options ...fruntime.GraphRunnerOption) (*fruntime.GraphRunner, error) {
	if targetGraph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	graphHash, err := targetGraph.SemanticHash()
	if err != nil {
		return nil, fmt.Errorf("compute graph semantic hash: %w", err)
	}
	snapshotHash, err := targetGraph.SnapshotHash()
	if err != nil {
		return nil, fmt.Errorf("compute graph snapshot hash: %w", err)
	}
	baseOptions := []fruntime.GraphRunnerOption{
		fruntime.WithNodeContracts(cloneNodeContracts(targetGraph.nodeContracts)),
		fruntime.WithStateSchemas(cloneStateSchemas(targetGraph.stateSchemas)),
		fruntime.WithStateReducers(targetGraph.reducers()),
		fruntime.WithStartupWarnings(buildRunnerWarnings(targetGraph.ContractDiagnostics())),
		fruntime.WithGraphMetadata("", "", graphHash, snapshotHash, ""),
	}
	if len(targetGraph.nodeContracts) > 0 {
		baseOptions = append(baseOptions, fruntime.WithContractValidation(core.ContractValidationStrict))
	}
	baseOptions = append(baseOptions, options...)
	return fruntime.NewGraphRunner(newRunnerGraph(targetGraph), executionStore, checkpointStore, codec, eventSink, baseOptions...)
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

type runtimeGraph struct {
	graph *Graph
}

func newRunnerGraph(targetGraph *Graph) fruntime.RunnerGraph {
	if targetGraph == nil {
		return nil
	}
	return &runtimeGraph{graph: targetGraph}
}

func (g *runtimeGraph) Validate() error {
	if g == nil || g.graph == nil {
		return fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.Validate()
}

func (g *runtimeGraph) EntryPointID() string {
	if g == nil || g.graph == nil {
		return ""
	}
	return g.graph.entryPoint
}

func (g *runtimeGraph) CompileForRunner(execution fruntime.RunnerExecution) (fruntime.RunnerRunnable, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.compileForRunner(execution)
}

func (g *runtimeGraph) ResolveNodeID(nodeID string) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNodeID(nodeID)
}

func (g *runtimeGraph) ResolveNextNode(ctx context.Context, currentNodeID string, currentState *state.State) (string, error) {
	next, err := g.ResolveNextNodes(ctx, currentNodeID, currentState)
	if err != nil {
		return "", err
	}
	if len(next) != 1 {
		return "", fmt.Errorf("node %q resolved %d next nodes; use ResolveNextNodes for fan-out", currentNodeID, len(next))
	}
	return next[0], nil
}

func (g *runtimeGraph) ResolveNextNodes(ctx context.Context, currentNodeID string, currentState *state.State) ([]string, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNextNodes(ctx, currentNodeID, currentState)
}

func (g *runtimeGraph) ResolveNextTasks(ctx context.Context, parent fruntime.GraphTask, currentState *state.State) ([]fruntime.GraphTask, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNextTasksObserved(ctx, parent, currentState, nil)
}

func (g *runtimeGraph) ResolveFailure(ctx context.Context, task fruntime.GraphTask, stage string, err error) ([]fruntime.GraphTask, error) {
	if g == nil || g.graph == nil {
		return nil, fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveFailure(ctx, task, stage, err)
}

func (g *runtimeGraph) IsParallelBranchTarget(nodeID string) bool {
	if g == nil || g.graph == nil {
		return false
	}
	return g.graph.isParallelBranchTarget(nodeID)
}

func (g *runtimeGraph) NodeName(nodeID string) string {
	if g == nil || g.graph == nil {
		return nodeID
	}
	return g.graph.nodeDisplayName(nodeID)
}

func (g *runtimeGraph) AfterInterruptNodes(breakpoints []fruntime.Breakpoint) ([]string, error) {
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
			return nil, fmt.Errorf("resolve after-node breakpoint %q: %w", breakpoint.NodeID, err)
		}
		if g.graph.isParallelBranchTarget(nodeID) {
			return nil, fmt.Errorf("after_node breakpoint for parallel branch node %q is not supported; use before_node or resume from after_wave", nodeID)
		}
		nodes = append(nodes, nodeID)
	}
	return nodes, nil
}

func buildRunnerWarnings(diagnostics []core.ContractDiagnostic) []fruntime.WarningRecord {
	if len(diagnostics) == 0 {
		return nil
	}
	warnings := make([]fruntime.WarningRecord, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != core.ContractDiagnosticSeverityWarning {
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
