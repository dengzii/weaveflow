package builder

import (
	"fmt"

	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

type GraphBuilder interface {
	AddNode(core.Node[wfstate.State]) error
	AddEdge(from, to string) error
	AddConditionalEdge(from, to string, condition registry.EdgeCondition) error
	SetEntryPoint(ref string) error
	SetFinishPoint(ref string) error
}

type FinalizableGraph interface {
	GraphBuilder
	SetInitialStatePaths([]string)
	SetNodeContracts(map[string]wfstate.NodeIOContract)
	ValidateGraph() error
	ContractDiagnostics() []core.ContractDiagnostic
}

func PrepareDefinition(def dsl.GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *BuildContext) (dsl.GraphDefinition, *BuildContext, error) {
	def = dsl.NormalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return dsl.GraphDefinition{}, ctx, err
	}
	if def.StateSchema != "" && def.StateSchema != dsl.CommonStateSchemaID {
		return dsl.GraphDefinition{}, ctx, fmt.Errorf("unsupported state schema %q", def.StateSchema)
	}
	if instance != nil {
		normalized := *instance
		if err := normalized.Validate(); err != nil {
			return dsl.GraphDefinition{}, ctx, err
		}
		applied, err := registry.ApplyGraphInstanceConfig(def, normalized)
		if err != nil {
			return dsl.GraphDefinition{}, ctx, err
		}
		def = applied
		if ctx != nil {
			ctx = ctx.Clone()
			ctx.InstanceConfig = &normalized
		}
	}
	if ctx == nil {
		ctx = &BuildContext{}
	}
	return def, ctx, nil
}

func PopulateGraph(
	target GraphBuilder,
	reg *registry.Registry,
	def dsl.GraphDefinition,
	ctx *BuildContext,
	applyBuiltInEdges func(dsl.GraphDefinition) error,
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
	}
	if applyBuiltInEdges != nil {
		if err := applyBuiltInEdges(def); err != nil {
			return err
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

func BuildFinalizedGraph[T FinalizableGraph](
	reg *registry.Registry,
	def dsl.GraphDefinition,
	ctx *BuildContext,
	newGraph func() T,
	initialStatePaths []string,
	applyBuiltInEdges func(T, dsl.GraphDefinition) error,
	resolveNodeContracts func(T, *registry.Registry) map[string]wfstate.NodeIOContract,
) (T, error) {
	var zero T
	if reg == nil {
		return zero, fmt.Errorf("registry is nil")
	}
	if newGraph == nil {
		return zero, fmt.Errorf("new graph factory is nil")
	}
	if resolveNodeContracts == nil {
		return zero, fmt.Errorf("resolve node contracts callback is nil")
	}

	graph := newGraph()
	graph.SetInitialStatePaths(initialStatePaths)

	var applyBuiltIns func(dsl.GraphDefinition) error
	if applyBuiltInEdges != nil {
		applyBuiltIns = func(def dsl.GraphDefinition) error {
			return applyBuiltInEdges(graph, def)
		}
	}
	if err := PopulateGraph(graph, reg, def, ctx, applyBuiltIns); err != nil {
		return zero, err
	}

	graph.SetNodeContracts(resolveNodeContracts(graph, reg))
	if err := graph.ValidateGraph(); err != nil {
		if ctx != nil {
			ctx.EmitContractDiagnostics(graph.ContractDiagnostics())
		}
		return zero, err
	}
	if ctx != nil {
		ctx.EmitContractDiagnostics(graph.ContractDiagnostics())
	}
	return graph, nil
}
