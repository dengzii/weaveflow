package falcon

import (
	"path/filepath"

	fruntime "falcon/runtime"
)

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

func NewLocalGraphRunner(graph *Graph, baseDir string) *GraphRunner {
	runner := NewGraphRunner(
		graph,
		NewFileExecutionStore(filepath.Join(baseDir, "execution")),
		NewFileCheckpointStore(filepath.Join(baseDir, "checkpoints")),
		NewJSONStateCodec(""),
		NewFileEventSink(filepath.Join(baseDir, "events")),
	)
	runner.ArtifactStore = NewFileArtifactStore(filepath.Join(baseDir, "artifacts"))
	return runner
}
