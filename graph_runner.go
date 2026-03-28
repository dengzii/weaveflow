package falcon

import fruntime "falcon/runtime"

type GraphRunner struct {
	*fruntime.GraphRunner
	Graph *Graph
}

func NewGraphRunner(graph *Graph, executionStore ExecutionStore, checkpointStore CheckpointStore, codec StateCodec, eventSink EventSink) *GraphRunner {
	inner := fruntime.NewGraphRunner(newRunnerGraph(graph), executionStore, checkpointStore, codec, eventSink)
	inner.GraphVersion = GraphDefinitionVersion
	return &GraphRunner{
		GraphRunner: inner,
		Graph:       graph,
	}
}
