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
	targetGraph.mutationMu.Lock()
	defer targetGraph.mutationMu.Unlock()
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
	runner, err := fruntime.NewGraphRunner(newRunnerGraph(targetGraph, graphHash, snapshotHash), executionStore, checkpointStore, codec, eventSink, baseOptions...)
	if err != nil {
		return nil, err
	}
	targetGraph.sealed = true
	return runner, nil
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
	graph        *Graph
	semanticHash string
	snapshotHash string
}

func newRunnerGraph(targetGraph *Graph, hashes ...string) fruntime.RunnerGraph {
	if targetGraph == nil {
		return nil
	}
	result := &runtimeGraph{graph: targetGraph}
	if len(hashes) > 0 {
		result.semanticHash = hashes[0]
	}
	if len(hashes) > 1 {
		result.snapshotHash = hashes[1]
	}
	return result
}

func (g *runtimeGraph) Validate() error {
	if g == nil || g.graph == nil {
		return fmt.Errorf("graph runner graph is nil")
	}
	if err := g.graph.Validate(); err != nil {
		return err
	}
	if g.semanticHash != "" {
		current, err := g.graph.SemanticHash()
		if err != nil {
			return fmt.Errorf("compute current graph semantic hash: %w", err)
		}
		if current != g.semanticHash {
			return fmt.Errorf("graph changed after runner construction: semantic hash %q, want %q", current, g.semanticHash)
		}
	}
	if g.snapshotHash != "" {
		current, err := g.graph.SnapshotHash()
		if err != nil {
			return fmt.Errorf("compute current graph snapshot hash: %w", err)
		}
		if current != g.snapshotHash {
			return fmt.Errorf("graph changed after runner construction: snapshot hash %q, want %q", current, g.snapshotHash)
		}
	}
	return nil
}

func (g *runtimeGraph) ValidateInitialState(initial *state.State) error {
	if g == nil || g.graph == nil {
		return fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.ValidateInitialState(initial)
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
	g.graph.mutationMu.Lock()
	defer g.graph.mutationMu.Unlock()
	if err := g.Validate(); err != nil {
		return nil, err
	}
	runnable, err := g.graph.compileForRunner(execution)
	if err == nil {
		g.graph.sealed = true
	}
	return runnable, err
}

func (g *runtimeGraph) ResolveNodeID(nodeID string) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveNodeID(nodeID)
}

func (g *runtimeGraph) ResolveEdgeTarget(target string) (string, error) {
	if g == nil || g.graph == nil {
		return "", fmt.Errorf("graph runner graph is nil")
	}
	return g.graph.resolveEdgeTarget(target)
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

func (g *runtimeGraph) CompensateEffect(ctx context.Context, nodeID string, request core.EffectCompensationRequest, currentState *state.State) error {
	if g == nil || g.graph == nil {
		return fmt.Errorf("graph runner graph is nil")
	}
	resolvedNodeID, err := g.graph.resolveNodeID(nodeID)
	if err != nil {
		return err
	}
	targetNode := g.graph.nodes[resolvedNodeID]
	if targetNode == nil {
		return fmt.Errorf("node %q is not found", resolvedNodeID)
	}
	if core.NodeEffectClass(targetNode) != core.EffectCompensatable {
		return fmt.Errorf("node %q does not declare compensatable effects", resolvedNodeID)
	}
	compensator, ok := targetNode.(core.EffectCompensator)
	if !ok {
		return fmt.Errorf("node %q does not implement effect compensation", resolvedNodeID)
	}
	if currentState == nil {
		currentState = state.NewState()
	}
	return compensator.CompensateEffect(core.NewContext(ctx), request, state.NewAccess(currentState))
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
