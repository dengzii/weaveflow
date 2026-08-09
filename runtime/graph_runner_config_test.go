package runtime_test

import (
	"strings"
	"testing"

	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestNewGraphRunnerRequiresExecutionDependencies(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	workflow := newTestGraph(t)
	executionStore := runtime.NewFileExecutionStore(directory)
	checkpointStore := runtime.NewFileCheckpointStore(directory)
	codec := state.NewJSONStateCodec("")
	eventSink := runtime.NewFileEventSink(directory)

	tests := []struct {
		name            string
		graph           *wfgraph.Graph
		executionStore  runtime.ExecutionStore
		checkpointStore runtime.CheckpointStore
		codec           state.StateCodec
		eventSink       runtime.EventSink
		want            string
	}{
		{name: "graph", executionStore: executionStore, checkpointStore: checkpointStore, codec: codec, eventSink: eventSink, want: "graph is required"},
		{name: "execution store", graph: workflow, checkpointStore: checkpointStore, codec: codec, eventSink: eventSink, want: "execution store is required"},
		{name: "checkpoint store", graph: workflow, executionStore: executionStore, codec: codec, eventSink: eventSink, want: "checkpoint store is required"},
		{name: "state codec", graph: workflow, executionStore: executionStore, checkpointStore: checkpointStore, eventSink: eventSink, want: "state codec is required"},
		{name: "event sink", graph: workflow, executionStore: executionStore, checkpointStore: checkpointStore, codec: codec, want: "event sink is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, err := wfgraph.NewGraphRunner(test.graph, test.executionStore, test.checkpointStore, test.codec, test.eventSink)
			if runner != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewGraphRunner() = %#v, %v; want %q", runner, err, test.want)
			}
		})
	}
}

func TestGraphRunnerOptionsAndGettersCloneMutableValues(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	breakpoints := []runtime.Breakpoint{{ID: "before:start", NodeID: "start", Stage: string(runtime.CheckpointBeforeNode), Enabled: true}}
	warnings := []runtime.WarningRecord{{Code: "test", Message: "warning", Sources: []string{"source-1"}}}
	contracts := map[string]state.Contract{
		"start": state.NewContract(state.FieldAccess{Path: state.Shared("request", "input"), Mode: state.AccessRead}),
	}

	runner, err := wfgraph.NewGraphRunner(
		newTestGraph(t),
		runtime.NewFileExecutionStore(directory),
		runtime.NewFileCheckpointStore(directory),
		state.NewJSONStateCodec(""),
		runtime.NewFileEventSink(directory),
		runtime.WithBreakpoints(breakpoints...),
		runtime.WithStartupWarnings(warnings),
		runtime.WithNodeContracts(contracts),
	)
	if err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}

	breakpoints[0].NodeID = "changed"
	warnings[0].Sources[0] = "changed"
	changedContract := contracts["start"]
	changedContract.Fields[0].Path = state.Shared("changed")
	contracts["start"] = changedContract
	if got := runner.Breakpoints()[0].NodeID; got != "start" {
		t.Fatalf("breakpoint option was not cloned: %q", got)
	}
	if got := runner.StartupWarnings()[0].Sources[0]; got != "source-1" {
		t.Fatalf("warning option was not cloned: %q", got)
	}
	if got := runner.NodeContracts()["start"].Fields[0].Path.String(); got != "shared.request.input" {
		t.Fatalf("contract option was not cloned: %q", got)
	}

	returnedBreakpoints := runner.Breakpoints()
	returnedBreakpoints[0].NodeID = "changed"
	returnedWarnings := runner.StartupWarnings()
	returnedWarnings[0].Sources[0] = "changed"
	returnedContracts := runner.NodeContracts()
	returnedContract := returnedContracts["start"]
	returnedContract.Fields[0].Path = state.Shared("changed")
	returnedContracts["start"] = returnedContract
	if runner.Breakpoints()[0].NodeID != "start" || runner.StartupWarnings()[0].Sources[0] != "source-1" || runner.NodeContracts()["start"].Fields[0].Path.String() != "shared.request.input" {
		t.Fatal("runner getter exposed mutable configuration")
	}
}

func newTestGraph(t *testing.T) *wfgraph.Graph {
	t.Helper()
	workflow := wfgraph.NewGraph(nil)
	input := node.NewUserInputNode(node.WithID("start"))
	if err := workflow.AddNode(input); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetEntryPoint(input.ID()); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.AddEdge(input.ID(), wfgraph.EndNodeRef); err != nil {
		t.Fatalf("AddEdge() error = %v", err)
	}
	return workflow
}
