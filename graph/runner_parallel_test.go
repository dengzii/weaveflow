package graph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	langgraph "github.com/smallnest/langgraphgo/graph"
)

func TestRunnerParallelFanOutFanInRecordsBranchSteps(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "b")
	})
	mustAddNode(t, g, "collector", func(ctx context.Context, access *state.Access) error {
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector"},
		{"b", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	checkpointStore := fruntime.NewFileCheckpointStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		checkpointStore,
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	count, ok := state.NewAccess(finalState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("expected collector to see two branches, got %#v ok=%v", count, ok)
	}

	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	byNode := map[string]fruntime.StepRecord{}
	for _, step := range steps {
		byNode[step.NodeID] = step
	}
	for _, nodeID := range []string{"router", "a", "b", "collector"} {
		step, ok := byNode[nodeID]
		if !ok {
			t.Fatalf("missing step for node %q; steps=%#v", nodeID, steps)
		}
		if step.Status != fruntime.StepStatusSucceeded {
			t.Fatalf("step %q status = %q", nodeID, step.Status)
		}
		if step.CheckpointBeforeID == "" || step.CheckpointAfterID == "" {
			t.Fatalf("step %q missing before/after checkpoints: %#v", nodeID, step)
		}
	}
	if byNode["a"].WaveID == "" || byNode["a"].WaveID != byNode["b"].WaveID {
		t.Fatalf("expected branch steps to share wave id, a=%q b=%q", byNode["a"].WaveID, byNode["b"].WaveID)
	}

	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, nodeID := range []string{"a", "b"} {
		step := byNode[nodeID]
		var started, finished bool
		for _, event := range events {
			if event.NodeID != nodeID || event.StepID != step.StepID {
				continue
			}
			if event.Type == fruntime.EventNodeStarted {
				started = true
			}
			if event.Type == fruntime.EventNodeFinished {
				finished = true
			}
		}
		if !started || !finished {
			t.Fatalf("branch %q missing started/finished events for step %q: started=%v finished=%v events=%#v", nodeID, step.StepID, started, finished, events)
		}
	}

	checkpoints, err := runner.ListCheckpoints(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	if len(checkpoints) != len(steps)*2+1 {
		t.Fatalf("expected before/after checkpoint per step plus barrier, got checkpoints=%d steps=%d", len(checkpoints), len(steps))
	}
	var barrier fruntime.CheckpointRecord
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage == fruntime.CheckpointAfterParallelWave {
			barrier = checkpoint
			break
		}
	}
	if barrier.CheckpointID == "" {
		t.Fatalf("missing after_parallel_wave checkpoint: %#v", checkpoints)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), barrier.CheckpointID)
	if err != nil {
		t.Fatalf("load barrier checkpoint: %v", err)
	}
	if len(restored.Runtime.NextNodeIDs) != 1 || restored.Runtime.NextNodeIDs[0] != "collector" {
		t.Fatalf("barrier next nodes = %#v, want [collector]", restored.Runtime.NextNodeIDs)
	}
}

func TestRunnerParallelFanInWaitsForUnevenBranches(t *testing.T) {
	t.Parallel()

	var collectorCalls atomic.Int32
	g := NewGraph()
	mustAddNode(t, g, "router", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "fast", func(_ context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "fast")
	})
	mustAddNode(t, g, "slow", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "slow_tail", func(_ context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "slow")
	})
	mustAddNode(t, g, "collector", func(_ context.Context, access *state.Access) error {
		collectorCalls.Add(1)
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "fast"},
		{"router", "slow"},
		{"fast", "collector"},
		{"slow", "slow_tail"},
		{"slow_tail", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	count, ok := state.NewAccess(finalState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("collector branch count = %#v ok=%v, want 2", count, ok)
	}
	if calls := collectorCalls.Load(); calls != 1 {
		t.Fatalf("collector calls = %d, want 1", calls)
	}
	if _, ok := state.ReadPath(finalState, "internal.graph_scheduler"); ok {
		t.Fatal("final state retained graph scheduler metadata")
	}

	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	collectorSteps := 0
	for _, step := range steps {
		if step.NodeID == "collector" {
			collectorSteps++
		}
	}
	if collectorSteps != 1 {
		t.Fatalf("collector steps = %d, want 1; steps=%#v", collectorSteps, steps)
	}

	checkpoints, err := runner.ListCheckpoints(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	barrierID := ""
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage != fruntime.CheckpointAfterParallelWave {
			continue
		}
		restored, err := runner.LoadCheckpointState(context.Background(), checkpoint.CheckpointID)
		if err != nil {
			t.Fatalf("load checkpoint %q: %v", checkpoint.CheckpointID, err)
		}
		if strings.Join(restored.Runtime.CurrentNodeIDs, ",") != "fast,slow" {
			continue
		}
		if strings.Join(restored.Runtime.NextNodeIDs, ",") != "slow_tail" {
			t.Fatalf("uneven barrier next nodes = %#v, want [slow_tail]", restored.Runtime.NextNodeIDs)
		}
		_, pending, ok := fruntime.LoadGraphSchedule(restored.Business)
		if !ok || strings.Join(pending, ",") != "collector" {
			t.Fatalf("uneven barrier pending fan-in = %#v ok=%v, want [collector]", pending, ok)
		}
		barrierID = checkpoint.CheckpointID
		break
	}
	if barrierID == "" {
		t.Fatalf("missing uneven branch barrier checkpoint: %#v", checkpoints)
	}

	collectorCalls.Store(0)
	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(context.Background(), barrierID, nil)
	if err != nil {
		t.Fatalf("resume uneven barrier: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed status = %q, want completed", resumedRun.Status)
	}
	resumedCount, ok := state.NewAccess(resumedState).ReadAny(state.Shared("branch_count"))
	if !ok || resumedCount != 2 {
		t.Fatalf("resumed collector branch count = %#v ok=%v, want 2", resumedCount, ok)
	}
	if calls := collectorCalls.Load(); calls != 1 {
		t.Fatalf("collector calls after barrier resume = %d, want 1", calls)
	}
}

func TestRunnerParallelResumeFromBarrierContinuesToCollector(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "b")
	})
	mustAddNode(t, g, "collector", func(ctx context.Context, access *state.Access) error {
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector"},
		{"b", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	checkpoints, err := runner.ListCheckpoints(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	var barrierID string
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage == fruntime.CheckpointAfterParallelWave {
			barrierID = checkpoint.CheckpointID
			break
		}
	}
	if barrierID == "" {
		t.Fatalf("missing barrier checkpoint: %#v", checkpoints)
	}

	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(context.Background(), barrierID, nil)
	if err != nil {
		t.Fatalf("resume from barrier: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want completed", resumedRun.Status)
	}
	count, ok := state.NewAccess(resumedState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("expected resumed collector to see two branches, got %#v ok=%v", count, ok)
	}
}

func TestRunnerSequentialResumeFromAfterNodeStillWorks(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("a"), true)
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("b"), true)
	})
	if err := g.SetEntryPoint("a"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("b"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatalf("add a -> b: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	var afterA string
	for _, step := range steps {
		if step.NodeID == "a" {
			afterA = step.CheckpointAfterID
			break
		}
	}
	if afterA == "" {
		t.Fatalf("missing after checkpoint for a: %#v", steps)
	}

	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(context.Background(), afterA, nil)
	if err != nil {
		t.Fatalf("resume from sequential after_node: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want completed", resumedRun.Status)
	}
	value, ok := state.NewAccess(resumedState).ReadAny(state.Shared("b"))
	if !ok || value != true {
		t.Fatalf("expected resumed run to execute b, got %#v ok=%v", value, ok)
	}
}

func TestRunnerResumeFromAfterNodeUsesActualConditionalRouting(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("route"), "right")
	})
	mustAddNode(t, g, "left", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("visited"), "left")
	})
	mustAddNode(t, g, "right", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("visited"), "right")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	condition := registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(ctx context.Context, current *state.State) bool {
		value, ok := state.NewAccess(current).ReadAny(state.Shared("route"))
		return ok && value == "right"
	})
	if err := g.AddConditionalEdge("router", "right", condition); err != nil {
		t.Fatalf("add conditional edge: %v", err)
	}
	if err := g.AddEdge("router", "left"); err != nil {
		t.Fatalf("add fallback edge: %v", err)
	}
	if err := g.AddEdge("left", EndNodeRef); err != nil {
		t.Fatalf("add left -> end: %v", err)
	}
	if err := g.AddEdge("right", EndNodeRef); err != nil {
		t.Fatalf("add right -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	var afterRouter string
	for _, step := range steps {
		if step.NodeID == "router" {
			afterRouter = step.CheckpointAfterID
			break
		}
	}
	if afterRouter == "" {
		t.Fatalf("missing after checkpoint for router: %#v", steps)
	}

	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(context.Background(), afterRouter, nil)
	if err != nil {
		t.Fatalf("resume from router after_node: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want completed", resumedRun.Status)
	}
	visited, ok := state.NewAccess(resumedState).ReadAny(state.Shared("visited"))
	if !ok || visited != "right" {
		t.Fatalf("expected resumed run to route to right, got %#v ok=%v", visited, ok)
	}
}

func TestRunnerParallelFanOutToEndLeavesLastCheckpointAtBarrier(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "b")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	branches, ok := state.NewAccess(finalState).ReadAny(state.Shared("branches"))
	items, _ := branches.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected merged branches, got %#v ok=%v", branches, ok)
	}

	restored, err := runner.LoadCheckpointState(context.Background(), run.LastCheckpointID)
	if err != nil {
		t.Fatalf("load last checkpoint: %v", err)
	}
	if restored.Record.Stage != fruntime.CheckpointAfterParallelWave {
		t.Fatalf("last checkpoint stage = %q, want after_parallel_wave", restored.Record.Stage)
	}
	if len(restored.Runtime.NextNodeIDs) != 1 || restored.Runtime.NextNodeIDs[0] != "END" {
		t.Fatalf("barrier next nodes = %#v, want [END]", restored.Runtime.NextNodeIDs)
	}
}

func TestRunnerParallelBarrierNextNodeIDsUseActualConditionalRouting(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Scope("a", "route"), "right")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Scope("b", "route"), "left")
	})
	for _, nodeID := range []string{"left", "right"} {
		id := nodeID
		mustAddNode(t, g, id, func(ctx context.Context, access *state.Access) error {
			return access.AppendAny(state.Shared("visited"), id)
		})
	}
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	conditionFor := func(branchID string) registry.EdgeCondition {
		return registry.NewEdgeCondition(dsl.GraphConditionSpec{Type: "test"}, func(ctx context.Context, current *state.State) bool {
			value, ok := state.NewAccess(current).ReadAny(state.Scope(branchID, "route"))
			return ok && value == "right"
		})
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"left", EndNodeRef},
		{"right", EndNodeRef},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}
	if err := g.AddConditionalEdge("a", "right", conditionFor("a")); err != nil {
		t.Fatalf("add a conditional edge: %v", err)
	}
	if err := g.AddEdge("a", "left"); err != nil {
		t.Fatalf("add a fallback edge: %v", err)
	}
	if err := g.AddConditionalEdge("b", "right", conditionFor("b")); err != nil {
		t.Fatalf("add b conditional edge: %v", err)
	}
	if err := g.AddEdge("b", "left"); err != nil {
		t.Fatalf("add b fallback edge: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	checkpoints, err := runner.ListCheckpoints(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	var barrierID string
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage != fruntime.CheckpointAfterParallelWave {
			continue
		}
		restored, err := runner.LoadCheckpointState(context.Background(), checkpoint.CheckpointID)
		if err != nil {
			t.Fatalf("load barrier checkpoint: %v", err)
		}
		if len(restored.Runtime.CurrentNodeIDs) == 2 {
			barrierID = checkpoint.CheckpointID
			if len(restored.Runtime.NextNodeIDs) != 2 ||
				restored.Runtime.NextNodeIDs[0] != "left" ||
				restored.Runtime.NextNodeIDs[1] != "right" {
				t.Fatalf("barrier next nodes = %#v, want [left right]", restored.Runtime.NextNodeIDs)
			}
			break
		}
	}
	if barrierID == "" {
		t.Fatalf("missing branch barrier checkpoint: %#v", checkpoints)
	}

	resumedRun, resumedState, err := runner.ResumeFromCheckpoint(context.Background(), barrierID, nil)
	if err != nil {
		t.Fatalf("resume from branch barrier: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want completed", resumedRun.Status)
	}
	visited, ok := state.NewAccess(resumedState).ReadAny(state.Shared("visited"))
	items, _ := visited.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected resumed run to visit both routed collectors, got %#v ok=%v", visited, ok)
	}
}

func TestRunnerParallelBarrierNextNodeIDsAreStable(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	for _, nodeID := range []string{"a", "b", "collector_a", "collector_b"} {
		id := nodeID
		mustAddNode(t, g, id, func(ctx context.Context, access *state.Access) error {
			return access.AppendAny(state.Shared("visited"), id)
		})
	}
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector_b"},
		{"a", "collector_a"},
		{"b", "collector_a"},
		{"b", "collector_b"},
		{"collector_a", EndNodeRef},
		{"collector_b", EndNodeRef},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	checkpoints, err := runner.ListCheckpoints(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage != fruntime.CheckpointAfterParallelWave {
			continue
		}
		restored, err := runner.LoadCheckpointState(context.Background(), checkpoint.CheckpointID)
		if err != nil {
			t.Fatalf("load barrier checkpoint: %v", err)
		}
		if len(restored.Runtime.NextNodeIDs) == 2 {
			got := restored.Runtime.NextNodeIDs[0] + "," + restored.Runtime.NextNodeIDs[1]
			if got != "collector_a,collector_b" {
				t.Fatalf("barrier next nodes = %#v, want [collector_a collector_b]", restored.Runtime.NextNodeIDs)
			}
			return
		}
	}
	t.Fatalf("missing branch barrier checkpoint: %#v", checkpoints)
}

func TestRunnerRejectsParallelMergeConflict(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("answer"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("answer"), "b")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil {
		t.Fatal("expected parallel merge conflict")
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
}

func TestRunnerParallelBarrierCheckpointFailureFailsRun(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "b")
	})
	mustAddNode(t, g, "collector", func(ctx context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("collected"), true)
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector"},
		{"b", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		failBarrierCheckpointStore{inner: fruntime.NewFileCheckpointStore(dir)},
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil {
		t.Fatal("expected barrier checkpoint failure")
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if run.ErrorCode != "callback_failed" {
		t.Fatalf("run error code = %q, want callback_failed", run.ErrorCode)
	}
}

func TestRunnerRejectsAfterNodeBreakpointOnParallelBranch(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	runner.Breakpoints = []fruntime.Breakpoint{{
		ID:      "bp-after-a",
		NodeID:  "a",
		Stage:   string(fruntime.CheckpointAfterNode),
		Enabled: true,
	}}

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil {
		t.Fatal("expected after_node breakpoint configuration error")
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if run.ErrorCode != "config_failed" {
		t.Fatalf("run error code = %q, want config_failed", run.ErrorCode)
	}
}

func TestRunnerParallelBeforeBreakpointPausesWithoutBarrierFailure(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "b")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	runner.Breakpoints = []fruntime.Breakpoint{{
		ID:      "bp-before-a",
		NodeID:  "a",
		Stage:   string(fruntime.CheckpointBeforeNode),
		Enabled: true,
	}}

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start with before breakpoint: %v", err)
	}
	if run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", run.Status)
	}
	if run.ErrorCode == "callback_failed" {
		t.Fatalf("before breakpoint was reported as callback failure: %#v", run)
	}
}

func TestRunnerParallelResumeFromBeforeBreakpointPreservesSiblingOutput(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return access.AppendAny(state.Shared("branches"), "b")
	})
	mustAddNode(t, g, "collector", func(ctx context.Context, access *state.Access) error {
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector"},
		{"b", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	runner.Breakpoints = []fruntime.Breakpoint{{
		ID:      "bp-before-a",
		NodeID:  "a",
		Stage:   string(fruntime.CheckpointBeforeNode),
		Enabled: true,
	}}

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start with before breakpoint: %v", err)
	}
	if run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", run.Status)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), run.LastCheckpointID)
	if err != nil {
		t.Fatalf("load pause checkpoint: %v", err)
	}
	branches, ok := state.NewAccess(restored.Business).ReadAny(state.Shared("branches"))
	items, _ := branches.([]any)
	if !ok || len(items) != 1 || items[0] != "b" {
		t.Fatalf("expected paused checkpoint to preserve sibling b output, got %#v ok=%v", branches, ok)
	}

	resumedRun, resumedState, err := runner.Resume(context.Background(), run.RunID, nil)
	if err != nil {
		t.Fatalf("resume before breakpoint: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed status = %q, want completed", resumedRun.Status)
	}
	count, ok := state.NewAccess(resumedState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("expected collector to see both branches after resume, got %#v ok=%v", count, ok)
	}
}

func TestRunnerParallelExternalPauseStopsAtBarrier(t *testing.T) {
	t.Parallel()

	g, started, release, collectorCalls := newControlledParallelRunnerGraph(t)
	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 2)
	runID := waitForRunID(t, executionStore)
	if err := runner.Pause(context.Background(), runID); err != nil {
		t.Fatalf("pause run: %v", err)
	}
	close(release)

	res := waitForRunnerResult(t, done)
	if res.err != nil {
		t.Fatalf("runner start returned error: %v", res.err)
	}
	if res.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", res.run.Status)
	}
	if got := atomic.LoadInt32(collectorCalls); got != 0 {
		t.Fatalf("collector executed before paused barrier resume: %d", got)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), res.run.LastCheckpointID)
	if err != nil {
		t.Fatalf("load last checkpoint: %v", err)
	}
	if restored.Record.Stage != fruntime.CheckpointAfterParallelWave {
		t.Fatalf("last checkpoint stage = %q, want after_parallel_wave", restored.Record.Stage)
	}
	if len(restored.Runtime.NextNodeIDs) != 1 || restored.Runtime.NextNodeIDs[0] != "collector" {
		t.Fatalf("barrier next nodes = %#v, want [collector]", restored.Runtime.NextNodeIDs)
	}

	resumedRun, resumedState, err := runner.Resume(context.Background(), res.run.RunID, nil)
	if err != nil {
		t.Fatalf("resume paused barrier: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed status = %q, want completed", resumedRun.Status)
	}
	if got := atomic.LoadInt32(collectorCalls); got != 1 {
		t.Fatalf("collector calls after resume = %d, want 1", got)
	}
	count, ok := state.NewAccess(resumedState).ReadAny(state.Shared("branch_count"))
	if !ok || count != 2 {
		t.Fatalf("expected resumed collector to see two branches, got %#v ok=%v", count, ok)
	}
}

func TestRunnerParallelExternalCancelStopsAtBarrier(t *testing.T) {
	t.Parallel()

	g, started, release, collectorCalls := newControlledParallelRunnerGraph(t)
	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 2)
	runID := waitForRunID(t, executionStore)
	if err := runner.Cancel(context.Background(), runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	close(release)

	res := waitForRunnerResult(t, done)
	if res.err != nil {
		t.Fatalf("runner start returned error: %v", res.err)
	}
	if res.run.Status != fruntime.RunStatusCanceled {
		t.Fatalf("run status = %q, want canceled", res.run.Status)
	}
	if got := atomic.LoadInt32(collectorCalls); got != 0 {
		t.Fatalf("collector executed after canceled barrier: %d", got)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), res.run.LastCheckpointID)
	if err != nil {
		t.Fatalf("load last checkpoint: %v", err)
	}
	if restored.Record.Stage != fruntime.CheckpointAfterParallelWave {
		t.Fatalf("last checkpoint stage = %q, want after_parallel_wave", restored.Record.Stage)
	}
}

func TestRunnerExternalPauseAfterSingleNodeDoesNotComplete(t *testing.T) {
	t.Parallel()

	g, started, release := newControlledSingleNodeRunnerGraph(t)
	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 1)
	runID := waitForRunID(t, executionStore)
	if err := runner.Pause(context.Background(), runID); err != nil {
		t.Fatalf("pause run: %v", err)
	}
	close(release)

	res := waitForRunnerResult(t, done)
	if res.err != nil {
		t.Fatalf("runner start returned error: %v", res.err)
	}
	if res.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", res.run.Status)
	}
	if res.run.LastCheckpointID == "" {
		t.Fatal("paused run last checkpoint id is empty")
	}
}

func TestRunnerRejectsConcurrentResumeOfSameRun(t *testing.T) {
	t.Parallel()

	g, started, release := newControlledSingleNodeRunnerGraph(t)
	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)
	runner.Breakpoints = []fruntime.Breakpoint{{
		ID:      "bp-before-work",
		NodeID:  "work",
		Stage:   string(fruntime.CheckpointBeforeNode),
		Enabled: true,
	}}
	pausedRun, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil || pausedRun.Status != fruntime.RunStatusPaused {
		t.Fatalf("initial run = %#v, err=%v; want paused", pausedRun, err)
	}

	firstDone := make(chan runnerResult, 1)
	go func() {
		run, finalState, resumeErr := runner.Resume(context.Background(), pausedRun.RunID, nil)
		firstDone <- runnerResult{run: run, state: finalState, err: resumeErr}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for resumed node")
	}
	if _, _, err := runner.Resume(context.Background(), pausedRun.RunID, nil); !errors.Is(err, fruntime.ErrRunControlNotAllowed) {
		t.Fatalf("concurrent Resume() error = %v, want ErrRunControlNotAllowed", err)
	}
	close(release)
	resumed := waitForRunnerResult(t, firstDone)
	if resumed.err != nil || resumed.run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("first resume = %#v, err=%v; want completed", resumed.run, resumed.err)
	}
}

func TestRunnerExternalPauseCancelsActiveNodeAtBeforeCheckpoint(t *testing.T) {
	t.Parallel()

	g, started, release := newControlledSingleNodeRunnerGraph(t)
	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 1)
	runID := waitForRunID(t, executionStore)
	if err := runner.Pause(context.Background(), runID); err != nil {
		t.Fatalf("pause run: %v", err)
	}

	res := waitForRunnerResult(t, done)
	if res.err != nil {
		t.Fatalf("runner start returned error: %v", res.err)
	}
	if res.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", res.run.Status)
	}
	restored, err := runner.LoadCheckpointState(context.Background(), res.run.LastCheckpointID)
	if err != nil {
		t.Fatalf("load last checkpoint: %v", err)
	}
	if restored.Record.Stage != fruntime.CheckpointBeforeNode {
		t.Fatalf("pause checkpoint stage = %q, want before_node", restored.Record.Stage)
	}

	close(release)
	resumedRun, _, err := runner.Resume(context.Background(), res.run.RunID, nil)
	if err != nil {
		t.Fatalf("resume paused run: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want completed", resumedRun.Status)
	}
}

func TestRunnerExternalPauseAcceptsCustomCancellationError(t *testing.T) {
	t.Parallel()

	started := make(chan string, 1)
	graph := NewGraph()
	mustAddNode(t, graph, "work", func(ctx context.Context, access *state.Access) error {
		started <- "work"
		<-ctx.Done()
		return errors.New("request cancelled")
	})
	if err := graph.SetEntryPoint("work"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := graph.SetFinishPoint("work"); err != nil {
		t.Fatalf("set finish: %v", err)
	}

	baseDir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(baseDir)
	runner := NewGraphRunner(
		graph,
		executionStore,
		fruntime.NewFileCheckpointStore(baseDir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(baseDir),
	)
	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 1)
	runID := waitForRunID(t, executionStore)
	if err := runner.Pause(context.Background(), runID); err != nil {
		t.Fatalf("pause run: %v", err)
	}

	result := waitForRunnerResult(t, done)
	if result.err != nil {
		t.Fatalf("runner start returned error: %v", result.err)
	}
	if result.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", result.run.Status)
	}
	if result.run.PauseRequested || result.run.CancelRequested {
		t.Fatalf("paused run retained control flags: %#v", result.run)
	}
	if result.run.ErrorCode != "" || result.run.ErrorMessage != "" {
		t.Fatalf("paused run retained failure: %#v", result.run)
	}
}

func TestRunnerExternalPauseAfterNodeStopsBeforeNextSequentialNode(t *testing.T) {
	t.Parallel()

	g, started, release, nextCalls := newControlledSequentialRunnerGraph(t)
	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 1)
	runID := waitForRunID(t, executionStore)
	if err := runner.Pause(context.Background(), runID); err != nil {
		t.Fatalf("pause run: %v", err)
	}
	close(release)

	res := waitForRunnerResult(t, done)
	if res.err != nil {
		t.Fatalf("runner start returned error: %v", res.err)
	}
	if res.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("run status = %q, want paused", res.run.Status)
	}
	if got := atomic.LoadInt32(nextCalls); got != 0 {
		t.Fatalf("next node executed after pause: %d", got)
	}

	resumedRun, _, err := runner.Resume(context.Background(), res.run.RunID, nil)
	if err != nil {
		t.Fatalf("resume paused run: %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed run status = %q, want completed", resumedRun.Status)
	}
	if got := atomic.LoadInt32(nextCalls); got != 1 {
		t.Fatalf("next node calls after resume = %d, want 1", got)
	}
}

func TestRunnerExternalCancelAfterSingleNodeDoesNotComplete(t *testing.T) {
	t.Parallel()

	g, started, release := newControlledSingleNodeRunnerGraph(t)
	dir := t.TempDir()
	executionStore := fruntime.NewFileExecutionStore(dir)
	runner := NewGraphRunner(
		g,
		executionStore,
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		done <- runnerResult{run: run, state: finalState, err: err}
	}()

	waitForBranchStarts(t, started, 1)
	runID := waitForRunID(t, executionStore)
	if err := runner.Cancel(context.Background(), runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	close(release)

	res := waitForRunnerResult(t, done)
	if res.err != nil {
		t.Fatalf("runner start returned error: %v", res.err)
	}
	if res.run.Status != fruntime.RunStatusCanceled {
		t.Fatalf("run status = %q, want canceled", res.run.Status)
	}
}

func TestRunnerParallelMarksAllFailedActiveBranches(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		return errors.New("a failed")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		return errors.New("b failed")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil {
		t.Fatal("expected branch failure")
	}
	if run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	failed := map[string]bool{}
	for _, step := range steps {
		if step.Status == fruntime.StepStatusFailed {
			failed[step.NodeID] = true
		}
	}
	if !failed["a"] || !failed["b"] {
		t.Fatalf("expected both branch steps to fail, failed=%#v steps=%#v", failed, steps)
	}
}

func TestRunnerParallelSurfacesOriginalBranchFailure(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	mustAddNode(t, g, "router", func(context.Context, *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "success", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("result"), "ok")
	})
	mustAddNode(t, g, "failed", func(context.Context, *state.Access) error {
		return errors.New("branch failed")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "success"},
		{"router", "failed"},
		{"success", EndNodeRef},
		{"failed", EndNodeRef},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, _, err := runner.Start(context.Background(), state.NewState())
	if err == nil || !strings.Contains(err.Error(), "branch failed") {
		t.Fatalf("expected original branch failure, got %v", err)
	}
	if strings.Contains(err.Error(), "parallel state merge requires branch patches") {
		t.Fatalf("branch failure was masked by merge error: %v", err)
	}
	if run.Status != fruntime.RunStatusFailed || !strings.Contains(run.ErrorMessage, "branch failed") {
		t.Fatalf("run did not retain original branch failure: %#v", run)
	}

	steps, err := runner.ListSteps(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	byNode := map[string]fruntime.StepRecord{}
	for _, step := range steps {
		byNode[step.NodeID] = step
	}
	if byNode["success"].Status != fruntime.StepStatusSucceeded {
		t.Fatalf("successful sibling status = %q, want succeeded", byNode["success"].Status)
	}
	if byNode["failed"].Status != fruntime.StepStatusFailed || !strings.Contains(byNode["failed"].ErrorMessage, "branch failed") {
		t.Fatalf("failed branch did not retain original error: %#v", byNode["failed"])
	}
}

func TestRunnerParallelRetryDoesNotReplaySucceededSibling(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		aCalls int
		bCalls int
	)
	g := NewGraph()
	g.SetRetryPolicy(&langgraph.RetryPolicy{
		MaxRetries:      1,
		BackoffStrategy: langgraph.FixedBackoff,
		RetryableErrors: []string{"temporary"},
	})
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	mustAddNode(t, g, "a", func(ctx context.Context, access *state.Access) error {
		mu.Lock()
		defer mu.Unlock()
		aCalls++
		return access.AppendAny(state.Shared("branches"), "a")
	})
	mustAddNode(t, g, "b", func(ctx context.Context, access *state.Access) error {
		mu.Lock()
		defer mu.Unlock()
		bCalls++
		if bCalls == 1 {
			return errors.New("temporary b failure")
		}
		return access.AppendAny(state.Shared("branches"), "b")
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddEdge("router", "a"); err != nil {
		t.Fatalf("add router -> a: %v", err)
	}
	if err := g.AddEdge("router", "b"); err != nil {
		t.Fatalf("add router -> b: %v", err)
	}
	if err := g.AddEdge("a", EndNodeRef); err != nil {
		t.Fatalf("add a -> end: %v", err)
	}
	if err := g.AddEdge("b", EndNodeRef); err != nil {
		t.Fatalf("add b -> end: %v", err)
	}

	dir := t.TempDir()
	runner := NewGraphRunner(
		g,
		fruntime.NewFileExecutionStore(dir),
		fruntime.NewFileCheckpointStore(dir),
		state.NewJSONStateCodec(""),
		fruntime.NewFileEventSink(dir),
	)

	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("runner start: %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	mu.Lock()
	gotACalls, gotBCalls := aCalls, bCalls
	mu.Unlock()
	if gotACalls != 1 || gotBCalls != 2 {
		t.Fatalf("expected a once and b twice, got a=%d b=%d", gotACalls, gotBCalls)
	}
	branches, ok := state.NewAccess(finalState).ReadAny(state.Shared("branches"))
	items, _ := branches.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected two merged branches after retry, got %#v ok=%v", branches, ok)
	}
}

type failBarrierCheckpointStore struct {
	inner fruntime.CheckpointStore
}

type runnerResult struct {
	run   fruntime.RunRecord
	state *state.State
	err   error
}

func (s failBarrierCheckpointStore) Save(ctx context.Context, record fruntime.CheckpointRecord, payload []byte) error {
	if record.Stage == fruntime.CheckpointAfterParallelWave {
		return errors.New("barrier checkpoint failed")
	}
	return s.inner.Save(ctx, record, payload)
}

func (s failBarrierCheckpointStore) Load(ctx context.Context, checkpointID string) (fruntime.CheckpointRecord, []byte, error) {
	return s.inner.Load(ctx, checkpointID)
}

func (s failBarrierCheckpointStore) List(ctx context.Context, runID string) ([]fruntime.CheckpointRecord, error) {
	return s.inner.List(ctx, runID)
}

func newControlledParallelRunnerGraph(t *testing.T) (*Graph, chan string, chan struct{}, *int32) {
	t.Helper()

	started := make(chan string, 2)
	release := make(chan struct{})
	var collectorCalls int32

	g := NewGraph()
	mustAddNode(t, g, "router", func(ctx context.Context, access *state.Access) error {
		return nil
	})
	for _, nodeID := range []string{"a", "b"} {
		id := nodeID
		mustAddNode(t, g, id, func(ctx context.Context, access *state.Access) error {
			started <- id
			<-release
			return access.AppendAny(state.Shared("branches"), id)
		})
	}
	mustAddNode(t, g, "collector", func(ctx context.Context, access *state.Access) error {
		atomic.AddInt32(&collectorCalls, 1)
		value, _ := access.ReadAny(state.Shared("branches"))
		items, _ := value.([]any)
		return access.SetAny(state.Shared("branch_count"), len(items))
	})
	if err := g.SetEntryPoint("router"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("collector"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	for _, edge := range [][2]string{
		{"router", "a"},
		{"router", "b"},
		{"a", "collector"},
		{"b", "collector"},
	} {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}
	return g, started, release, &collectorCalls
}

func newControlledSingleNodeRunnerGraph(t *testing.T) (*Graph, chan string, chan struct{}) {
	t.Helper()
	g := NewGraph()
	started := make(chan string, 1)
	release := make(chan struct{})
	mustAddNode(t, g, "work", func(ctx context.Context, access *state.Access) error {
		started <- "work"
		select {
		case <-release:
			return access.SetAny(state.Shared("done"), true)
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err := g.SetEntryPoint("work"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("work"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	return g, started, release
}

func newControlledSequentialRunnerGraph(t *testing.T) (*Graph, chan string, chan struct{}, *int32) {
	t.Helper()
	g := NewGraph()
	started := make(chan string, 1)
	release := make(chan struct{})
	var nextCalls int32
	mustAddNode(t, g, "first", func(ctx context.Context, access *state.Access) error {
		started <- "first"
		select {
		case <-release:
			return access.SetAny(state.Shared("first_done"), true)
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	mustAddNode(t, g, "second", func(ctx context.Context, access *state.Access) error {
		atomic.AddInt32(&nextCalls, 1)
		return access.SetAny(state.Shared("second_done"), true)
	})
	if err := g.SetEntryPoint("first"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.SetFinishPoint("second"); err != nil {
		t.Fatalf("set finish: %v", err)
	}
	if err := g.AddEdge("first", "second"); err != nil {
		t.Fatalf("add first -> second: %v", err)
	}
	return g, started, release, &nextCalls
}

func waitForBranchStarts(t *testing.T, started <-chan string, want int) {
	t.Helper()
	seen := map[string]struct{}{}
	deadline := time.After(5 * time.Second)
	for len(seen) < want {
		select {
		case nodeID := <-started:
			seen[nodeID] = struct{}{}
		case <-deadline:
			t.Fatalf("timed out waiting for branch starts, seen=%#v", seen)
		}
	}
}

func waitForRunID(t *testing.T, store fruntime.ExecutionStore) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for run id")
		case <-ticker.C:
			runs, err := store.ListRuns(context.Background(), fruntime.RunFilter{})
			if err != nil {
				t.Fatalf("list runs: %v", err)
			}
			if len(runs) > 0 {
				return runs[0].RunID
			}
		}
	}
}

func waitForRunnerResult(t *testing.T, done <-chan runnerResult) runnerResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runner result")
	}
	return runnerResult{}
}
