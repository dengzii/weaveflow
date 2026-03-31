package falcon

import fruntime "falcon/runtime"

func NewGraphRunner(graph *Graph, executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, codec fruntime.StateCodec, eventSink fruntime.EventSink) *fruntime.GraphRunner {
	return fruntime.NewGraphRunner(newRunnerGraph(graph), executionStore, checkpointStore, codec, eventSink)
}
