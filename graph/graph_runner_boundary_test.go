package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type boundaryTestNode struct {
	id      string
	execute func()
}

type reservedContractTestNode struct {
	id string
}

func (node *boundaryTestNode) ID() string          { return node.id }
func (node *boundaryTestNode) Name() string        { return node.id }
func (node *boundaryTestNode) Description() string { return "boundary test node" }

func (node *boundaryTestNode) Execute(_ core.Context, _ *state.Access) (core.NodeResult, error) {
	if node.execute != nil {
		node.execute()
	}
	return core.Success(), nil
}
func (node *boundaryTestNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:   node.id,
		Name: node.id,
		Type: "boundary.test",
		Config: map[string]any{
			"nested": map[string]any{"value": "original"},
		},
		Policy: &dsl.ExecutionPolicy{
			Retry: &dsl.RetryPolicy{RetryableErrorClasses: []string{"transient"}, NonRetryableErrorClasses: []string{}},
		},
	}
}
func (*boundaryTestNode) Contract() state.Contract {
	return state.NewContract(state.FieldAccess{
		Path:     state.Shared("required"),
		Mode:     state.AccessRead,
		Required: true,
	})
}

func (node *reservedContractTestNode) ID() string          { return node.id }
func (node *reservedContractTestNode) Name() string        { return node.id }
func (node *reservedContractTestNode) Description() string { return "reserved contract test node" }
func (*reservedContractTestNode) Execute(_ core.Context, _ *state.Access) (core.NodeResult, error) {
	return core.Success(), nil
}
func (*reservedContractTestNode) Contract() state.Contract {
	return state.NewContract(state.FieldAccess{
		Path: state.Internal("scheduler", "state"),
		Mode: state.AccessReadWrite,
	})
}

func newBoundaryTestGraph(t *testing.T) *Graph {
	t.Helper()
	workflow := NewGraph(nil)
	if err := workflow.AddNode(&boundaryTestNode{id: "entry"}); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetEntryPoint("entry"); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.AddEdge("entry", EndNodeRef); err != nil {
		t.Fatalf("AddEdge() error = %v", err)
	}
	return workflow
}

func TestGraphRunnerRejectsReservedAndUnknownExternalState(t *testing.T) {
	t.Parallel()
	workflow := newBoundaryTestGraph(t)
	store := fruntime.NewMemoryRuntimeStore()
	runner, err := NewGraphRunner(workflow, store, store, state.NewJSONStateCodec(""), store)
	if err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}

	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{name: "reserved", input: map[string]any{
			"shared":   map[string]any{"required": "ok"},
			"internal": map[string]any{"graph_scheduler": map[string]any{"node_executions": 99}},
		}, want: `state section "internal" is reserved`},
		{name: "unknown", input: map[string]any{
			"shared": map[string]any{"required": "ok"},
			"tenant": map[string]any{"id": "tenant-1"},
		}, want: `state section "tenant" is unknown`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := runner.Start(context.Background(), state.FromMap(test.input))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Start() error = %v, want %q", err, test.want)
			}
			runs, listErr := store.ListRuns(context.Background(), fruntime.RunFilter{})
			if listErr != nil {
				t.Fatalf("ListRuns() error = %v", listErr)
			}
			if len(runs) != 0 {
				t.Fatalf("rejected input persisted %d runs", len(runs))
			}
		})
	}
}

func TestGraphRunnerRejectsOpaqueInitialStateBeforeCreatingRun(t *testing.T) {
	t.Parallel()

	executed := false
	workflow := NewGraph(nil)
	if err := workflow.AddNode(&boundaryTestNode{id: "entry", execute: func() { executed = true }}); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	if err := workflow.SetEntryPoint("entry"); err != nil {
		t.Fatalf("SetEntryPoint() error = %v", err)
	}
	if err := workflow.AddEdge("entry", EndNodeRef); err != nil {
		t.Fatalf("AddEdge() error = %v", err)
	}
	store := fruntime.NewMemoryRuntimeStore()
	runner, err := NewGraphRunner(workflow, store, store, state.NewJSONStateCodec(""), store)
	if err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}
	_, _, err = runner.Start(context.Background(), state.FromShared(map[string]any{
		"required": "ok",
		"opaque":   make(chan struct{}),
	}))
	if err == nil || !strings.Contains(err.Error(), "cannot be safely cloned") {
		t.Fatalf("Start() error = %v, want unsafe clone rejection", err)
	}
	if executed {
		t.Fatal("entry node executed with opaque initial state")
	}
	runs, listErr := store.ListRuns(context.Background(), fruntime.RunFilter{})
	if listErr != nil {
		t.Fatalf("ListRuns() error = %v", listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("opaque initial state persisted %d runs", len(runs))
	}
}

func TestAddNodeRejectsReservedProgrammaticContract(t *testing.T) {
	t.Parallel()
	workflow := NewGraph(nil)
	if err := workflow.AddNode(&reservedContractTestNode{id: "reserved"}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("AddNode() error = %v, want reserved contract error", err)
	}
	if _, exists := workflow.nodes["reserved"]; exists {
		t.Fatal("node with invalid contract was retained")
	}
}

func TestGraphRunnerValidatesInitialStateContract(t *testing.T) {
	t.Parallel()
	workflow := newBoundaryTestGraph(t)
	store := fruntime.NewMemoryRuntimeStore()
	runner, err := NewGraphRunner(workflow, store, store, state.NewJSONStateCodec(""), store)
	if err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}
	_, _, err = runner.Start(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), `shared.required`) {
		t.Fatalf("Start() error = %v, want missing initial state path", err)
	}
	runs, listErr := store.ListRuns(context.Background(), fruntime.RunFilter{})
	if listErr != nil {
		t.Fatalf("ListRuns() error = %v", listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("missing initial state persisted %d runs", len(runs))
	}
}

func TestNewGraphRunnerSealsGraphTopology(t *testing.T) {
	t.Parallel()
	workflow := newBoundaryTestGraph(t)
	store := fruntime.NewMemoryRuntimeStore()
	if _, err := NewGraphRunner(workflow, store, store, state.NewJSONStateCodec(""), store); err != nil {
		t.Fatalf("NewGraphRunner() error = %v", err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{ID: "entry", Type: "boundary.test"}); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("SetNodeSpec() error = %v, want sealed graph error", err)
	}
}

func TestGraphRunnerRejectsDeletingAnotherGraphRun(t *testing.T) {
	t.Parallel()
	store := fruntime.NewMemoryRuntimeStore()
	artifactStore := fruntime.NewMemoryArtifactStore()
	deleter := fruntime.NewRunDeletionCoordinator(store, store, store, artifactStore)
	newRunner := func(t *testing.T, graphID string) *fruntime.GraphRunner {
		t.Helper()
		runner, err := NewGraphRunner(
			newBoundaryTestGraph(t),
			store,
			store,
			state.NewJSONStateCodec(""),
			store,
			fruntime.WithArtifactStore(artifactStore),
			fruntime.WithRunDeleter(deleter),
			fruntime.WithGraphMetadata(graphID, "v1", "", "", graphID+"-session"),
		)
		if err != nil {
			t.Fatalf("NewGraphRunner(%q) error = %v", graphID, err)
		}
		return runner
	}
	owner := newRunner(t, "graph-a")
	run, _, err := owner.Start(context.Background(), state.FromShared(map[string]any{"required": "ok"}))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	other := newRunner(t, "graph-b")
	if _, err := other.DeleteRun(context.Background(), run.RunID); err == nil || !strings.Contains(err.Error(), "graph id mismatch") {
		t.Fatalf("DeleteRun() error = %v, want graph ownership rejection", err)
	}
	if _, err := store.GetRun(context.Background(), run.RunID); err != nil {
		t.Fatalf("owned run was deleted: %v", err)
	}
}

func TestSchedulerRejectsDynamicJoinTarget(t *testing.T) {
	t.Parallel()
	workflow := NewGraph(nil)
	for _, nodeID := range []string{"left", "right", "join"} {
		if err := workflow.AddNode(&boundaryTestNode{id: nodeID}); err != nil {
			t.Fatalf("AddNode(%q) error = %v", nodeID, err)
		}
	}
	if err := workflow.AddEdge("left", "join"); err != nil {
		t.Fatalf("AddEdge(left) error = %v", err)
	}
	if err := workflow.AddEdge("right", "join"); err != nil {
		t.Fatalf("AddEdge(right) error = %v", err)
	}
	runnable := newScheduledRunnable(workflow, nil, nil)
	err := runnable.validateNextTasks(context.Background(), fruntime.SchedulerConfig{}, []fruntime.GraphTask{{TaskID: "dynamic-join", NodeID: "join", Dynamic: true}}, map[string]fruntime.GraphTask{"join": fruntime.NewStaticGraphTask("join", 0)})
	if err == nil || !strings.Contains(err.Error(), "explicit barrier") {
		t.Fatalf("validateNextTasks() error = %v, want dynamic join rejection", err)
	}
}

func TestSchedulerRejectsDynamicAndStaticJoinTargetsInSameWave(t *testing.T) {
	t.Parallel()
	workflow := NewGraph(nil)
	for _, nodeID := range []string{"left", "right", "join"} {
		if err := workflow.AddNode(&boundaryTestNode{id: nodeID}); err != nil {
			t.Fatalf("AddNode(%q) error = %v", nodeID, err)
		}
	}
	if err := workflow.AddEdge("left", "join"); err != nil {
		t.Fatalf("AddEdge(left) error = %v", err)
	}
	if err := workflow.AddEdge("right", "join"); err != nil {
		t.Fatalf("AddEdge(right) error = %v", err)
	}
	runnable := newScheduledRunnable(workflow, nil, nil)
	err := runnable.validateNextTasks(context.Background(), fruntime.SchedulerConfig{}, []fruntime.GraphTask{
		fruntime.NewStaticGraphTask("join", 0),
		{TaskID: "dynamic-join", NodeID: "join", Order: 1, Dynamic: true},
	}, map[string]fruntime.GraphTask{})
	if err == nil || !strings.Contains(err.Error(), "same join node") {
		t.Fatalf("validateNextTasks() error = %v, want same-wave join rejection", err)
	}
}

func TestDynamicTaskIDSeparatesNULDelimitedKeys(t *testing.T) {
	t.Parallel()
	parent := fruntime.GraphTask{TaskID: "parent"}
	first, err := dynamicTaskID(parent, core.Send{Target: "worker", CorrelationKey: "a\x00b", OrderKey: "c"}, 0)
	if err != nil {
		t.Fatalf("dynamicTaskID(first) error = %v", err)
	}
	second, err := dynamicTaskID(parent, core.Send{Target: "worker", CorrelationKey: "a", OrderKey: "b\x00c"}, 0)
	if err != nil {
		t.Fatalf("dynamicTaskID(second) error = %v", err)
	}
	if first == second {
		t.Fatalf("dynamicTaskID() collision = %q", first)
	}
}

func TestNodeSpecsReturnsDeepClone(t *testing.T) {
	t.Parallel()
	workflow := newBoundaryTestGraph(t)
	specs := workflow.NodeSpecs()
	spec := specs["entry"]
	spec.Config["nested"].(map[string]any)["value"] = "changed"
	spec.State = map[string]dsl.StateBinding{"new": {Path: "shared.new"}}
	spec.Policy.Retry.RetryableErrorClasses[0] = "changed"
	spec.Policy.Retry.MaxAttempts = 99
	specs["entry"] = spec

	current := workflow.NodeSpecs()["entry"]
	if got := current.Config["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("nested config was aliased: %v", got)
	}
	if len(current.State) != 0 {
		t.Fatalf("state bindings were aliased: %#v", current.State)
	}
	if got := current.Policy.Retry.RetryableErrorClasses[0]; got != "transient" {
		t.Fatalf("retry classes were aliased: %q", got)
	}
	if got := current.Policy.Retry.MaxAttempts; got == 99 {
		t.Fatalf("policy pointer was aliased")
	}
	if current.Policy.Retry.NonRetryableErrorClasses == nil {
		t.Fatal("explicit empty policy classes were not preserved")
	}
}

func TestDefinitionReturnsDeepClone(t *testing.T) {
	t.Parallel()
	workflow := newBoundaryTestGraph(t)
	spec := workflow.NodeSpecs()["entry"]
	spec.State = map[string]dsl.StateBinding{}
	if err := workflow.SetNodeSpec(spec); err != nil {
		t.Fatalf("SetNodeSpec() error = %v", err)
	}

	definition, err := workflow.Definition()
	if err != nil {
		t.Fatalf("Definition() error = %v", err)
	}
	definition.Nodes[0].State["injected"] = dsl.StateBinding{Path: "shared.injected"}
	definition.Nodes[0].Policy.Retry.RetryableErrorClasses[0] = "changed"
	definition.Nodes[0].Policy.Retry.MaxAttempts = 99

	current := workflow.NodeSpecs()["entry"]
	if len(current.State) != 0 {
		t.Fatalf("definition state bindings were aliased: %#v", current.State)
	}
	if got := current.Policy.Retry.RetryableErrorClasses[0]; got != "transient" {
		t.Fatalf("definition retry classes were aliased: %q", got)
	}
	if got := current.Policy.Retry.MaxAttempts; got == 99 {
		t.Fatal("definition policy pointer was aliased")
	}
}

func TestGraphClonesNodeSpecInputs(t *testing.T) {
	t.Parallel()
	workflow := newBoundaryTestGraph(t)
	spec := dsl.GraphNodeSpec{
		ID:   "entry",
		Type: "boundary.test",
		Config: map[string]any{
			"nested": map[string]any{"value": "original"},
		},
		State: map[string]dsl.StateBinding{"value": {Path: "shared.value"}},
		Policy: &dsl.ExecutionPolicy{
			Retry: &dsl.RetryPolicy{RetryableErrorClasses: []string{"transient"}},
		},
	}
	if err := workflow.SetNodeSpec(spec); err != nil {
		t.Fatalf("SetNodeSpec() error = %v", err)
	}
	spec.Config["nested"].(map[string]any)["value"] = "changed"
	spec.State["value"] = dsl.StateBinding{Path: "shared.changed"}
	spec.Policy.Retry.RetryableErrorClasses[0] = "changed"

	stored := workflow.NodeSpecs()["entry"]
	if got := stored.Config["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("stored config was aliased: %v", got)
	}
	if got := stored.State["value"].Path; got != "shared.value" {
		t.Fatalf("stored state binding was aliased: %q", got)
	}
	if got := stored.Policy.Retry.RetryableErrorClasses[0]; got != "transient" {
		t.Fatalf("stored policy was aliased: %q", got)
	}
}
