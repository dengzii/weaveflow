package graphbuild

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type Builder interface {
	AddNode(core.Node) error
	AddEdge(from, to string) error
	AddConditionalEdge(from, to string, condition registry.EdgeCondition) error
	SetEntryPoint(ref string) error
	SetFinishPoint(ref string) error
}

type resolvedConditionGraphBuilder interface {
	AddResolvedConditionalEdge(from, to string, condition registry.EdgeCondition, contract state.Contract) error
}

func ValidateGraphBuildPath(path []string, next string) error {
	next = strings.TrimSpace(next)
	if next == "" {
		return fmt.Errorf("graph_ref is required")
	}
	for _, existing := range path {
		if existing == next {
			cycle := append(append([]string(nil), path...), next)
			return fmt.Errorf("cyclic graph_ref dependency detected: %s", strings.Join(cycle, " -> "))
		}
	}
	return nil
}

func PrepareDefinition(def dsl.GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *registry.BuildContext) (dsl.GraphDefinition, *registry.BuildContext, error) {
	def = dsl.NormalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return dsl.GraphDefinition{}, ctx, err
	}
	if ctx == nil {
		ctx = &registry.BuildContext{}
	} else {
		ctx = ctx.Clone()
	}
	if instance != nil {
		normalized := CloneGraphInstanceConfig(*instance)
		if err := normalized.Validate(); err != nil {
			return dsl.GraphDefinition{}, ctx, err
		}
		applied, err := ApplyGraphInstanceConfig(def, normalized)
		if err != nil {
			return dsl.GraphDefinition{}, ctx, err
		}
		def = applied
		ctx.InstanceConfig = &normalized
	}
	return def, ctx, nil
}

func PopulateGraph(
	target Builder,
	reg *registry.Registry,
	def dsl.GraphDefinition,
	ctx *registry.BuildContext,
	resolved ResolvedGraphBindings,
) error {
	if target == nil {
		return fmt.Errorf("graph builder is nil")
	}
	if reg == nil {
		return fmt.Errorf("registry is nil")
	}
	for _, nodeSpec := range def.Nodes {
		nodeDef, ok := reg.FindNodeType(nodeSpec.Type)
		if !ok {
			return fmt.Errorf("nodes type %q is not registered", nodeSpec.Type)
		}
		resolvedSpec, ok := resolved.Nodes[nodeSpec.ID]
		if !ok {
			return fmt.Errorf("node %q has no resolved state bindings", nodeSpec.ID)
		}
		node, err := nodeDef.Build(ctx, resolvedSpec)
		if err != nil {
			return err
		}
		if err := target.AddNode(node); err != nil {
			return err
		}
		if specTarget, ok := target.(interface{ SetNodeSpec(dsl.GraphNodeSpec) error }); ok {
			if err := specTarget.SetNodeSpec(nodeSpec); err != nil {
				return err
			}
		}
	}
	for edgeIndex, edge := range def.Edges {
		if edge.Condition == nil {
			if err := target.AddEdge(edge.From, edge.To); err != nil {
				return err
			}
			continue
		}
		resolvedCondition, ok := resolved.Conditions[edgeIndex]
		if !ok {
			return fmt.Errorf("condition on edge %q -> %q has no resolved state bindings", edge.From, edge.To)
		}
		condition, err := reg.ResolveCondition(resolvedCondition)
		if err != nil {
			return err
		}
		if resolvedTarget, ok := target.(resolvedConditionGraphBuilder); ok {
			contract, exists := resolved.ConditionContracts[edgeIndex]
			if !exists {
				return fmt.Errorf("condition on edge %q -> %q has no resolved state contract", edge.From, edge.To)
			}
			if err := resolvedTarget.AddResolvedConditionalEdge(edge.From, edge.To, condition, contract); err != nil {
				return err
			}
			continue
		}
		if err := target.AddConditionalEdge(edge.From, edge.To, condition); err != nil {
			return err
		}
	}
	if def.EntryPoint != "" {
		if err := target.SetEntryPoint(def.EntryPoint); err != nil {
			return err
		}
	}
	if def.FinishPoint != "" {
		if err := target.SetFinishPoint(def.FinishPoint); err != nil {
			return err
		}
	}
	return nil
}
