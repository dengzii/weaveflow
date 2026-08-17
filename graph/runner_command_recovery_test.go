package graph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
	"github.com/dengzii/weaveflow/registry"
	fruntime "github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

const afterNodeCommandStatePath = "internal.graph_scheduler.after_node_command"

func TestRunnerRestoresGotoCommandFromFileCheckpoint(t *testing.T) {
	tests := []struct {
		name           string
		target         core.NodeRef
		selectedCalls  int32
		completedState bool
	}{
		{name: "selected target", target: "selected", selectedCalls: 1, completedState: true},
		{name: "end target", target: EndNodeRef},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var routerCalls atomic.Int32
			var skippedCalls atomic.Int32
			var selectedCalls atomic.Int32
			workflow := NewGraph(nil)
			mustAddResultNode(t, workflow, "router", func(core.Context, *state.Access) (core.NodeResult, error) {
				routerCalls.Add(1)
				return core.NodeResult{Command: core.Command{Goto: []core.NodeRef{test.target}}}, nil
			})
			mustAddResultNode(t, workflow, "skipped", func(core.Context, *state.Access) (core.NodeResult, error) {
				skippedCalls.Add(1)
				return core.Success(), nil
			})
			mustAddResultNode(t, workflow, "selected", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
				selectedCalls.Add(1)
				return core.Success(), access.SetAny(state.Shared("selected"), true)
			})
			if err := workflow.SetEntryPoint("router"); err != nil {
				t.Fatal(err)
			}
			for _, edge := range [][2]string{{"router", "skipped"}, {"router", "selected"}, {"skipped", EndNodeRef}, {"selected", EndNodeRef}} {
				if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
					t.Fatalf("add edge %s -> %s: %v", edge[0], edge[1], err)
				}
			}

			directory := t.TempDir()
			runner, _ := newCommandFileRunner(t, workflow, directory, fruntime.WithBreakpoints(fruntime.Breakpoint{
				ID: "after-router", NodeID: "router", Stage: string(fruntime.CheckpointAfterNode), Enabled: true,
			}))
			pausedRun, _, err := runner.Start(context.Background(), state.NewState())
			if err != nil || pausedRun.Status != fruntime.RunStatusPaused {
				t.Fatalf("Start() run = %#v, error = %v", pausedRun, err)
			}
			sourceCheckpointID := pausedRun.LastCheckpointID
			assertCheckpointStage(t, runner, sourceCheckpointID, fruntime.CheckpointAfterNode)
			if err := runner.Close(); err != nil {
				t.Fatalf("Close() error: %v", err)
			}

			restarted, _ := newCommandFileRunner(t, workflow, directory, fruntime.WithBreakpoints(fruntime.Breakpoint{
				ID: "after-router", NodeID: "router", Stage: string(fruntime.CheckpointAfterNode), Enabled: true,
			}))
			completedRun, finalState, err := restarted.Resume(context.Background(), pausedRun.RunID, nil)
			if err != nil || completedRun.Status != fruntime.RunStatusCompleted {
				t.Fatalf("Resume() run = %#v, error = %v", completedRun, err)
			}
			if routerCalls.Load() != 1 || skippedCalls.Load() != 0 || selectedCalls.Load() != test.selectedCalls {
				t.Fatalf("calls router=%d skipped=%d selected=%d", routerCalls.Load(), skippedCalls.Load(), selectedCalls.Load())
			}
			selected, _ := state.ReadPath(finalState, "shared.selected")
			if (selected == true) != test.completedState {
				t.Fatalf("selected state = %#v", selected)
			}
			assertOnlySourceCheckpointHasCommand(t, restarted, pausedRun.RunID, sourceCheckpointID)
		})
	}
}

func TestRunnerRestoresSendCommandFromFileCheckpoint(t *testing.T) {
	var mapperCalls atomic.Int32
	var workerCalls atomic.Int32
	var collectorCalls atomic.Int32
	sends := []core.Send{
		commandSend("third", 3, "2", "b"),
		commandSend("second", 2, "2", "a"),
		commandSend("first", 1, "1", "a"),
	}
	reg := builtin.NewDefaultRegistry()
	if err := reg.RegisterNodeType(registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "test"},
		Build: func(*registry.BuildContext, registry.ResolvedNodeSpec) (core.Node, error) {
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	workflow := NewGraph(reg)
	mustAddResultNode(t, workflow, "mapper", func(core.Context, *state.Access) (core.NodeResult, error) {
		mapperCalls.Add(1)
		return core.NodeResult{Command: core.Command{Send: sends}}, nil
	})
	mustAddResultNode(t, workflow, "worker", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		workerCalls.Add(1)
		item, itemOK := access.ReadAny(state.Shared("item"))
		total, totalOK := access.ReadAny(state.Shared("total"))
		if !itemOK || !totalOK {
			return core.NodeResult{}, errors.New("worker input is missing")
		}
		return core.NodeResult{Patch: state.NewPatch(state.PatchOp{
			Kind: state.OpAppend, Path: state.Shared("results"), Value: fmt.Sprintf("%s:%v", item, total),
		})}, nil
	})
	mustAddResultNode(t, workflow, "collector", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		collectorCalls.Add(1)
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
	workflow.setNodeContracts(map[string]state.Contract{
		"worker": state.NewContract(
			state.FieldAccess{Path: state.Shared("item"), Mode: state.AccessRead},
			state.FieldAccess{Path: state.Shared("total"), Mode: state.AccessReadWrite, Reducer: "sum.v1"},
			state.FieldAccess{Path: state.Shared("results"), Mode: state.AccessWrite, Merge: state.MergeAppend},
		),
	})

	directory := t.TempDir()
	runner, _ := newCommandFileRunner(t, workflow, directory, fruntime.WithBreakpoints(fruntime.Breakpoint{
		ID: "after-mapper", NodeID: "mapper", Stage: string(fruntime.CheckpointAfterNode), Enabled: true,
	}))
	pausedRun, _, err := runner.Start(context.Background(), state.FromShared(map[string]any{"total": 10}))
	if err != nil || pausedRun.Status != fruntime.RunStatusPaused {
		t.Fatalf("Start() run = %#v, error = %v", pausedRun, err)
	}
	sourceCheckpointID := pausedRun.LastCheckpointID
	originalTasks := persistedDynamicTasks(t, runner, pausedRun.RunID)
	if len(originalTasks) != 3 {
		t.Fatalf("dynamic task count = %d", len(originalTasks))
	}
	for index, want := range []string{"first", "second", "third"} {
		if originalTasks[index].Order != index || commandTaskItem(t, originalTasks[index]) != want {
			t.Fatalf("dynamic task %d = %#v", index, originalTasks[index])
		}
	}
	if err := runner.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	restarted, _ := newCommandFileRunner(t, workflow, directory, fruntime.WithBreakpoints(fruntime.Breakpoint{
		ID: "after-mapper", NodeID: "mapper", Stage: string(fruntime.CheckpointAfterNode), Enabled: true,
	}))
	completedRun, finalState, err := restarted.Resume(context.Background(), pausedRun.RunID, nil)
	if err != nil || completedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("Resume() run = %#v, error = %v", completedRun, err)
	}
	if mapperCalls.Load() != 1 || workerCalls.Load() != 3 || collectorCalls.Load() != 1 {
		t.Fatalf("calls mapper=%d worker=%d collector=%d", mapperCalls.Load(), workerCalls.Load(), collectorCalls.Load())
	}
	if results := readStringItems(t, finalState, "shared.results"); !reflect.DeepEqual(results, []string{"first:11", "second:12", "third:13"}) {
		t.Fatalf("results = %#v", results)
	}
	steps, err := restarted.ListSteps(context.Background(), pausedRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	workerTaskIDs := map[string]struct{}{}
	for _, step := range steps {
		if step.NodeID == "worker" {
			workerTaskIDs[step.TaskID] = struct{}{}
		}
	}
	for _, task := range originalTasks {
		if _, ok := workerTaskIDs[task.TaskID]; !ok {
			t.Fatalf("restored workers lost stable task id %q: %#v", task.TaskID, steps)
		}
	}
	assertOnlySourceCheckpointHasCommand(t, restarted, pausedRun.RunID, sourceCheckpointID)
}

func TestRunnerRestoresSuspendCommandOnceFromFileCheckpoint(t *testing.T) {
	var approvalCalls atomic.Int32
	var finishCalls atomic.Int32
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "approval", func(core.Context, *state.Access) (core.NodeResult, error) {
		approvalCalls.Add(1)
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

	directory := t.TempDir()
	runner, runtimeStore := newCommandFileRunner(t, workflow, directory)
	pausedRun, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil || pausedRun.Status != fruntime.RunStatusPaused {
		t.Fatalf("Start() run = %#v, error = %v", pausedRun, err)
	}
	sourceCheckpointID := afterNodeCheckpointID(t, runner, pausedRun.RunID, "approval")
	forcePausedRunCheckpoint(t, runtimeStore, pausedRun.RunID, sourceCheckpointID)
	if err := runner.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	restarted, _ := newCommandFileRunner(t, workflow, directory)
	restoredPause, _, err := restarted.Resume(context.Background(), pausedRun.RunID, nil)
	if err != nil || restoredPause.Status != fruntime.RunStatusPaused {
		t.Fatalf("first Resume() run = %#v, error = %v", restoredPause, err)
	}
	if restoredPause.LastCheckpointID == sourceCheckpointID {
		t.Fatal("restored suspend reused the command checkpoint")
	}
	assertCheckpointStage(t, restarted, restoredPause.LastCheckpointID, fruntime.CheckpointAfterWave)
	assertCheckpointHasCommand(t, restarted, restoredPause.LastCheckpointID, false)
	if err := restarted.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	restartedAgain, _ := newCommandFileRunner(t, workflow, directory)
	completedRun, _, err := restartedAgain.Resume(context.Background(), pausedRun.RunID, nil)
	if err != nil || completedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("second Resume() run = %#v, error = %v", completedRun, err)
	}
	if approvalCalls.Load() != 1 || finishCalls.Load() != 1 {
		t.Fatalf("calls approval=%d finish=%d", approvalCalls.Load(), finishCalls.Load())
	}
	assertOnlySourceCheckpointHasCommand(t, restartedAgain, pausedRun.RunID, sourceCheckpointID)
}

func TestRunnerRestoresReturnCommandFromFileCheckpoint(t *testing.T) {
	var returnCalls atomic.Int32
	var fallbackCalls atomic.Int32
	returnValue := map[string]any{"answer": "done", "approved": true}
	workflow := NewGraph(nil)
	mustAddResultNode(t, workflow, "return", func(core.Context, *state.Access) (core.NodeResult, error) {
		returnCalls.Add(1)
		return core.NodeResult{Command: core.Command{Return: &core.ReturnCommand{Value: returnValue}}}, nil
	})
	mustAddResultNode(t, workflow, "fallback", func(core.Context, *state.Access) (core.NodeResult, error) {
		fallbackCalls.Add(1)
		return core.Success(), nil
	})
	if err := workflow.SetEntryPoint("return"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("fallback"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.AddEdge("return", "fallback"); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runner, runtimeStore := newCommandFileRunner(t, workflow, directory)
	completedRun, _, err := runner.Start(context.Background(), state.NewState())
	if err != nil || completedRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("Start() run = %#v, error = %v", completedRun, err)
	}
	sourceCheckpointID := afterNodeCheckpointID(t, runner, completedRun.RunID, "return")
	forcePausedRunCheckpoint(t, runtimeStore, completedRun.RunID, sourceCheckpointID)
	if err := runner.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	restarted, _ := newCommandFileRunner(t, workflow, directory)
	restoredRun, _, err := restarted.Resume(context.Background(), completedRun.RunID, nil)
	if err != nil || restoredRun.Status != fruntime.RunStatusCompleted {
		t.Fatalf("Resume() run = %#v, error = %v", restoredRun, err)
	}
	if !reflect.DeepEqual(restoredRun.ReturnValue, returnValue) {
		t.Fatalf("return value = %#v", restoredRun.ReturnValue)
	}
	if returnCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("calls return=%d fallback=%d", returnCalls.Load(), fallbackCalls.Load())
	}
	assertOnlySourceCheckpointHasCommand(t, restarted, completedRun.RunID, sourceCheckpointID)
}

func TestRunnerFailedParallelWaveKeepsSafeCheckpointAndOwnStepErrors(t *testing.T) {
	workflow := NewGraph(nil)
	releaseFailures := make(chan struct{})
	successCommitted := make(chan struct{}, 1)
	mustAddNode(t, workflow, "router", func(context.Context, *state.Access) error { return nil })
	mustAddNode(t, workflow, "successful", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("successful"), true)
	})
	mustAddNode(t, workflow, "first", func(context.Context, *state.Access) error {
		<-releaseFailures
		return errors.New("first branch exploded")
	})
	mustAddNode(t, workflow, "second", func(context.Context, *state.Access) error {
		<-releaseFailures
		return errors.New("second branch collapsed")
	})
	if err := workflow.SetEntryPoint("router"); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"router", "successful"}, {"router", "first"}, {"router", "second"}, {"successful", EndNodeRef}, {"first", EndNodeRef}, {"second", EndNodeRef}} {
		if err := workflow.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}

	directory := t.TempDir()
	runner, _ := newCommandFileRunner(t, workflow, directory)
	ctx := fruntime.WithRunnerEventObserver(context.Background(), fruntime.EventObserverFunc(func(_ context.Context, event fruntime.Event) error {
		if event.Type == fruntime.EventNodeFinished && event.NodeID == "successful" {
			select {
			case successCommitted <- struct{}{}:
			default:
			}
		}
		return nil
	}))
	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, runErr := runner.Start(ctx, state.NewState())
		done <- runnerResult{run: run, state: finalState, err: runErr}
	}()
	select {
	case <-successCommitted:
	case <-time.After(5 * time.Second):
		t.Fatal("successful sibling did not commit")
	}
	close(releaseFailures)
	result := waitForRunnerResult(t, done)
	if result.err == nil || result.run.Status != fruntime.RunStatusFailed {
		t.Fatalf("run = %#v, error = %v", result.run, result.err)
	}
	checkpoint, err := runner.LoadCheckpointState(context.Background(), result.run.LastCheckpointID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Record.Stage != fruntime.CheckpointAfterWave {
		t.Fatalf("failed run last checkpoint = %#v, want prior after_wave", checkpoint.Record)
	}
	steps, err := runner.ListSteps(context.Background(), result.run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]fruntime.StepRecord{}
	for _, step := range steps {
		byNode[step.NodeID] = step
	}
	if !strings.Contains(byNode["first"].ErrorMessage, "first branch exploded") || strings.Contains(byNode["first"].ErrorMessage, "second branch collapsed") {
		t.Fatalf("first step error = %q", byNode["first"].ErrorMessage)
	}
	if !strings.Contains(byNode["second"].ErrorMessage, "second branch collapsed") || strings.Contains(byNode["second"].ErrorMessage, "first branch exploded") {
		t.Fatalf("second step error = %q", byNode["second"].ErrorMessage)
	}
	if byNode["successful"].Status != fruntime.StepStatusSucceeded || byNode["successful"].CheckpointAfterID == "" {
		t.Fatalf("successful step = %#v", byNode["successful"])
	}
}

func TestRunnerCancelWinsAfterNodePersistenceRace(t *testing.T) {
	workflow := NewGraph(nil)
	mustAddNode(t, workflow, "work", func(_ context.Context, access *state.Access) error {
		return access.SetAny(state.Shared("late"), true)
	})
	if err := workflow.SetEntryPoint("work"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.SetFinishPoint("work"); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	runtimeStore := mustOpenFileStore(t, directory)
	codec := newBlockingDiffCodec()
	runner := mustNewGraphRunner(t, workflow, runtimeStore.ExecutionStore(), runtimeStore.CheckpointStore(), codec, runtimeStore.EventSink(), fruntime.WithRuntimeTransactionStore(runtimeStore))
	finishedEvents := make(chan struct{}, 1)
	ctx := fruntime.WithRunnerEventObserver(context.Background(), fruntime.EventObserverFunc(func(_ context.Context, event fruntime.Event) error {
		if event.Type == fruntime.EventNodeFinished && event.NodeID == "work" {
			finishedEvents <- struct{}{}
		}
		return nil
	}))
	done := make(chan runnerResult, 1)
	go func() {
		run, finalState, runErr := runner.Start(ctx, state.NewState())
		done <- runnerResult{run: run, state: finalState, err: runErr}
	}()
	select {
	case <-codec.started:
	case <-time.After(5 * time.Second):
		t.Fatal("afterNode did not enter state diff")
	}
	runID := waitForRunID(t, runtimeStore)
	if err := runner.Cancel(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	result := waitForRunnerResult(t, done)
	if result.err != nil || result.run.Status != fruntime.RunStatusCanceled {
		t.Fatalf("run = %#v, error = %v", result.run, result.err)
	}
	close(codec.release)
	select {
	case <-finishedEvents:
		t.Fatal("late afterNode emitted node.finished after cancellation")
	case <-time.After(200 * time.Millisecond):
	}
	steps, err := runner.ListSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != fruntime.StepStatusCanceled || steps[0].CheckpointAfterID != "" {
		t.Fatalf("late afterNode overwrote canceled step: %#v", steps)
	}
}

func newCommandFileRunner(t *testing.T, workflow *Graph, directory string, options ...fruntime.GraphRunnerOption) (*fruntime.GraphRunner, *filestore.Store) {
	t.Helper()
	return mustNewFileGraphRunner(t, workflow, directory, options...)
}

func commandSend(item string, add int, correlationKey, orderKey string) core.Send {
	return core.Send{
		Target: "worker",
		Input: state.NewPatch(
			state.PatchOp{Kind: state.OpSet, Path: state.Shared("item"), Value: item},
			state.PatchOp{Kind: state.OpReduce, Path: state.Shared("total"), Reducer: "sum.v1", Value: add},
		),
		CorrelationKey: correlationKey,
		OrderKey:       orderKey,
	}
}

func commandTaskItem(t *testing.T, task fruntime.GraphTask) string {
	t.Helper()
	input, err := task.Input.ApplyWithReducers(state.FromShared(map[string]any{"total": 10}), map[string]state.Reducer{"sum.v1": state.SumReducer{}})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := state.ReadPath(input, "shared.item")
	item, _ := value.(string)
	return item
}

func afterNodeCheckpointID(t *testing.T, runner *fruntime.GraphRunner, runID, nodeID string) string {
	t.Helper()
	steps, err := runner.ListSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.NodeID == nodeID && step.CheckpointAfterID != "" {
			return step.CheckpointAfterID
		}
	}
	t.Fatalf("node %q has no after_node checkpoint: %#v", nodeID, steps)
	return ""
}

func forcePausedRunCheckpoint(t *testing.T, runtimeStore *filestore.Store, runID, checkpointID string) {
	t.Helper()
	run, err := runtimeStore.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = fruntime.RunStatusPaused
	run.LastCheckpointID = checkpointID
	run.PauseRequested = false
	run.CancelRequested = false
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.ReturnValue = nil
	run.FinishedAt = nil
	if _, err := runtimeStore.CompareAndSwapRun(context.Background(), run.Revision, run); err != nil {
		t.Fatal(err)
	}
}

func assertCheckpointStage(t *testing.T, runner *fruntime.GraphRunner, checkpointID string, stage fruntime.CheckpointStage) {
	t.Helper()
	checkpoint, err := runner.LoadCheckpointState(context.Background(), checkpointID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Record.Stage != stage {
		t.Fatalf("checkpoint %q stage = %q, want %q", checkpointID, checkpoint.Record.Stage, stage)
	}
}

func assertCheckpointHasCommand(t *testing.T, runner *fruntime.GraphRunner, checkpointID string, want bool) {
	t.Helper()
	checkpoint, err := runner.LoadCheckpointState(context.Background(), checkpointID)
	if err != nil {
		t.Fatal(err)
	}
	_, exists := state.ReadPath(checkpoint.Business, afterNodeCommandStatePath)
	if exists != want {
		t.Fatalf("checkpoint %q command marker exists = %v, want %v", checkpointID, exists, want)
	}
}

func assertOnlySourceCheckpointHasCommand(t *testing.T, runner *fruntime.GraphRunner, runID, sourceCheckpointID string) {
	t.Helper()
	checkpoints, err := runner.ListCheckpoints(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	foundSource := false
	for _, checkpoint := range checkpoints {
		want := checkpoint.CheckpointID == sourceCheckpointID
		assertCheckpointHasCommand(t, runner, checkpoint.CheckpointID, want)
		foundSource = foundSource || want
	}
	if !foundSource {
		t.Fatalf("source checkpoint %q is missing", sourceCheckpointID)
	}
}

type blockingDiffCodec struct {
	delegate *state.JSONStateCodec
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newBlockingDiffCodec() *blockingDiffCodec {
	return &blockingDiffCodec{
		delegate: state.NewJSONStateCodec(""),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (codec *blockingDiffCodec) Name() string {
	return codec.delegate.Name()
}

func (codec *blockingDiffCodec) Version() string {
	return codec.delegate.Version()
}

func (codec *blockingDiffCodec) Encode(snapshot state.Snapshot) ([]byte, error) {
	return codec.delegate.Encode(snapshot)
}

func (codec *blockingDiffCodec) Decode(data []byte) (state.Snapshot, error) {
	return codec.delegate.Decode(data)
}

func (codec *blockingDiffCodec) Diff(before, after state.Snapshot) ([]state.Change, error) {
	codec.once.Do(func() {
		close(codec.started)
		<-codec.release
	})
	return codec.delegate.Diff(before, after)
}
