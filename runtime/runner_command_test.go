package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

func TestAfterNodeCommandRoundTripsThroughSnapshot(t *testing.T) {
	current := state.NewState()
	task := GraphTask{TaskID: "task-1", NodeID: "planner"}
	command := core.Command{
		Goto: []core.NodeRef{"review", "finish"},
		Send: []core.Send{{
			Target: "worker",
			Input: state.NewPatch(state.PatchOp{
				Kind:  state.OpSet,
				Path:  state.Shared("request"),
				Value: map[string]any{"input": "hello"},
			}),
			CorrelationKey: "correlation-1",
			OrderKey:       "order-1",
		}},
		Suspend: &core.SuspendRequest{Value: map[string]any{"question": "continue?"}},
		Return:  &core.ReturnCommand{Value: "done"},
	}
	if err := storeAfterNodeCommand(current, task, command); err != nil {
		t.Fatalf("storeAfterNodeCommand() error = %v", err)
	}
	snapshot, err := state.SnapshotFromState(current)
	if err != nil {
		t.Fatalf("SnapshotFromState() error = %v", err)
	}
	restored, err := state.FromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("FromSnapshot() error = %v", err)
	}
	record, ok, err := loadAfterNodeCommand(restored)
	if err != nil || !ok {
		t.Fatalf("loadAfterNodeCommand() = %#v, %v, %v", record, ok, err)
	}
	roundTrip := record.command()
	if !reflect.DeepEqual(roundTrip.Goto, command.Goto) || len(roundTrip.Send) != 1 || roundTrip.Send[0].Target != "worker" || roundTrip.Send[0].CorrelationKey != "correlation-1" || roundTrip.Suspend == nil || roundTrip.Return == nil {
		t.Fatalf("round-trip command = %#v", roundTrip)
	}
	operations := roundTrip.Send[0].Input.Ops()
	if len(operations) != 1 || operations[0].Path.String() != "shared.request" {
		t.Fatalf("round-trip patch = %#v", operations)
	}

	if err := storeAfterNodeCommand(restored, task, core.Command{}); err != nil {
		t.Fatalf("clear command error = %v", err)
	}
	if _, ok, err := loadAfterNodeCommand(restored); err != nil || ok {
		t.Fatalf("cleared command = found %v, error %v", ok, err)
	}
	if err := clearAfterNodeCommand(nil); err != nil {
		t.Fatalf("clearAfterNodeCommand(nil) error = %v", err)
	}
}

func TestDecodeAfterNodeCommandRejectsCorruptedMetadata(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{"version": float64(afterNodeCommandVersion), "task_id": "task", "node_id": "node"}
	}
	tests := []struct {
		name     string
		mutate   func(map[string]any)
		contains string
	}{
		{name: "version", mutate: func(values map[string]any) { values["version"] = 99 }, contains: "unsupported"},
		{name: "task", mutate: func(values map[string]any) { values["task_id"] = " " }, contains: "task id is required"},
		{name: "node", mutate: func(values map[string]any) { values["node_id"] = "" }, contains: "node id is required"},
		{name: "send type", mutate: func(values map[string]any) { values["send"] = "bad" }, contains: "send command has invalid type"},
		{name: "send item", mutate: func(values map[string]any) { values["send"] = []any{"bad"} }, contains: "send command 0 has invalid type"},
		{name: "patch type", mutate: func(values map[string]any) {
			values["send"] = []any{map[string]any{"target": "worker", "input": "bad"}}
		}, contains: "patch has invalid type"},
		{name: "patch operation", mutate: func(values map[string]any) {
			values["send"] = []any{map[string]any{"target": "worker", "input": []any{"bad"}}}
		}, contains: "patch operation 0 has invalid type"},
		{name: "patch path", mutate: func(values map[string]any) {
			values["send"] = []any{map[string]any{"target": "worker", "input": []any{map[string]any{"kind": "set", "path": "unknown.path"}}}}
		}, contains: "patch operation 0 path"},
		{name: "patch validation", mutate: func(values map[string]any) {
			values["send"] = []any{map[string]any{"target": "worker", "input": []any{map[string]any{"kind": "unknown", "path": "shared.value"}}}}
		}, contains: "invalid patch"},
		{name: "suspend", mutate: func(values map[string]any) { values["suspend"] = "bad" }, contains: "suspend command has invalid type"},
		{name: "return", mutate: func(values map[string]any) { values["return"] = "bad" }, contains: "return command has invalid type"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			values := valid()
			testCase.mutate(values)
			_, err := decodeAfterNodeCommand(values)
			if err == nil || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("decodeAfterNodeCommand() error = %v, want %q", err, testCase.contains)
			}
		})
	}
	current := state.NewState()
	if err := state.SetPath(current, afterNodeCommandPath.String(), "invalid"); err != nil {
		t.Fatalf("seed invalid command: %v", err)
	}
	if _, _, err := loadAfterNodeCommand(current); err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("loadAfterNodeCommand() error = %v", err)
	}
}

func TestResolveAfterNodeCommandRestoresGotoSchedule(t *testing.T) {
	runner := &GraphRunner{graph: &blockingChildRunGraph{}}
	current := state.NewState()
	pending := GraphTask{TaskID: "pending", NodeID: "join"}
	if err := StoreGraphSchedule(current, GraphSchedule{PendingFanInTasks: []GraphTask{pending}}); err != nil {
		t.Fatalf("StoreGraphSchedule() error = %v", err)
	}
	parent := GraphTask{TaskID: "task-1", NodeID: "planner"}
	if err := storeAfterNodeCommand(current, parent, core.Command{Goto: []core.NodeRef{"zeta", "alpha", "zeta"}}); err != nil {
		t.Fatalf("storeAfterNodeCommand() error = %v", err)
	}
	tasks, suspend, found, err := runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{TaskID: parent.TaskID, NodeID: parent.NodeID}, parent, current)
	if err != nil || !found || suspend != nil {
		t.Fatalf("resolveAfterNodeCommand() = %#v, %#v, %v, %v", tasks, suspend, found, err)
	}
	if len(tasks) != 2 || tasks[0].NodeID != "alpha" || tasks[1].NodeID != "zeta" {
		t.Fatalf("restored goto tasks = %#v", tasks)
	}
	schedule, ok, err := LoadGraphSchedule(current)
	if err != nil || !ok || len(schedule.NextTasks) != 2 || len(schedule.PendingFanInTasks) != 1 || schedule.PendingFanInTasks[0].TaskID != "pending" {
		t.Fatalf("restored schedule = %#v, %v, %v", schedule, ok, err)
	}
	if _, ok, err := loadAfterNodeCommand(current); err != nil || ok {
		t.Fatalf("restored command was not cleared: found %v, error %v", ok, err)
	}
}

func TestResolveAfterNodeCommandRestoresSendSuspendAndReturn(t *testing.T) {
	runner := &GraphRunner{graph: &blockingChildRunGraph{}}
	parent := GraphTask{TaskID: "parent-task", NodeID: "planner"}
	input := state.NewPatch(state.PatchOp{Kind: state.OpSet, Path: state.Shared("input"), Value: "hello"})
	sends := []core.Send{
		{Target: "worker-b", Input: input, CorrelationKey: "b", OrderKey: "2"},
		{Target: "worker-a", Input: input, CorrelationKey: "a", OrderKey: "1"},
	}
	current := state.NewState()
	if err := StoreGraphSchedule(current, GraphSchedule{}); err != nil {
		t.Fatalf("StoreGraphSchedule() error = %v", err)
	}
	if err := storeAfterNodeCommand(current, parent, core.Command{Send: sends}); err != nil {
		t.Fatalf("store send command: %v", err)
	}
	tasks, _, found, err := runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{TaskID: parent.TaskID, NodeID: parent.NodeID}, parent, current)
	if err != nil || !found || len(tasks) != 2 || tasks[0].NodeID != "worker-a" || tasks[1].NodeID != "worker-b" || !tasks[0].Dynamic || tasks[0].TaskID == tasks[1].TaskID {
		t.Fatalf("restored send tasks = %#v, found %v, error %v", tasks, found, err)
	}
	firstID, err := restoredDynamicTaskID(parent, sends[0], 0)
	if err != nil {
		t.Fatalf("restoredDynamicTaskID() error = %v", err)
	}
	secondID, err := restoredDynamicTaskID(parent, sends[0], 0)
	if err != nil || firstID != secondID || !strings.HasPrefix(firstID, "send-") {
		t.Fatalf("dynamic task IDs = %q, %q, %v", firstID, secondID, err)
	}

	suspendState := state.NewState()
	if err := StoreGraphSchedule(suspendState, GraphSchedule{}); err != nil {
		t.Fatalf("StoreGraphSchedule() error = %v", err)
	}
	if err := storeAfterNodeCommand(suspendState, parent, core.Command{Suspend: &core.SuspendRequest{Value: "question"}}); err != nil {
		t.Fatalf("store suspend command: %v", err)
	}
	_, suspend, found, err := runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{TaskID: parent.TaskID, NodeID: parent.NodeID}, parent, suspendState)
	if err != nil || !found || suspend == nil || suspend.Value != "question" {
		t.Fatalf("restored suspend = %#v, found %v, error %v", suspend, found, err)
	}

	returnState := state.NewState()
	if err := StoreGraphSchedule(returnState, GraphSchedule{NextTasks: []GraphTask{{TaskID: "old", NodeID: "old"}}}); err != nil {
		t.Fatalf("StoreGraphSchedule() error = %v", err)
	}
	if err := storeAfterNodeCommand(returnState, parent, core.Command{Return: &core.ReturnCommand{Value: map[string]any{"answer": "done"}}}); err != nil {
		t.Fatalf("store return command: %v", err)
	}
	_, _, found, err = runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{TaskID: parent.TaskID, NodeID: parent.NodeID}, parent, returnState)
	if err != nil || !found {
		t.Fatalf("restore return command: found %v, error %v", found, err)
	}
	value, ok := LoadGraphReturnValue(returnState)
	if !ok || !reflect.DeepEqual(value, map[string]any{"answer": "done"}) {
		t.Fatalf("graph return value = %#v, found %v", value, ok)
	}
	if _, ok, err := LoadGraphSchedule(returnState); err != nil || ok {
		t.Fatalf("return command retained graph schedule: found %v, error %v", ok, err)
	}
}

func TestResolveAfterNodeCommandRejectsMismatchedAndAmbiguousSources(t *testing.T) {
	runner := &GraphRunner{graph: &blockingChildRunGraph{}}
	parent := GraphTask{TaskID: "task", NodeID: "node"}
	current := state.NewState()
	if err := storeAfterNodeCommand(current, parent, core.Command{Goto: []core.NodeRef{"next"}}); err != nil {
		t.Fatalf("store command: %v", err)
	}
	if _, _, found, err := runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{TaskID: "other", NodeID: "node"}, parent, current); err == nil || !found || !strings.Contains(err.Error(), "does not match checkpoint") {
		t.Fatalf("mismatched command = found %v, error %v", found, err)
	}

	ambiguous := state.NewState()
	if err := storeAfterNodeCommand(ambiguous, parent, core.Command{Goto: []core.NodeRef{"next"}, Return: &core.ReturnCommand{Value: "done"}}); err != nil {
		t.Fatalf("store ambiguous command: %v", err)
	}
	if _, _, found, err := runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{TaskID: parent.TaskID, NodeID: parent.NodeID}, parent, ambiguous); err == nil || !found || !strings.Contains(err.Error(), "restored 2 control commands") {
		t.Fatalf("ambiguous command = found %v, error %v", found, err)
	}

	if tasks, suspend, found, err := runner.resolveAfterNodeCommand(context.Background(), CheckpointRecord{}, parent, state.NewState()); err != nil || found || tasks != nil || suspend != nil {
		t.Fatalf("missing command = %#v, %#v, %v, %v", tasks, suspend, found, err)
	}
	if nodeCommandCount(core.Command{}) != 0 || !hasNodeCommand(core.Command{Return: &core.ReturnCommand{}}) || nodeCommandCount(core.Command{Goto: []core.NodeRef{"x"}, Suspend: &core.SuspendRequest{}}) != 2 {
		t.Fatal("node command counting is incorrect")
	}
}
