package weaveflow

import (
	"weaveflow/builder"
	"weaveflow/dsl"
	"weaveflow/graph"
	"weaveflow/registry"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"

	"go.uber.org/zap"
)

const EndNodeRef = graph.EndNodeRef

type (
	Graph    = graph.Graph
	Runnable = graph.Runnable
)

func NewGraph() *Graph { return graph.NewGraph() }

func BuildGraph(reg *registry.Registry, def dsl.GraphDefinition, ctx *builder.BuildContext) (*Graph, error) {
	return graph.BuildGraph(reg, def, ctx)
}

func BuildGraphInstance(reg *registry.Registry, def dsl.GraphDefinition, instance dsl.GraphInstanceConfig, ctx *builder.BuildContext) (*Graph, error) {
	return graph.BuildGraphInstance(reg, def, instance, ctx)
}

func LoadGraphFromFile(buildContext *builder.BuildContext, path string) (*Graph, error) {
	return graph.LoadGraphFromFile(buildContext, path)
}

func NewGraphRunner(g *Graph, es fruntime.ExecutionStore, cs fruntime.CheckpointStore, codec wfstate.StateCodec, sink fruntime.EventSink) *fruntime.GraphRunner {
	return graph.NewGraphRunner(g, es, cs, codec, sink)
}

func SetLogger(l *zap.Logger) { fruntime.SetLogger(l) }
