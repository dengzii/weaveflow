package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/node"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

func TestRunnerDynamicSendPersistsStableOrderedTasks(t *testing.T) {
	t.Parallel()

	var mapperCalls atomic.Int32
	var workerCalls atomic.Int32
	sends := []core.Send{
		{Target: "worker", Input: sendValuePatch("third"), CorrelationKey: "2", OrderKey: "b"},
		{Target: "worker", Input: sendValuePatch("second"), CorrelationKey: "2", OrderKey: "a"},
		{Target: "worker", Input: sendValuePatch("first"), CorrelationKey: "1", OrderKey: "a"},
	}
	workflow := newDynamicSendGraph(t, sends, &mapperCalls, &workerCalls, nil, nil)
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)

	firstRun, firstState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("first Start(): %v", err)
	}
	secondRun, secondState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("second Start(): %v", err)
	}
	for label, finalState := range map[string]*state.State{"first": firstState, "second": secondState} {
		if values := readStringItems(t, finalState, "shared.results"); !reflect.DeepEqual(values, []string{"first", "second", "third"}) {
			t.Fatalf("%s run results = %#v", label, values)
		}
	}
	if calls := mapperCalls.Load(); calls != 2 {
		t.Fatalf("mapper calls = %d, want one per run", calls)
	}
	if calls := workerCalls.Load(); calls != 6 {
		t.Fatalf("worker calls = %d, want three per run", calls)
	}

	firstTasks := persistedDynamicTasks(t, runner, firstRun.RunID)
	secondTasks := persistedDynamicTasks(t, runner, secondRun.RunID)
	if len(firstTasks) != 3 || len(secondTasks) != 3 {
		t.Fatalf("persisted tasks = %d and %d, want three", len(firstTasks), len(secondTasks))
	}
	for taskIndex, wantValue := range []string{"first", "second", "third"} {
		firstTask := firstTasks[taskIndex]
		secondTask := secondTasks[taskIndex]
		if firstTask.TaskID == "" || firstTask.TaskID != secondTask.TaskID {
			t.Fatalf("task %d IDs = %q and %q, want stable identity", taskIndex, firstTask.TaskID, secondTask.TaskID)
		}
		if firstTask.Order != taskIndex || secondTask.Order != taskIndex || !firstTask.Dynamic || !secondTask.Dynamic {
			t.Fatalf("task %d ordering = %#v and %#v", taskIndex, firstTask, secondTask)
		}
		if value := taskInputValue(t, firstTask); value != wantValue {
			t.Fatalf("task %d input = %q, want %q", taskIndex, value, wantValue)
		}
	}
}

func TestGraphRunDynamicSendWithoutContractMatchesRunner(t *testing.T) {
	t.Parallel()

	sends := []core.Send{
		{Target: "worker", Input: sendValuePatch("second"), OrderKey: "b"},
		{Target: "worker", Input: sendValuePatch("first"), OrderKey: "a"},
	}
	directGraph := newDynamicSendGraph(t, sends, &atomic.Int32{}, &atomic.Int32{}, nil, nil)
	directState, err := directGraph.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Graph.Run(): %v", err)
	}

	runnerGraph := newDynamicSendGraph(t, sends, &atomic.Int32{}, &atomic.Int32{}, nil, nil)
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, runnerGraph, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	_, runnerState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("GraphRunner.Start(): %v", err)
	}

	want := []string{"first", "second"}
	if values := readStringItems(t, directState, "shared.results"); !reflect.DeepEqual(values, want) {
		t.Fatalf("direct results = %#v, want %#v", values, want)
	}
	if values := readStringItems(t, runnerState, "shared.results"); !reflect.DeepEqual(values, want) {
		t.Fatalf("runner results = %#v, want %#v", values, want)
	}
}

func TestRunnerDynamicSendPauseAtBarrierDoesNotReplaySenderOrWorkers(t *testing.T) {
	t.Parallel()

	var mapperCalls atomic.Int32
	var workerCalls atomic.Int32
	var collectorCalls atomic.Int32
	workerStarted := make(chan string, 3)
	releaseWorkers := make(chan struct{})
	sends := []core.Send{
		{Target: "worker", Input: sendValuePatch("third"), CorrelationKey: "2", OrderKey: "b"},
		{Target: "worker", Input: sendValuePatch("second"), CorrelationKey: "2", OrderKey: "a"},
		{Target: "worker", Input: sendValuePatch("first"), CorrelationKey: "1", OrderKey: "a"},
	}
	workflow := newDynamicSendGraph(t, sends, &mapperCalls, &workerCalls, &collectorCalls, func(value string) {
		workerStarted <- value
		<-releaseWorkers
	})
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)

	completed := make(chan runnerResult, 1)
	go func() {
		run, finalState, err := runner.Start(context.Background(), state.NewState())
		completed <- runnerResult{run: run, state: finalState, err: err}
	}()
	for startedCount := 0; startedCount < len(sends); startedCount++ {
		select {
		case <-workerStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for dynamic workers")
		}
	}
	runID := waitForRunID(t, runtimeStore)
	if err := runner.Pause(context.Background(), runID); err != nil {
		t.Fatalf("Pause(): %v", err)
	}
	close(releaseWorkers)

	paused := waitForRunnerResult(t, completed)
	if paused.err != nil {
		t.Fatalf("Start() while pausing: %v", paused.err)
	}
	if paused.run.Status != fruntime.RunStatusPaused {
		t.Fatalf("paused status = %q", paused.run.Status)
	}
	if mapperCalls.Load() != 1 || workerCalls.Load() != 3 || collectorCalls.Load() != 0 {
		t.Fatalf("calls before resume = mapper %d worker %d collector %d", mapperCalls.Load(), workerCalls.Load(), collectorCalls.Load())
	}
	restored, err := runner.LoadCheckpointState(context.Background(), paused.run.LastCheckpointID)
	if err != nil {
		t.Fatalf("LoadCheckpointState(): %v", err)
	}
	if restored.Record.Stage != fruntime.CheckpointAfterWave {
		t.Fatalf("pause checkpoint stage = %q, want after_wave", restored.Record.Stage)
	}
	schedule, ok := fruntime.LoadGraphSchedule(restored.Business)
	if !ok || len(schedule.NextTasks) != 1 || schedule.NextTasks[0].NodeID != "collector" || schedule.NextTasks[0].Dynamic {
		t.Fatalf("pause schedule = %#v", schedule)
	}

	resumedRun, resumedState, err := runner.Resume(context.Background(), paused.run.RunID, nil)
	if err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("resumed status = %q", resumedRun.Status)
	}
	if mapperCalls.Load() != 1 || workerCalls.Load() != 3 || collectorCalls.Load() != 1 {
		t.Fatalf("calls after resume = mapper %d worker %d collector %d", mapperCalls.Load(), workerCalls.Load(), collectorCalls.Load())
	}
	if values := readStringItems(t, resumedState, "shared.results"); !reflect.DeepEqual(values, []string{"first", "second", "third"}) {
		t.Fatalf("resumed results = %#v", values)
	}
	steps, err := runner.ListSteps(context.Background(), resumedRun.RunID)
	if err != nil {
		t.Fatalf("ListSteps(): %v", err)
	}
	workerTasks := map[string]struct{}{}
	for _, step := range steps {
		if step.NodeID == "worker" {
			workerTasks[step.TaskID] = struct{}{}
		}
	}
	if len(workerTasks) != 3 {
		t.Fatalf("worker task identities = %#v", workerTasks)
	}
}

func TestRunnerCommandGotoOverridesStaticRouting(t *testing.T) {
	t.Parallel()

	var skippedCalls atomic.Int32
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "router", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{Command: core.Command{Goto: []core.NodeRef{"selected"}}}, nil
	})
	mustAddResultNode(t, workflow, "skipped", func(core.Context, *state.Access) (core.NodeResult, error) {
		skippedCalls.Add(1)
		return core.Success(), nil
	})
	mustAddResultNode(t, workflow, "selected", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("selected"), true)
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("selected"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "skipped"}, {"skipped", "selected"}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	_, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if skippedCalls.Load() != 0 {
		t.Fatalf("static fallback executed %d times", skippedCalls.Load())
	}
	if selected, ok := state.ReadPath(finalState, "shared.selected"); !ok || selected != true {
		t.Fatalf("selected state = %#v, ok=%v", selected, ok)
	}
}

func TestRunnerSuspendCommandResumesAfterCompletedNode(t *testing.T) {
	t.Parallel()

	var suspendCalls atomic.Int32
	var finishCalls atomic.Int32
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "approval", func(core.Context, *state.Access) (core.NodeResult, error) {
		suspendCalls.Add(1)
		return core.NodeResult{Command: core.Command{Suspend: &core.SuspendRequest{Value: "approve"}}}, nil
	})
	mustAddResultNode(t, workflow, "finish", func(core.Context, *state.Access) (core.NodeResult, error) {
		finishCalls.Add(1)
		return core.Success(), nil
	})
	if err := workflow.SetEntryPoint("approval"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("finish"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("approval", "finish"); err != nil {
		t.Fatal(err)
	}
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	pausedRun, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if pausedRun.Status != fruntime.RunStatusPaused || suspendCalls.Load() != 1 || finishCalls.Load() != 0 {
		t.Fatalf("paused run = %#v; calls approval=%d finish=%d", pausedRun, suspendCalls.Load(), finishCalls.Load())
	}
	checkpoint, err := runner.LoadCheckpointState(context.Background(), pausedRun.LastCheckpointID)
	if err != nil {
		t.Fatalf("LoadCheckpointState(): %v", err)
	}
	if checkpoint.Record.Stage != fruntime.CheckpointAfterWave {
		t.Fatalf("suspend checkpoint stage = %q", checkpoint.Record.Stage)
	}
	resumedRun, _, err := runner.Resume(context.Background(), pausedRun.RunID, nil)
	if err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	if resumedRun.Status != fruntime.RunStatusCompleted || suspendCalls.Load() != 1 || finishCalls.Load() != 1 {
		t.Fatalf("resumed run = %#v; calls approval=%d finish=%d", resumedRun, suspendCalls.Load(), finishCalls.Load())
	}
}

func TestRunnerReturnPersistsValueAndFinalCheckpointIsNotResumable(t *testing.T) {
	t.Parallel()

	returnValue := map[string]any{"answer": "done", "approved": true}
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "return", func(core.Context, *state.Access) (core.NodeResult, error) {
		return core.NodeResult{
			Patch:   state.NewPatch(state.PatchOp{Kind: state.OpSet, Path: state.Shared("finished"), Value: true}),
			Command: core.Command{Return: &core.ReturnCommand{Value: returnValue}},
		}, nil
	})
	if err := workflow.SetEntryPoint("return"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("return"); err != nil {
		t.Fatal(err)
	}
	runtimeStore := fruntime.NewMemoryRuntimeStore()
	runner := mustNewGraphRunner(t, workflow, runtimeStore, runtimeStore, state.NewJSONStateCodec(""), runtimeStore)
	run, finalState, err := runner.Start(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if run.Status != fruntime.RunStatusCompleted || !reflect.DeepEqual(run.ReturnValue, returnValue) {
		t.Fatalf("completed run = %#v", run)
	}
	persistedRun, err := runner.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("GetRun(): %v", err)
	}
	if !reflect.DeepEqual(persistedRun.ReturnValue, returnValue) {
		t.Fatalf("persisted return value = %#v", persistedRun.ReturnValue)
	}
	if _, exists := state.ReadPath(finalState, "internal.graph_result"); exists {
		t.Fatal("final state retained internal return command data")
	}
	checkpoint, err := runner.LoadCheckpointState(context.Background(), run.LastCheckpointID)
	if err != nil {
		t.Fatalf("LoadCheckpointState(): %v", err)
	}
	if checkpoint.Record.Stage != fruntime.CheckpointFinal {
		t.Fatalf("last checkpoint stage = %q", checkpoint.Record.Stage)
	}
	if _, exists := state.ReadPath(checkpoint.Business, "internal.graph_result"); exists {
		t.Fatal("final checkpoint retained internal return command data")
	}
	events, err := runner.ListEvents(run.RunID)
	if err != nil {
		t.Fatalf("ListEvents(): %v", err)
	}
	foundFinished := false
	for _, event := range events {
		if event.Type != fruntime.EventRunFinished {
			continue
		}
		var payload struct {
			ReturnValue any `json:"return_value"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode run.finished payload: %v", err)
		}
		if !reflect.DeepEqual(payload.ReturnValue, returnValue) {
			t.Fatalf("run.finished return value = %#v", payload.ReturnValue)
		}
		foundFinished = true
	}
	if !foundFinished {
		t.Fatalf("events missing run.finished: %#v", events)
	}
	if _, _, err := runner.Resume(context.Background(), run.RunID, nil); err == nil {
		t.Fatal("Resume() accepted final checkpoint")
	}
	if _, _, err := runner.ResumeFromCheckpoint(context.Background(), run.LastCheckpointID, nil); err == nil {
		t.Fatal("ResumeFromCheckpoint() accepted final checkpoint")
	}
}

func newDynamicSendGraph(t *testing.T, sends []core.Send, mapperCalls, workerCalls, collectorCalls *atomic.Int32, beforeWorkerReturn func(string)) *Graph {
	t.Helper()
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "mapper", func(core.Context, *state.Access) (core.NodeResult, error) {
		mapperCalls.Add(1)
		return core.NodeResult{Command: core.Command{Send: sends}}, nil
	})
	mustAddResultNode(t, workflow, "worker", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		workerCalls.Add(1)
		value, ok := access.ReadAny(state.Shared("item"))
		if !ok {
			return core.NodeResult{}, fmt.Errorf("dynamic worker input is missing")
		}
		text, ok := value.(string)
		if !ok {
			return core.NodeResult{}, fmt.Errorf("dynamic worker input type = %T", value)
		}
		if beforeWorkerReturn != nil {
			beforeWorkerReturn(text)
		}
		return core.NodeResult{Patch: state.NewPatch(state.PatchOp{Kind: state.OpAppend, Path: state.Shared("results"), Value: text})}, nil
	})
	mustAddResultNode(t, workflow, "collector", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		if collectorCalls != nil {
			collectorCalls.Add(1)
		}
		return core.Success(), access.SetAny(state.Shared("collected"), true)
	})
	if err := workflow.SetEntryPoint("mapper"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("collector"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"mapper", "worker"}, {"worker", "collector"}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	return workflow
}

func mustAddResultNode(t *testing.T, workflow *Graph, nodeID string, execute node.ExecuteFunc) {
	t.Helper()
	if err := workflow.AddNode(node.NewFuncNode(node.Spec{ID: nodeID, Name: nodeID}, execute)); err != nil {
		t.Fatalf("AddNode(%q): %v", nodeID, err)
	}
	if err := workflow.SetNodeSpec(dsl.GraphNodeSpec{ID: nodeID, Type: "test", Name: nodeID}); err != nil {
		t.Fatalf("SetNodeSpec(%q): %v", nodeID, err)
	}
}

func sendValuePatch(value string) state.Patch {
	return state.NewPatch(state.PatchOp{Kind: state.OpSet, Path: state.Shared("item"), Value: value})
}

func persistedDynamicTasks(t *testing.T, runner *fruntime.GraphRunner, runID string) []fruntime.GraphTask {
	t.Helper()
	checkpoints, err := runner.ListCheckpoints(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListCheckpoints(%q): %v", runID, err)
	}
	for _, checkpoint := range checkpoints {
		if checkpoint.Stage != fruntime.CheckpointAfterWave {
			continue
		}
		restored, err := runner.LoadCheckpointState(context.Background(), checkpoint.CheckpointID)
		if err != nil {
			t.Fatalf("LoadCheckpointState(%q): %v", checkpoint.CheckpointID, err)
		}
		schedule, ok := fruntime.LoadGraphSchedule(restored.Business)
		if !ok || len(schedule.NextTasks) == 0 {
			continue
		}
		allDynamic := true
		for _, task := range schedule.NextTasks {
			allDynamic = allDynamic && task.Dynamic && task.NodeID == "worker"
		}
		if allDynamic {
			return schedule.NextTasks
		}
	}
	t.Fatalf("run %q has no persisted dynamic task wave", runID)
	return nil
}

func taskInputValue(t *testing.T, task fruntime.GraphTask) string {
	t.Helper()
	inputState, err := task.Input.Apply(state.NewState())
	if err != nil {
		t.Fatalf("apply task input: %v", err)
	}
	value, ok := state.ReadPath(inputState, "shared.item")
	if !ok {
		t.Fatalf("task input is missing: %#v", task)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("task input type = %T", value)
	}
	return text
}

func readStringItems(t *testing.T, currentState *state.State, path string) []string {
	t.Helper()
	value, ok := state.ReadPath(currentState, path)
	if !ok {
		t.Fatalf("state path %q is missing", path)
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("state path %q type = %T", path, value)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("state path %q item type = %T", path, item)
		}
		result = append(result, text)
	}
	return result
}
