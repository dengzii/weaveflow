package node

import (
	"context"
	"fmt"
	"sort"

	fruntime "weaveflow/runtime"
	"weaveflow/state"
)

type SubgraphInvoker func(context.Context, *state.State) (*state.State, error)

type PathMapping struct {
	From state.Path
	To   state.Path
}

type MappedSubgraphNode struct {
	Base
	GraphRef       string
	InputMappings  []PathMapping
	OutputMappings []PathMapping
	InvokeSubgraph SubgraphInvoker
}

func NewMappedSubgraphNode(options ...NodeOption) *MappedSubgraphNode {
	node := &MappedSubgraphNode{
		Base: NewBase(Spec{
			Name:        NodeTypeMappedSubgraph,
			Description: "Invoke another graph with explicitly mapped state paths.",
		}),
	}
	applyNodeOptions(&node.Base, options)
	return node
}

func (n *MappedSubgraphNode) Execute(ctx context.Context, access *state.Access) error {
	if n.InvokeSubgraph == nil {
		return fmt.Errorf("mapped subgraph node %q has no invoker for graph_ref %q", n.ID(), n.GraphRef)
	}
	subgraphInput, err := n.buildSubgraphInput(access)
	if err != nil {
		return err
	}

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphStarted, map[string]any{
		"graph_ref": n.GraphRef,
	})
	subgraphResult, err := n.InvokeSubgraph(ctx, subgraphInput)
	if err != nil {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFailed, map[string]any{
			"graph_ref": n.GraphRef,
			"error":     err.Error(),
		})
		return err
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFinished, map[string]any{
		"graph_ref": n.GraphRef,
	})
	return n.applyOutputMappings(access, subgraphResult)
}

func (n *MappedSubgraphNode) buildSubgraphInput(parent *state.Access) (*state.State, error) {
	subgraphAccess := state.NewEditingAccess(parent.Registry(), state.NewState())
	for _, mapping := range n.InputMappings {
		if mapping.From.Empty() || mapping.To.Empty() {
			return nil, fmt.Errorf("mapped subgraph node %q has an empty input mapping", n.ID())
		}
		value, ok := parent.ReadAny(mapping.From)
		if !ok {
			continue
		}
		if err := subgraphAccess.SetAny(mapping.To, value); err != nil {
			return nil, err
		}
	}
	return subgraphAccess.State(), nil
}

func (n *MappedSubgraphNode) applyOutputMappings(parent *state.Access, subgraphResult *state.State) error {
	subgraphAccess := state.NewAccess(parent.Registry(), subgraphResult)
	for _, mapping := range n.OutputMappings {
		if mapping.From.Empty() || mapping.To.Empty() {
			return fmt.Errorf("mapped subgraph node %q has an empty output mapping", n.ID())
		}
		value, ok := subgraphAccess.ReadAny(mapping.From)
		if !ok {
			continue
		}
		if err := parent.SetAny(mapping.To, value); err != nil {
			return err
		}
	}
	return nil
}

func (n *MappedSubgraphNode) Contract(*state.Registry) (state.Contract, error) {
	fields := make([]state.FieldAccess, 0, len(n.InputMappings)+len(n.OutputMappings))
	for _, mapping := range n.InputMappings {
		if mapping.From.Empty() || mapping.To.Empty() {
			return state.Contract{}, fmt.Errorf("mapped subgraph node %q has an empty input mapping", n.ID())
		}
		fields = append(fields, state.FieldAccess{
			Path:        mapping.From,
			Mode:        state.AccessRead,
			Type:        "any",
			Description: "Parent input path mapped into the subgraph.",
		})
	}
	for _, mapping := range n.OutputMappings {
		if mapping.From.Empty() || mapping.To.Empty() {
			return state.Contract{}, fmt.Errorf("mapped subgraph node %q has an empty output mapping", n.ID())
		}
		fields = append(fields, state.FieldAccess{
			Path:        mapping.To,
			Mode:        state.AccessWrite,
			Type:        "any",
			Description: "Parent output path mapped from the subgraph result.",
		})
	}
	return state.NewContract(fields...), nil
}

func MappedSubgraphInputPaths(inputMappings []PathMapping) []string {
	paths := make([]string, 0, len(inputMappings))
	for _, mapping := range inputMappings {
		if !mapping.From.Empty() {
			paths = append(paths, mapping.From.String())
		}
	}
	sort.Strings(paths)
	return paths
}

func MappedSubgraphOutputPaths(outputMappings []PathMapping) []string {
	paths := make([]string, 0, len(outputMappings))
	for _, mapping := range outputMappings {
		if !mapping.To.Empty() {
			paths = append(paths, mapping.To.String())
		}
	}
	sort.Strings(paths)
	return paths
}
