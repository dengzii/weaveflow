package weaveflow

import (
	"fmt"
	"strings"
	"weaveflow/builder"
	"weaveflow/builtin"
	"weaveflow/core"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
	wfstate "weaveflow/state"
)

type JSONSchema = dsl.JSONSchema
type GraphConditionSpec = dsl.GraphConditionSpec
type GraphNodeSpec = dsl.GraphNodeSpec
type StateFieldDefinition = dsl.StateFieldDefinition
type StateContract = dsl.StateContract
type StateFieldRef = dsl.StateFieldRef
type GraphDefinition = dsl.GraphDefinition
type NodeTypeSchema = dsl.NodeTypeSchema
type ConditionSchema = dsl.ConditionSchema

type GraphResolver = registry.GraphResolver
type NodeBuildContext = registry.NodeBuildContext
type NodeTypeDefinition = registry.NodeTypeDefinition
type SubgraphBuilder = registry.SubgraphBuilder
type SubgraphRunner = registry.SubgraphRunner
type Registry struct {
	*registry.Registry
}

type BuildContext = builder.BuildContext
type LegacyNodeBuilder = builder.LegacyNodeBuilder

type ConditionDefinition = registry.ConditionDefinition

func NewRegistry() *Registry {
	return &Registry{Registry: registry.NewRegistry()}
}

func DefaultRegistry() *Registry {
	return &Registry{Registry: builtin.NewDefaultRegistry()}
}

func RegisterDefaultComponents(r *Registry) {
	if r == nil {
		return
	}
	builtin.RegisterDefaultComponents(r.Registry)
}

func RegisterBuiltinCoreNodeTypes(r *Registry) {
	if r == nil {
		return
	}
	builtin.RegisterCoreNodeTypes(r.Registry)
}

func (r *Registry) AddConditionalEdge(g *Graph, from, to string, spec GraphConditionSpec) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	condition, err := r.ResolveCondition(spec)
	if err != nil {
		return err
	}
	return g.AddConditionalEdge(from, to, condition)
}

func (r *Registry) BuildGraph(def GraphDefinition, ctx *BuildContext) (*Graph, error) {
	return r.buildGraph(def, nil, ctx)
}

func (r *Registry) BuildGraphInstance(def GraphDefinition, instance dsl.GraphInstanceConfig, ctx *BuildContext) (*Graph, error) {
	return r.buildGraph(def, &instance, ctx)
}

func (r *Registry) buildGraph(def GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *BuildContext) (*Graph, error) {
	var err error
	def, ctx, err = builder.PrepareDefinition(def, instance, ctx)
	if err != nil {
		return nil, err
	}
	ctx.SubgraphBuilder = r.makeSubgraphBuilder(ctx)
	return builder.BuildFinalizedGraph(
		r.Registry,
		def,
		ctx,
		NewGraph,
		initialContractPathsFromStateFields(r.StateFields),
		func(g *Graph, def dsl.GraphDefinition) error {
			return builder.ApplyBuiltInNodeEdges(g, def)
		},
		func(def dsl.GraphDefinition, _ *registry.Registry) (map[string]core.NodeIOContract, error) {
			return builder.ResolveNodeContracts(def, r.Registry)
		},
	)
}

func (r *Registry) JSONSchema() JSONSchema {
	nodeTypes := make(map[string]dsl.NodeTypeSchema, len(r.NodeTypes))
	for key, def := range r.NodeTypes {
		nodeTypes[key] = def.NodeTypeSchema
	}
	conditions := make(map[string]dsl.ConditionSchema, len(r.Conditions))
	for key, def := range r.Conditions {
		conditions[key] = def.ConditionSchema
	}
	return dsl.BuildGraphDefinitionSchema(dsl.CommonStateSchemaID, r.StateFields, nodeTypes, conditions)
}

func stringConfig(config map[string]any, key string) string {
	return registry.StringConfig(config, key)
}

func AdaptLegacyNodeBuilder(build LegacyNodeBuilder) func(NodeBuildContext, dsl.GraphNodeSpec) (nodes.Node[wfstate.State], error) {
	return builder.AdaptLegacyNodeBuilder(build)
}

func (r *Registry) makeSubgraphBuilder(parentCtx *BuildContext) SubgraphBuilder {
	return func(graphRef string) (SubgraphRunner, error) {
		graphRef = strings.TrimSpace(graphRef)
		if graphRef == "" {
			return nil, fmt.Errorf("graph_ref is required")
		}
		if parentCtx == nil || parentCtx.GraphResolver == nil {
			return nil, fmt.Errorf("graph resolver is required")
		}
		if err := builder.ValidateGraphBuildPath(parentCtx.GraphBuildPath(), graphRef); err != nil {
			return nil, err
		}
		def, err := parentCtx.GraphResolver(graphRef)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", graphRef, err)
		}
		subgraphCtx := parentCtx.Clone()
		subgraphCtx.InstanceConfig = nil
		subgraphCtx.PushGraphRef(graphRef)
		graph, err := r.buildGraph(def, nil, subgraphCtx)
		if err != nil {
			return nil, fmt.Errorf("build graph %q: %w", graphRef, err)
		}
		return graph.Run, nil
	}
}
