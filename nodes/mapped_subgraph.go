package nodes

import (
	"context"
	"fmt"
	"sort"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"github.com/google/uuid"
)

// MappedSubgraphNode invokes a subgraph with explicit input/output path mappings,
// so the subgraph only sees the state it needs and only writes back what it declares.
type MappedSubgraphNode struct {
	NodeInfo
	GraphRef       string
	InputMap       map[string]string // parent_path -> subgraph_path
	OutputMap      map[string]string // subgraph_path -> parent_path
	InvokeSubgraph SubgraphInvoker
}

func NewMappedSubgraphNode() *MappedSubgraphNode {
	id := uuid.New()
	return &MappedSubgraphNode{
		NodeInfo: NodeInfo{
			NodeID:          "MappedSubgraph_" + id.String(),
			NodeName:        "Mapped Subgraph",
			NodeDescription: "Invoke another graph with explicitly mapped input and output state paths.",
		},
	}
}

func (n *MappedSubgraphNode) Invoke(ctx context.Context, state wfstate.State) (wfstate.State, error) {
	if n.InvokeSubgraph == nil {
		return state, fmt.Errorf("mapped subgraph node %q has no invoker for graph_ref %q", n.ID(), n.GraphRef)
	}
	subgraphInput := n.buildSubgraphInput(state)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphStarted, map[string]any{
		"graph_ref": n.GraphRef,
	})
	subgraphResult, err := n.InvokeSubgraph(ctx, subgraphInput)
	if err != nil {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFailed, map[string]any{
			"graph_ref": n.GraphRef,
			"error":     err.Error(),
		})
		return state, err
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFinished, map[string]any{
		"graph_ref": n.GraphRef,
	})
	return n.mergeOutputToState(state, subgraphResult)
}

func (n *MappedSubgraphNode) Execute(ctx context.Context, input wfstate.State) (wfstate.State, error) {
	if n.InvokeSubgraph == nil {
		return nil, fmt.Errorf("mapped subgraph node %q has no invoker for graph_ref %q", n.ID(), n.GraphRef)
	}
	subgraphInput := n.buildSubgraphInput(input)
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphStarted, map[string]any{
		"graph_ref": n.GraphRef,
	})
	subgraphResult, err := n.InvokeSubgraph(ctx, subgraphInput)
	if err != nil {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFailed, map[string]any{
			"graph_ref": n.GraphRef,
			"error":     err.Error(),
		})
		return nil, err
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFinished, map[string]any{
		"graph_ref": n.GraphRef,
	})
	return n.buildOutputPatch(subgraphResult)
}

// buildSubgraphInput creates a minimal state for the subgraph by mapping paths from parent state.
func (n *MappedSubgraphNode) buildSubgraphInput(parentState wfstate.State) wfstate.State {
	subgraphInput := wfstate.State{}
	for parentPath, subgraphPath := range n.InputMap {
		value, ok := wfstate.ResolveContractPathValue(parentState, parentPath)
		if !ok {
			continue
		}
		wfstate.SetContractPathValue(subgraphInput, subgraphPath, value)
	}
	return subgraphInput
}

// buildOutputPatch extracts mapped paths from subgraph result into a parent-side patch.
func (n *MappedSubgraphNode) buildOutputPatch(subgraphResult wfstate.State) (wfstate.State, error) {
	patch := wfstate.State{}
	for subgraphPath, parentPath := range n.OutputMap {
		value, ok := wfstate.ResolveContractPathValue(subgraphResult, subgraphPath)
		if !ok {
			continue
		}
		wfstate.SetContractPathValue(patch, parentPath, value)
	}
	return patch, nil
}

// mergeOutputToState applies output mappings onto a clone of the parent state (for Invoke compat).
func (n *MappedSubgraphNode) mergeOutputToState(parentState wfstate.State, subgraphResult wfstate.State) (wfstate.State, error) {
	result := parentState.CloneState()
	for subgraphPath, parentPath := range n.OutputMap {
		value, ok := wfstate.ResolveContractPathValue(subgraphResult, subgraphPath)
		if !ok {
			continue
		}
		wfstate.SetContractPathValue(result, parentPath, value)
	}
	return result, nil
}

func (n *MappedSubgraphNode) GraphNodeSpec() dsl.GraphNodeSpec {
	config := map[string]any{
		"graph_ref": n.GraphRef,
	}
	if len(n.InputMap) > 0 {
		config["input_map"] = cloneMappedSubgraphMap(n.InputMap)
	}
	if len(n.OutputMap) > 0 {
		config["output_map"] = cloneMappedSubgraphMap(n.OutputMap)
	}
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "mapped_subgraph",
		Description: n.Description(),
		Config:      config,
	}
}

func cloneMappedSubgraphMap(m map[string]string) map[string]any {
	if len(m) == 0 {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// MappedSubgraphInputPaths returns the parent-side read paths from input_map (sorted).
func MappedSubgraphInputPaths(inputMap map[string]string) []string {
	paths := make([]string, 0, len(inputMap))
	for parentPath := range inputMap {
		paths = append(paths, parentPath)
	}
	sort.Strings(paths)
	return paths
}

// MappedSubgraphOutputPaths returns the parent-side write paths from output_map (sorted).
func MappedSubgraphOutputPaths(outputMap map[string]string) []string {
	paths := make([]string, 0, len(outputMap))
	for _, parentPath := range outputMap {
		paths = append(paths, parentPath)
	}
	sort.Strings(paths)
	return paths
}
