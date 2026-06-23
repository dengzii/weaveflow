package graphbuild

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
)

type GraphBuilder interface {
	AddNode(node.Node) error
	AddEdge(from, to string) error
	AddConditionalEdge(from, to string, condition registry.EdgeCondition) error
	SetEntryPoint(ref string) error
	SetFinishPoint(ref string) error
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
	if def.StateSchema != "" && def.StateSchema != dsl.CommonStateSchemaID {
		return dsl.GraphDefinition{}, ctx, fmt.Errorf("unsupported state schema %q", def.StateSchema)
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
	target GraphBuilder,
	reg *registry.Registry,
	def dsl.GraphDefinition,
	ctx *registry.BuildContext,
) error {
	if target == nil {
		return fmt.Errorf("graph builder is nil")
	}
	if reg == nil {
		return fmt.Errorf("registry is nil")
	}
	for _, nodeSpec := range def.Nodes {
		nodeDef, ok := reg.NodeTypes[nodeSpec.Type]
		if !ok {
			return fmt.Errorf("nodes type %q is not registered", nodeSpec.Type)
		}
		node, err := nodeDef.Build(ctx, nodeSpec)
		if err != nil {
			return err
		}
		if err := target.AddNode(node); err != nil {
			return err
		}
		if specTarget, ok := target.(interface{ SetNodeSpec(dsl.GraphNodeSpec) }); ok {
			specTarget.SetNodeSpec(nodeSpec)
		}
	}
	for _, edge := range def.Edges {
		if edge.Condition == nil {
			if err := target.AddEdge(edge.From, edge.To); err != nil {
				return err
			}
			continue
		}
		condition, err := reg.ResolveCondition(*edge.Condition)
		if err != nil {
			return err
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
