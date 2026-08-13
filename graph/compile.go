package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func (g *Graph) Compile() (*Runnable, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	patches := newCompilePatchCollector(g.compileBranchOrders())
	scheduled := newScheduledRunnable(g, patches, func(ctx context.Context, task fruntime.GraphTask, currentState *state.State) (core.ExecutionResult, error) {
		targetNode := g.nodes[task.NodeID]
		if targetNode == nil {
			return core.ExecutionResult{}, fmt.Errorf("node %q is not compiled", task.NodeID)
		}
		return g.executePatchNode(ctx, task, targetNode, currentState, patches)
	})
	return &Runnable{scheduled: scheduled, stateSchemas: cloneStateSchemas(g.stateSchemas)}, nil
}

func (g *Graph) executePatchNode(ctx context.Context, task fruntime.GraphTask, targetNode core.Node, currentState *state.State, patches *compilePatchCollector) (_ core.ExecutionResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			recordFailedBranchPatch(patches, currentState, task, resultErr)
		}
	}()
	if targetNode == nil {
		return core.ExecutionResult{}, fmt.Errorf("node %q is nil", task.NodeID)
	}
	if task.Failure != nil {
		ctx = core.WithFailure(ctx, core.FailureContext(*task.Failure))
	}
	resolvedContract, err := core.ContractFor(targetNode)
	if err != nil {
		return core.ExecutionResult{}, err
	}
	hasResolvedContract := false
	if g != nil && len(g.nodeContracts) > 0 {
		if nodeContract, ok := g.nodeContracts[task.NodeID]; ok {
			resolvedContract = nodeContract.Clone()
			hasResolvedContract = true
		}
	}
	if task.Dynamic && hasResolvedContract {
		if issues := state.ValidateInputPatchByContract(currentState, task.Input, resolvedContract); len(issues) > 0 {
			return core.ExecutionResult{}, state.NewValidationError("send input", issues)
		}
	}
	inputState, err := task.Input.ApplyWithReducers(currentState, g.reducers())
	if err != nil {
		return core.ExecutionResult{}, fmt.Errorf("apply input for task %q: %w", task.TaskID, err)
	}
	contract := &resolvedContract
	var readIssues []state.ValidationIssue
	var writeIssues []state.ValidationIssue
	result, err := core.ExecuteNodeWithOptions(ctx, inputState, targetNode, core.NodeExecutionOptions{
		Contract:               contract,
		EnforceInputProjection: hasResolvedContract,
		ValidateRequiredReads:  hasResolvedContract,
		ValidateWrites:         hasResolvedContract || len(contract.Fields) > 0 || contract.WildcardWrite,
		ApplyPatchToInput:      hasResolvedContract,
		Reducers:               g.reducers(),
		OnRequiredReadIssues: func(issues []state.ValidationIssue) {
			readIssues = append([]state.ValidationIssue(nil), issues...)
		},
		OnWriteIssues: func(issues []state.ValidationIssue) {
			writeIssues = append([]state.ValidationIssue(nil), issues...)
		},
	})
	if err != nil {
		if len(readIssues) > 0 || len(writeIssues) > 0 {
			return core.ExecutionResult{}, fmt.Errorf("node %q state contract violation: %w", task.NodeID, err)
		}
		return core.ExecutionResult{}, err
	}
	if patches != nil {
		patches.record(currentState, task, result.Patch)
	}
	return result, nil
}

func (g *Graph) compileForRunner(execution fruntime.RunnerExecution) (fruntime.RunnerRunnable, error) {
	patches, err := g.runnerPatchCollector(execution)
	if err != nil {
		return nil, err
	}
	scheduled := newScheduledRunnable(g, patches, func(ctx context.Context, task fruntime.GraphTask, currentState *state.State) (core.ExecutionResult, error) {
		targetNode := g.nodes[task.NodeID]
		inputState, err := task.Input.ApplyWithReducers(currentState, g.reducers())
		if err != nil {
			recordFailedBranchPatch(patches, currentState, task, err)
			return core.ExecutionResult{}, fmt.Errorf("apply input for task %q: %w", task.TaskID, err)
		}
		result, err := execution.ExecuteNode(ctx, task, targetNode, currentState, inputState)
		if err != nil {
			recordFailedBranchPatch(patches, currentState, task, err)
		} else if !patches.hasPatch(currentState, task.TaskID) {
			patches.record(currentState, task, result.Patch)
		}
		return result, err
	})
	scheduled.prepareNode = execution.PrepareNode
	if recorder, ok := execution.(fruntime.FailureRouteRecorder); ok {
		scheduled.recordFailure = recorder.OnFailureRouted
	}
	return scheduled, nil
}

func (g *Graph) runnerPatchCollector(execution fruntime.RunnerExecution) (*compilePatchCollector, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if execution == nil {
		return nil, fmt.Errorf("runner execution is nil")
	}
	patches := newCompilePatchCollector(g.compileBranchOrders())
	if setter, ok := execution.(fruntime.BranchPatchRecorderSetter); ok {
		setter.SetBranchPatchRecorder(patches)
	}
	if recorder, ok := execution.(fruntime.ParallelWaveRecorder); ok {
		patches.setWaveRecorder(recorder)
	}
	return patches, nil
}

func (g *Graph) mergeCompiledResults(ctx context.Context, current *state.State, results []core.ExecutionResult, patches *compilePatchCollector) (*state.State, error) {
	if len(results) == 0 {
		if current == nil {
			return state.NewState(), nil
		}
		return current, nil
	}
	if patches == nil {
		return nil, fmt.Errorf("state merge requires branch patches")
	}
	branches := patches.consume(current)
	if len(branches) != len(results) {
		return nil, fmt.Errorf("state merge requires branch patches: collected %d for %d task results", len(branches), len(results))
	}
	if err := patches.notifyParallelWave(ctx, current, branches); err != nil {
		return nil, err
	}
	return state.MergeParallelPatches(current, branches, state.ParallelMergeOptions{Contracts: g.nodeContracts, Schemas: g.stateSchemas, Reducers: g.reducers()})
}

func (g *Graph) reducers() map[string]state.Reducer {
	if g == nil || g.registry == nil {
		return nil
	}
	identifiers := map[string]state.Reducer{}
	for _, contract := range g.nodeContracts {
		for _, field := range contract.Fields {
			if field.Reducer == "" {
				continue
			}
			if reducer, ok := g.registry.FindReducer(field.Reducer); ok {
				identifiers[field.Reducer] = reducer
			}
		}
	}
	return identifiers
}

func (g *Graph) compileBranchOrders() map[string]int {
	if g == nil {
		return nil
	}
	orders := map[string]int{}
	nextOrder := 0
	for _, edge := range g.edgeSpecs {
		if edge.Condition != nil {
			continue
		}
		target := strings.TrimSpace(edge.To)
		if target == EndNodeRef {
			target = endNodeID
		}
		if _, exists := orders[target]; exists {
			continue
		}
		orders[target] = nextOrder
		nextOrder++
	}
	return orders
}

func (g *Graph) isParallelBranchTarget(nodeID string) bool {
	if g == nil || strings.TrimSpace(nodeID) == "" {
		return false
	}
	for from, targets := range g.defaultEdges {
		if len(targets) <= 1 || len(g.conditionalEdges[from]) > 0 {
			continue
		}
		for _, target := range targets {
			if target == nodeID {
				return true
			}
		}
	}
	return false
}
