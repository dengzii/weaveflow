package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type Builder struct {
	registry *registry.Registry
}

func NewBuilder(reg *registry.Registry) *Builder {
	return &Builder{registry: reg}
}

func (builder *Builder) Build(def dsl.GraphDefinition, ctx *registry.BuildContext) (*Graph, error) {
	return builder.build(def, nil, ctx, nil)
}

func (builder *Builder) BuildInstance(def dsl.GraphDefinition, instance dsl.GraphInstanceConfig, ctx *registry.BuildContext) (*Graph, error) {
	return builder.build(def, &instance, ctx, nil)
}

func (builder *Builder) BuildFile(path string, ctx *registry.BuildContext) (*Graph, error) {
	def, err := LoadGraphDefinitionFile(path)
	if err != nil {
		return nil, err
	}
	return builder.Build(def, ctx)
}

func (builder *Builder) build(def dsl.GraphDefinition, instance *dsl.GraphInstanceConfig, ctx *registry.BuildContext, buildPath []string) (*Graph, error) {
	if builder == nil || builder.registry == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	var err error
	def, ctx, err = graphbuild.PrepareDefinition(def, instance, ctx)
	if err != nil {
		return nil, err
	}
	ctx.ChildRunBuilder = builder.makeChildRunBuilder(ctx, buildPath)

	graph := NewGraph(builder.registry)
	graph.setDefinitionMetadata(def)
	if err := graph.applyDefinitionExecutionPolicy(def); err != nil {
		return nil, err
	}
	bindings, err := graphbuild.ResolveGraphBindings(def, builder.registry)
	if err != nil {
		return nil, err
	}
	graph.setInitialStatePaths(bindings.InitialStatePaths)
	graph.setStateSchemas(bindings.StateSchemas)
	graph.setNodeContracts(bindings.NodeContracts)
	graph.setConditionContracts(bindings.ConditionContractsBySource)
	graph.setStateBindingSemantics(graphbuild.StateBindingSemantics(bindings))

	if err := graphbuild.PopulateGraph(graph, builder.registry, def, ctx, bindings); err != nil {
		return nil, err
	}
	if err := graph.Validate(); err != nil {
		ctx.EmitContractDiagnostics(graph.ContractDiagnostics())
		return nil, err
	}
	ctx.EmitContractDiagnostics(graph.ContractDiagnostics())
	return graph, nil
}

func (builder *Builder) makeChildRunBuilder(parentCtx *registry.BuildContext, buildPath []string) registry.ChildRunBuilder {
	return func(graphRef string) (registry.ChildRunRunner, error) {
		graphRef = strings.TrimSpace(graphRef)
		if graphRef == "" {
			return nil, fmt.Errorf("graph_ref is required")
		}
		if parentCtx == nil || parentCtx.GraphResolver == nil {
			return nil, fmt.Errorf("graph resolver is required")
		}
		if err := graphbuild.ValidateGraphBuildPath(buildPath, graphRef); err != nil {
			return nil, err
		}
		def, err := parentCtx.GraphResolver(graphRef)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", graphRef, err)
		}
		subgraphCtx := parentCtx.Clone()
		subgraphCtx.InstanceConfig = nil
		nextPath := append(append([]string(nil), buildPath...), graphRef)
		childGraph, err := builder.build(def, nil, subgraphCtx, nextPath)
		if err != nil {
			return nil, fmt.Errorf("build graph %q: %w", graphRef, err)
		}
		return func(ctx context.Context, request fruntime.ChildRunRequest, input *state.State) (fruntime.ChildRunResult, error) {
			parentRunner, ok := fruntime.GraphRunnerFromContext(ctx)
			if !ok {
				return fruntime.ChildRunResult{}, fmt.Errorf("child run %q requires a parent graph runner", graphRef)
			}
			childRunner, err := NewGraphRunner(childGraph, parentRunner.ExecutionStore(), parentRunner.CheckpointStore(), parentRunner.StateCodec(), parentRunner.EventSink(),
				fruntime.WithArtifactStore(parentRunner.ArtifactStore()),
				fruntime.WithRuntimeTransactionStore(parentRunner.TransactionStore()),
				fruntime.WithGraphMetadata(childGraph.name, childGraph.version, "", "", parentRunner.GraphSessionID()),
			)
			if err != nil {
				return fruntime.ChildRunResult{}, err
			}
			return childRunner.RunChild(ctx, request, input)
		}, nil
	}
}
