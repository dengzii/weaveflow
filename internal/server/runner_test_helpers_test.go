package server

import (
	"context"
	"testing"

	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/runtime"
)

func mustNewDefaultRunner(t *testing.T, graph *wfgraph.Graph, cfg Config, baseDir string, hub *EventHub) *runtime.GraphRunner {
	t.Helper()
	runner, err := newDefaultRunner(graph, cfg, baseDir, hub)
	if err != nil {
		t.Fatalf("newDefaultRunner() error: %v", err)
	}
	return runner
}

func newMinimalTestGraph(t *testing.T) *wfgraph.Graph {
	t.Helper()
	workflow := wfgraph.NewGraph(nil)
	input := node.NewUserInputNode(node.WithID("start"))
	if err := workflow.AddNode(input); err != nil {
		t.Fatalf("AddNode() error: %v", err)
	}
	if err := workflow.SetEntryPoint(input.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error: %v", err)
	}
	if err := workflow.AddEdge(input.ID(), wfgraph.EndNodeRef); err != nil {
		t.Fatalf("AddEdge() error: %v", err)
	}
	return workflow
}

func mustNewEventTestServer(t *testing.T, sink runtime.EventSink, eventBuffer int) (*Server, *runtime.GraphRunner) {
	t.Helper()
	server, err := New(context.Background(), Config{
		Graph:       newMinimalTestGraph(t),
		EventSink:   sink,
		EventBuffer: eventBuffer,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return server, server.Runner()
}
