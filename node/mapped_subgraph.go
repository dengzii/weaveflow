package node

import (
	"context"
	"fmt"
	"sort"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
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

func MappedSubgraphNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:        NodeTypeMappedSubgraph,
			Title:       "Mapped Subgraph Node",
			Description: "Invoke another graph with explicit input/output state path mappings.",
			ConfigSchema: dsl.JSONSchema{
				"type": "object",
				"properties": dsl.JSONSchema{
					"graph_ref":  dsl.JSONSchema{"type": "string"},
					"input_map":  dsl.JSONSchema{"type": "object", "additionalProperties": dsl.JSONSchema{"type": "string"}},
					"output_map": dsl.JSONSchema{"type": "object", "additionalProperties": dsl.JSONSchema{"type": "string"}},
				},
				"required":             []string{"graph_ref"},
				"additionalProperties": false,
			},
		},
		ResolveStateContract: func(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
			inputMap := config.StringMap(spec.Config, "input_map")
			outputMap := config.StringMap(spec.Config, "output_map")
			fields := make([]dsl.StateFieldRef, 0, len(inputMap)+len(outputMap))
			inputPaths := make([]string, 0, len(inputMap))
			for parentPath := range inputMap {
				inputPaths = append(inputPaths, parentPath)
			}
			sort.Strings(inputPaths)
			for _, parentPath := range inputPaths {
				fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(parentPath), Mode: dsl.StateAccessRead, Required: true, Description: "Input path mapped into the subgraph."})
			}
			outputPaths := make([]string, 0, len(outputMap))
			for _, parentPath := range outputMap {
				outputPaths = append(outputPaths, parentPath)
			}
			sort.Strings(outputPaths)
			for _, parentPath := range outputPaths {
				fields = append(fields, dsl.StateFieldRef{Path: canonicalContractPath(parentPath), Mode: dsl.StateAccessWrite, Description: "Output path mapped back from the subgraph.", MergeStrategy: dsl.StateMergeMerge})
			}
			return dsl.StateContract{Fields: fields}, nil
		},
		Build: func(ctx *registry.BuildContext, spec dsl.GraphNodeSpec) (Node, error) {
			graphRef := config.String(spec.Config, "graph_ref")
			if graphRef == "" {
				return nil, fmt.Errorf("build mapped_subgraph node %q: graph_ref is required", spec.ID)
			}
			if ctx == nil || ctx.SubgraphBuilder == nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q: subgraph builder is required", spec.ID)
			}
			runner, err := ctx.SubgraphBuilder(graphRef)
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q: %w", spec.ID, err)
			}
			mappedNode := NewMappedSubgraphNode(WithID(spec.ID))
			applyNodeMetadata(&mappedNode.Base, spec)
			mappedNode.GraphRef = graphRef
			mappedNode.InputMappings, err = parsePathMappings(config.StringMap(spec.Config, "input_map"))
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q input_map: %w", spec.ID, err)
			}
			mappedNode.OutputMappings, err = parsePathMappings(config.StringMap(spec.Config, "output_map"))
			if err != nil {
				return nil, fmt.Errorf("build mapped_subgraph node %q output_map: %w", spec.ID, err)
			}
			mappedNode.InvokeSubgraph = runner
			return mappedNode, nil
		},
	}
}

func (n *MappedSubgraphNode) Execute(ctx core.Context, access *state.Access) error {
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

func parsePathMappings(values map[string]string) ([]PathMapping, error) {
	if len(values) == 0 {
		return nil, nil
	}
	mappings := make([]PathMapping, 0, len(values))
	for fromText, toText := range values {
		from, err := parseRequiredStatePath(fromText)
		if err != nil {
			return nil, err
		}
		to, err := parseRequiredStatePath(toText)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, PathMapping{From: from, To: to})
	}
	return mappings, nil
}
