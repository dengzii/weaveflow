package weaveflow

import (
	"fmt"
	"strconv"
	"strings"
	"weaveflow/builder"
	"weaveflow/builtin"
	"weaveflow/dsl"
	"weaveflow/nodes"
	"weaveflow/registry"
	fruntime "weaveflow/runtime"
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
type Registry registry.Registry

type BuildContext = builder.BuildContext
type LegacyNodeBuilder = builder.LegacyNodeBuilder

type ConditionDefinition = registry.ConditionDefinition

func NewRegistry() *Registry {
	return (*Registry)(registry.NewRegistry())
}

func DefaultRegistry() *Registry {
	return (*Registry)(builtin.NewDefaultRegistry())
}

func RegisterDefaultComponents(r *Registry) {
	if r == nil {
		return
	}
	builtin.RegisterDefaultComponents((*registry.Registry)(r))
}

func RegisterBuiltinCoreNodeTypes(r *Registry) {
	if r == nil {
		return
	}
	builtin.RegisterCoreNodeTypes((*registry.Registry)(r))
}

func (r *Registry) RegisterStateField(def StateFieldDefinition) {
	(*registry.Registry)(r).RegisterStateField(def)
}

func (r *Registry) RegisterNodeType(def NodeTypeDefinition) {
	(*registry.Registry)(r).RegisterNodeType(def)
}

func (r *Registry) RegisterCondition(def ConditionDefinition) {
	(*registry.Registry)(r).RegisterCondition(def)
}

func (r *Registry) ResolveCondition(spec GraphConditionSpec) (EdgeCondition, error) {
	return (*registry.Registry)(r).ResolveCondition(spec)
}

func (r *Registry) ResolveNodeStateContract(spec dsl.GraphNodeSpec) (dsl.StateContract, error) {
	return (*registry.Registry)(r).ResolveNodeStateContract(spec)
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
		(*registry.Registry)(r),
		def,
		ctx,
		NewGraph,
		initialContractPathsFromStateFields(r.StateFields),
		func(g *Graph, def dsl.GraphDefinition) error {
			return builder.ApplyBuiltInNodeEdges(g, def)
		},
		func(g *Graph, _ *registry.Registry) map[string]fruntime.NodeIOContract {
			return builder.ResolveNodeContracts(g, (*registry.Registry)(r))
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

func stringSliceConfig(config map[string]any, key string) []string {
	if len(config) == 0 {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	values, ok := raw.([]any)
	if ok {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	}
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}
	return nil
}

func stringConfig(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func intConfig(config map[string]any, key string) (int, bool) {
	if len(config) == 0 {
		return 0, false
	}

	switch value := config[key].(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	if len(config) == 0 {
		return false, false
	}

	switch value := config[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}

	return false, false
}

func floatConfig(config map[string]any, key string) (float64, bool) {
	if len(config) == 0 {
		return 0, false
	}

	switch value := config[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	}

	return 0, false
}

func AdaptLegacyNodeBuilder(build LegacyNodeBuilder) func(NodeBuildContext, dsl.GraphNodeSpec) (nodes.Node[State], error) {
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
