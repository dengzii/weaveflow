package graph

import (
	"context"
	"testing"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestRunnerParallelExternalCancelDoesNotWaitForUncooperativeBranches(t *testing.T) {
	workflow := NewGraph(nil)
	started := make(chan string, 2)
	exited := make(chan string, 2)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	for _, nodeID := range []string{"first", "second"} {
		currentNodeID := nodeID
		mustAddNode(t, workflow, currentNodeID, func(context.Context, *state.Access) error {
			started <- currentNodeID
			<-release
			exited <- currentNodeID
			return nil
		})
	}
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "first"}, {"router", "second"}, {"first", EndNodeRef}, {"second", EndNodeRef}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	directory := t.TempDir()
	runtimeStore, err := fruntime.NewFileRuntimeStore(directory)
	if err != nil {
		t.Fatalf("NewFileRuntimeStore(): %v", err)
	}
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, runErr := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: runErr}
	}()

	waitForBranchStarts(t, started, 2)
	runID := waitForRunID(t, runtimeStore)
	if err := runner.Cancel(context.Background(), runID); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}

	var result runnerResult
	select {
	case result = <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("parallel cancel waited for uncooperative branches")
	}
	if result.err != nil {
		t.Fatalf("Start(): %v", result.err)
	}
	if result.run.Status != fruntime.RunStatusCanceled {
		t.Fatalf("run status = %q, want canceled", result.run.Status)
	}

	close(release)
	released = true
	waitForBranchStarts(t, exited, 2)

	persistedRun, err := runner.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if persistedRun.Status != fruntime.RunStatusCanceled {
		t.Fatalf("late branch return changed run status to %q", persistedRun.Status)
	}
	steps, err := runner.ListSteps(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListSteps(): %v", err)
	}
	canceled := map[string]bool{}
	for _, step := range steps {
		if step.NodeID == "first" || step.NodeID == "second" {
			canceled[step.NodeID] = step.Status == fruntime.StepStatusCanceled && step.CheckpointAfterID == ""
		}
	}
	if !canceled["first"] || !canceled["second"] {
		t.Fatalf("parallel branch steps were not durably canceled: %#v", steps)
	}
}
