package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	scheduled := newScheduledRunnable(g, patches, func(ctx context.Context, nodeID string, currentState *state.State) (*state.State, error) {
		targetNode := g.nodes[nodeID]
		if targetNode == nil {
			return currentState, fmt.Errorf("node %q is not compiled", nodeID)
		}
		return g.executePatchNode(ctx, nodeID, targetNode, currentState, patches)
	})
	return &Runnable{scheduled: scheduled}, nil
}

func (g *Graph) executePatchNode(ctx context.Context, nodeID string, targetNode core.Node, currentState *state.State, patches *compilePatchCollector) (_ *state.State, resultErr error) {
	defer func() {
		if resultErr != nil {
			recordFailedBranchPatch(patches, currentState, nodeID, resultErr)
		}
	}()
	if targetNode == nil {
		return currentState, fmt.Errorf("node %q is nil", nodeID)
	}
	resolvedContract, err := core.ContractFor(targetNode)
	if err != nil {
		return currentState, err
	}
	hasResolvedContract := false
	if g != nil && len(g.nodeContracts) > 0 {
		if nodeContract, ok := g.nodeContracts[nodeID]; ok {
			resolvedContract = nodeContract.Clone()
			hasResolvedContract = true
		}
	}
	contract := &resolvedContract
	var readIssues []state.ValidationIssue
	var writeIssues []state.ValidationIssue
	result, err := core.ExecuteNodeWithOptions(ctx, currentState, targetNode, core.NodeExecutionOptions{
		Contract:               contract,
		EnforceInputProjection: hasResolvedContract,
		ValidateRequiredReads:  hasResolvedContract,
		ValidateWrites:         hasResolvedContract || len(contract.Fields) > 0 || contract.WildcardWrite,
		ApplyPatchToInput:      hasResolvedContract,
		OnRequiredReadIssues: func(issues []state.ValidationIssue) {
			readIssues = append([]state.ValidationIssue(nil), issues...)
		},
		OnWriteIssues: func(issues []state.ValidationIssue) {
			writeIssues = append([]state.ValidationIssue(nil), issues...)
		},
	})
	if err != nil {
		if len(readIssues) > 0 || len(writeIssues) > 0 {
			return currentState, fmt.Errorf("node %q state contract violation: %w", nodeID, err)
		}
		return currentState, err
	}
	if patches != nil {
		patches.record(currentState, nodeID, result.Patch)
	}
	return result.State, nil
}

func (g *Graph) compileForRunner(execution fruntime.RunnerExecution) (fruntime.RunnerRunnable, error) {
	patches, err := g.runnerPatchCollector(execution)
	if err != nil {
		return nil, err
	}
	scheduled := newScheduledRunnable(g, patches, func(ctx context.Context, nodeID string, currentState *state.State) (*state.State, error) {
		targetNode := g.nodes[nodeID]
		next, err := execution.ExecuteNode(ctx, nodeID, targetNode, currentState)
		if err != nil {
			recordFailedBranchPatch(patches, currentState, nodeID, err)
		} else if !patches.hasPatch(currentState, nodeID) {
			patches.record(currentState, nodeID, stateDiffPatch(currentState, next))
		}
		return next, err
	})
	scheduled.prepareNode = execution.PrepareNode
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

func (g *Graph) mergeCompiledStates(ctx context.Context, current *state.State, newStates []*state.State, patches *compilePatchCollector) (*state.State, error) {
	if len(newStates) == 0 {
		if current == nil {
			return state.NewState(), nil
		}
		return current, nil
	}
	if len(newStates) == 1 {
		if patches != nil {
			_ = patches.consume(current)
		}
		if newStates[0] == nil {
			return current, nil
		}
		return newStates[0], nil
	}
	if patches == nil {
		return nil, fmt.Errorf("parallel state merge requires branch patches")
	}
	branches := patches.consume(current)
	if len(branches) != len(newStates) {
		return nil, fmt.Errorf("parallel state merge requires branch patches: collected %d for %d branch states", len(branches), len(newStates))
	}
	if err := patches.notifyParallelWave(ctx, current, branches); err != nil {
		return nil, err
	}
	return state.MergeParallelPatches(current, branches, state.ParallelMergeOptions{Contracts: g.nodeContracts})
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

func stateDiffPatch(before, after *state.State) state.Patch {
	beforeFlat := flattenStateForPatch(before)
	afterFlat := flattenStateForPatch(after)
	paths := make([]string, 0, len(beforeFlat)+len(afterFlat))
	seen := map[string]struct{}{}
	for path := range beforeFlat {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range afterFlat {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	ops := make([]state.PatchOp, 0, len(paths))
	for _, path := range paths {
		beforeValue, beforeOK := beforeFlat[path]
		afterValue, afterOK := afterFlat[path]
		if beforeOK && afterOK && jsonValuesEqual(beforeValue, afterValue) {
			continue
		}
		parsed, err := state.ParsePath(path)
		if err != nil {
			continue
		}
		if !afterOK {
			ops = append(ops, state.PatchOp{Kind: state.OpDelete, Path: parsed})
			continue
		}
		ops = append(ops, state.PatchOp{Kind: state.OpSet, Path: parsed, Value: afterValue})
	}
	return state.NewPatch(ops...)
}

func flattenStateForPatch(current *state.State) map[string]any {
	out := map[string]any{}
	if current == nil {
		return out
	}
	for section, value := range current.Export() {
		flattenStateValueForPatch(out, section, value)
	}
	return out
}

func flattenStateValueForPatch(out map[string]any, path string, value any) {
	mapped, ok := value.(map[string]any)
	if !ok || len(mapped) == 0 {
		out[path] = value
		return
	}
	for key, item := range mapped {
		flattenStateValueForPatch(out, path+"."+key, item)
	}
}

func jsonValuesEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}
