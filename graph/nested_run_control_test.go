package graph

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/node"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestRunnerNestedRunInterruptPausesParentAndResumeReusesChild(t *testing.T) {
	t.Parallel()

	var approvalCalls atomic.Int32
	var finishCalls atomic.Int32
	childGraph := NewGraph(nil)
	mustAddResultNode(t, childGraph, "approval", func(core.Context, *state.Access) (core.NodeResult, error) {
		approvalCalls.Add(1)
		return core.NodeResult{Command: core.Command{Suspend: &core.SuspendRequest{Value: "approve child"}}}, nil
	})
	mustAddResultNode(t, childGraph, "finish", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		finishCalls.Add(1)
		return core.Success(), access.SetAny(state.Shared("child_done"), true)
	})
	if err := childGraph.SetEntryPoint("approval"); err != nil {
		t.Fatal(err)
	}
	if err := childGraph.SetFinishPoint("finish"); err != nil {
		t.Fatal(err)
	}
	if err := childGraph.AddEdge("approval", "finish"); err != nil {
		t.Fatal(err)
	}

	parentRunner, runtimeStore := newNestedRunParentRunner(t, childGraph)
	pausedParent, _, err := parentRunner.Start(context.Background(), nestedRunParentInput())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if pausedParent.Status != fruntime.RunStatusPaused {
		t.Fatalf("parent status = %q, want paused", pausedParent.Status)
	}
	children := listNestedRunChildren(t, runtimeStore, pausedParent.RunID)
	if len(children) != 1 {
		t.Fatalf("child runs = %#v, want one", children)
	}
	childRunID := children[0].RunID
	if children[0].Status != fruntime.RunStatusPaused || children[0].LastCheckpointID == "" {
		t.Fatalf("interrupted child = %#v", children[0])
	}
	if approvalCalls.Load() != 1 || finishCalls.Load() != 0 {
		t.Fatalf("child calls before resume: approval=%d finish=%d", approvalCalls.Load(), finishCalls.Load())
	}

	completedParent, finalState, err := parentRunner.Resume(context.Background(), pausedParent.RunID, nil)
	if err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	if completedParent.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed parent status = %q, want completed", completedParent.Status)
	}
	if approvalCalls.Load() != 1 || finishCalls.Load() != 1 {
		t.Fatalf("child calls after resume: approval=%d finish=%d", approvalCalls.Load(), finishCalls.Load())
	}
	children = listNestedRunChildren(t, runtimeStore, pausedParent.RunID)
	if len(children) != 1 || children[0].RunID != childRunID || children[0].Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed child runs = %#v, want completed %q", children, childRunID)
	}
	rawOutput, ok := state.ReadPath(finalState, "shared.child_output")
	if !ok {
		t.Fatal("parent output is missing after child resume")
	}
	exported, ok := rawOutput.(map[string]any)
	if !ok {
		t.Fatalf("parent child output type = %T", rawOutput)
	}
	shared, ok := exported[state.SectionShared].(map[string]any)
	if !ok || shared["child_done"] != true {
		t.Fatalf("parent child output = %#v", exported)
	}
}

func TestRunnerNestedRunCancelPausedParentCascadesToPausedChild(t *testing.T) {
	t.Parallel()

	childGraph := NewGraph(nil)
	mustAddResultNode(t, childGraph, "approval", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{Command: core.Command{Suspend: &core.SuspendRequest{Value: "approve child"}}}, nil
	})
	if err := childGraph.SetEntryPoint("approval"); err != nil {
		t.Fatal(err)
	}
	if err := childGraph.SetFinishPoint("approval"); err != nil {
		t.Fatal(err)
	}

	parentRunner, runtimeStore := newNestedRunParentRunner(t, childGraph)
	pausedParent, _, err := parentRunner.Start(context.Background(), nestedRunParentInput())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	children := listNestedRunChildren(t, runtimeStore, pausedParent.RunID)
	if pausedParent.Status != fruntime.RunStatusPaused || len(children) != 1 || children[0].Status != fruntime.RunStatusPaused {
		t.Fatalf("paused lineage: parent=%#v children=%#v", pausedParent, children)
	}
	childRunID := children[0].RunID
	if parentRunner.IsRunActive(pausedParent.RunID) || parentRunner.IsRunActive(childRunID) {
		t.Fatalf("paused lineage retained active execution: parent=%v child=%v", parentRunner.IsRunActive(pausedParent.RunID), parentRunner.IsRunActive(childRunID))
	}

	if err := parentRunner.Cancel(context.Background(), pausedParent.RunID); err != nil {
		t.Fatalf("Cancel(paused parent): %v", err)
	}
	for _, runID := range []string{pausedParent.RunID, childRunID} {
		canceled, err := parentRunner.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("GetRun(%q): %v", runID, err)
		}
		if canceled.Status != fruntime.RunStatusCanceled || canceled.FinishedAt == nil {
			t.Fatalf("canceled run %q = %#v", runID, canceled)
		}
		events, err := runtimeStore.ListEvents(runID)
		if err != nil {
			t.Fatalf("ListEvents(%q): %v", runID, err)
		}
		if len(events) < 2 || events[len(events)-2].Type != fruntime.EventRunCancelRequested || events[len(events)-1].Type != fruntime.EventRunCanceled {
			t.Fatalf("cancel events for %q = %#v", runID, events)
		}
	}
}

func TestRunnerNestedRunParentPauseCascadesToChild(t *testing.T) {
	t.Parallel()

	childStarted := make(chan struct{}, 1)
	childGraph := blockingNestedRunGraph(t, childStarted)
	parentRunner, runtimeStore := newNestedRunParentRunner(t, childGraph)
	completed := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := parentRunner.Start(context.Background(), nestedRunParentInput())
		completed <- runnerResult{run: run, state: finalState, err: err}
	}()
	waitForNestedChildStart(t, childStarted)
	parentRun := waitForNestedRun(t, runtimeStore, "parent", "")
	childRun := waitForNestedRun(t, runtimeStore, "child", parentRun.RunID)

	if err := parentRunner.Pause(context.Background(), parentRun.RunID); err != nil {
		t.Fatalf("Pause(parent): %v", err)
	}
	result := waitForRunnerResult(t, completed)
	if result.err != nil {
		t.Fatalf("Start() while pausing: %v", result.err)
	}
	if result.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("parent status = %q, want paused", result.run.Status)
	}
	persistedChild := waitForNestedRunStatus(t, parentRunner, childRun.RunID, fruntime.RunStatusPaused)
	if persistedChild.Status != fruntime.RunStatusPaused || persistedChild.LastCheckpointID == "" {
		t.Fatalf("cascaded child pause = %#v", persistedChild)
	}
}

func TestRunnerNestedRunParentCancelCascadesToChild(t *testing.T) {
	t.Parallel()

	childStarted := make(chan struct{}, 1)
	childGraph := blockingNestedRunGraph(t, childStarted)
	parentRunner, runtimeStore := newNestedRunParentRunner(t, childGraph)
	completed := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := parentRunner.Start(context.Background(), nestedRunParentInput())
		completed <- runnerResult{run: run, state: finalState, err: err}
	}()
	waitForNestedChildStart(t, childStarted)
	parentRun := waitForNestedRun(t, runtimeStore, "parent", "")
	childRun := waitForNestedRun(t, runtimeStore, "child", parentRun.RunID)

	if err := parentRunner.Cancel(context.Background(), parentRun.RunID); err != nil {
		t.Fatalf("Cancel(parent): %v", err)
	}
	result := waitForRunnerResult(t, completed)
	if result.err != nil {
		t.Fatalf("Start() while canceling: %v", result.err)
	}
	if result.run.Status != fruntime.RunStatusCanceled {
		t.Fatalf("parent status = %q, want canceled", result.run.Status)
	}
	persistedChild := waitForNestedRunStatus(t, parentRunner, childRun.RunID, fruntime.RunStatusCanceled)
	if persistedChild.Status != fruntime.RunStatusCanceled {
		t.Fatalf("cascaded child cancel = %#v", persistedChild)
	}
}

func newNestedRunParentRunner(t *testing.T, childGraph *Graph) (*fruntime.GraphRunner, *fruntime.MemoryRuntimeStore) {
	t.Helper()
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	codec := state.NewJSONStateCodec("")
	childRunner := mustNewGraphRunner(t, childGraph, runtimeStore, runtimeStore, codec, runtimeStore,
		fruntime.WithGraphMetadata("child", "1", "", "", "nested-test"))

	subgraphNode := node.NewSubgraphNode(node.WithID("child"))
	subgraphNode.GraphRef = "child"
	subgraphNode.InputPath = state.Shared("child_input")
	subgraphNode.OutputPath = state.Shared("child_output")
	subgraphNode.RunChild = childRunner.RunChild
	parentGraph := NewGraph(nil)
	if err := parentGraph.AddNode(subgraphNode); err != nil {
		t.Fatalf("AddNode(subgraph): %v", err)
	}
	if err := parentGraph.SetEntryPoint(subgraphNode.ID()); err != nil {
		t.Fatalf("SetEntryPoint(subgraph): %v", err)
	}
	if err := parentGraph.SetFinishPoint(subgraphNode.ID()); err != nil {
		t.Fatalf("SetFinishPoint(subgraph): %v", err)
	}
	parentRunner := mustNewGraphRunner(t, parentGraph, runtimeStore, runtimeStore, codec, runtimeStore,
		fruntime.WithGraphMetadata("parent", "1", "", "", "nested-test"))
	return parentRunner, runtimeStore
}

func blockingNestedRunGraph(t *testing.T, started chan<- struct{}) *Graph {
	t.Helper()
	childGraph := NewGraph(nil)
	mustAddResultNode(t, childGraph, "work", func(ctx core.Context, _ *state.Access) (core.NodeResult, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return core.NodeResult{}, ctx.Err()
	})
	if err := childGraph.SetEntryPoint("work"); err != nil {
		t.Fatal(err)
	}
	if err := childGraph.SetFinishPoint("work"); err != nil {
		t.Fatal(err)
	}
	return childGraph
}

func nestedRunParentInput() *state.State {
	return state.FromShared(map[string]any{"child_input": map[string]any{"value": "nested"}})
}

func listNestedRunChildren(t *testing.T, runtimeStore *fruntime.MemoryRuntimeStore, parentRunID string) []fruntime.RunRecord {
	t.Helper()
	runs, err := runtimeStore.ListRuns(context.Background(), fruntime.RunFilter{ParentRunID: parentRunID, ParentTaskID: "child"})
	if err != nil {
		t.Fatalf("ListRuns(children): %v", err)
	}
	return runs
}

func waitForNestedChildStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for nested child node")
	}
}

func waitForNestedRun(t *testing.T, runtimeStore *fruntime.MemoryRuntimeStore, graphID, parentRunID string) fruntime.RunRecord {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for graph %q run under parent %q", graphID, parentRunID)
		case <-ticker.C:
			runs, err := runtimeStore.ListRuns(context.Background(), fruntime.RunFilter{})
			if err != nil {
				t.Fatalf("ListRuns(): %v", err)
			}
			for _, run := range runs {
				if run.GraphID == graphID && run.ParentRunID == parentRunID {
					return run
				}
			}
		}
	}
}

func waitForNestedRunStatus(t *testing.T, runner *fruntime.GraphRunner, runID string, status fruntime.RunStatus) fruntime.RunRecord {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			run, _ := runner.GetRun(context.Background(), runID)
			t.Fatalf("timed out waiting for run %q status %q: %#v", runID, status, run)
		case <-ticker.C:
			run, err := runner.GetRun(context.Background(), runID)
			if err != nil {
				t.Fatalf("GetRun(%q): %v", runID, err)
			}
			if run.Status == status {
				return run
			}
		}
	}
}
