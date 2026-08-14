package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

const afterNodeCommandVersion = 1

var afterNodeCommandPath = state.Internal(graphSchedulerNamespace, "after_node_command")

type afterNodeCommand struct {
	Version int             `json:"version"`
	TaskID  string          `json:"task_id"`
	NodeID  string          `json:"node_id"`
	Goto    []string        `json:"goto,omitempty"`
	Send    []afterNodeSend `json:"send,omitempty"`
	Suspend *afterNodeValue `json:"suspend,omitempty"`
	Return  *afterNodeValue `json:"return,omitempty"`
}

type afterNodeSend struct {
	Target         string      `json:"target"`
	Input          state.Patch `json:"input,omitempty"`
	CorrelationKey string      `json:"correlation_key,omitempty"`
	OrderKey       string      `json:"order_key,omitempty"`
}

type afterNodeValue struct {
	Value any `json:"value"`
}

func storeAfterNodeCommand(currentState *state.State, task GraphTask, command core.Command) error {
	if currentState == nil {
		return nil
	}
	if !hasNodeCommand(command) {
		return clearAfterNodeCommand(currentState)
	}
	record := afterNodeCommand{
		Version: afterNodeCommandVersion,
		TaskID:  task.TaskID,
		NodeID:  task.NodeID,
	}
	if len(command.Goto) > 0 {
		record.Goto = make([]string, len(command.Goto))
		for index, target := range command.Goto {
			record.Goto[index] = string(target)
		}
	}
	if len(command.Send) > 0 {
		record.Send = make([]afterNodeSend, len(command.Send))
		for index, send := range command.Send {
			record.Send[index] = afterNodeSend{
				Target:         string(send.Target),
				Input:          state.NewPatch(send.Input.Ops()...),
				CorrelationKey: send.CorrelationKey,
				OrderKey:       send.OrderKey,
			}
		}
	}
	if command.Suspend != nil {
		record.Suspend = &afterNodeValue{Value: command.Suspend.Value}
	}
	if command.Return != nil {
		record.Return = &afterNodeValue{Value: command.Return.Value}
	}
	return state.SetPath(currentState, afterNodeCommandPath.String(), record)
}

func loadAfterNodeCommand(currentState *state.State) (afterNodeCommand, bool, error) {
	value, ok := state.ReadPath(currentState, afterNodeCommandPath.String())
	if !ok {
		return afterNodeCommand{}, false, nil
	}
	switch typed := value.(type) {
	case afterNodeCommand:
		if err := validateAfterNodeCommand(typed); err != nil {
			return afterNodeCommand{}, false, err
		}
		return typed, true, nil
	case map[string]any:
		record, err := decodeAfterNodeCommand(typed)
		if err != nil {
			return afterNodeCommand{}, false, err
		}
		return record, true, nil
	default:
		return afterNodeCommand{}, false, fmt.Errorf("after-node command has invalid type %T", value)
	}
}

func clearAfterNodeCommand(currentState *state.State) error {
	if currentState == nil {
		return nil
	}
	return state.DeletePath(currentState, afterNodeCommandPath.String())
}

func decodeAfterNodeCommand(values map[string]any) (afterNodeCommand, error) {
	record := afterNodeCommand{
		Version: intValue(values["version"]),
		TaskID:  stringValue(values["task_id"]),
		NodeID:  stringValue(values["node_id"]),
		Goto:    stringValues(values["goto"]),
	}
	if rawSends, ok := values["send"]; ok {
		sends, err := decodeAfterNodeSends(rawSends)
		if err != nil {
			return afterNodeCommand{}, err
		}
		record.Send = sends
	}
	if rawSuspend, ok := values["suspend"]; ok && rawSuspend != nil {
		value, err := decodeAfterNodeValue(rawSuspend, "suspend")
		if err != nil {
			return afterNodeCommand{}, err
		}
		record.Suspend = &afterNodeValue{Value: value}
	}
	if rawReturn, ok := values["return"]; ok && rawReturn != nil {
		value, err := decodeAfterNodeValue(rawReturn, "return")
		if err != nil {
			return afterNodeCommand{}, err
		}
		record.Return = &afterNodeValue{Value: value}
	}
	if err := validateAfterNodeCommand(record); err != nil {
		return afterNodeCommand{}, err
	}
	return record, nil
}

func decodeAfterNodeSends(value any) ([]afterNodeSend, error) {
	items, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]afterNodeSend); typedOK {
			return append([]afterNodeSend(nil), typed...), nil
		}
		return nil, fmt.Errorf("after-node send command has invalid type %T", value)
	}
	sends := make([]afterNodeSend, 0, len(items))
	for index, item := range items {
		values, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("after-node send command %d has invalid type %T", index, item)
		}
		input, err := decodeAfterNodePatch(values["input"])
		if err != nil {
			return nil, fmt.Errorf("decode after-node send command %d input: %w", index, err)
		}
		sends = append(sends, afterNodeSend{
			Target:         stringValue(values["target"]),
			Input:          input,
			CorrelationKey: stringValue(values["correlation_key"]),
			OrderKey:       stringValue(values["order_key"]),
		})
	}
	return sends, nil
}

func decodeAfterNodePatch(value any) (state.Patch, error) {
	if value == nil {
		return state.Patch{}, nil
	}
	if patch, ok := value.(state.Patch); ok {
		return state.NewPatch(patch.Ops()...), nil
	}
	items, ok := value.([]any)
	if !ok {
		return state.Patch{}, fmt.Errorf("patch has invalid type %T", value)
	}
	operations := make([]state.PatchOp, 0, len(items))
	for index, item := range items {
		values, ok := item.(map[string]any)
		if !ok {
			return state.Patch{}, fmt.Errorf("patch operation %d has invalid type %T", index, item)
		}
		path, err := state.ParsePath(stringValue(values["path"]))
		if err != nil {
			return state.Patch{}, fmt.Errorf("patch operation %d path: %w", index, err)
		}
		operations = append(operations, state.PatchOp{
			Kind:    state.PatchOpKind(stringValue(values["kind"])),
			Path:    path,
			Value:   values["value"],
			Reducer: stringValue(values["reducer"]),
		})
	}
	patch := state.NewPatch(operations...)
	if issues := state.ValidatePatch(patch); len(issues) > 0 {
		return state.Patch{}, fmt.Errorf("invalid patch: %s", issues[0].Message)
	}
	return patch, nil
}

func decodeAfterNodeValue(value any, name string) (any, error) {
	values, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("after-node %s command has invalid type %T", name, value)
	}
	return values["value"], nil
}

func validateAfterNodeCommand(record afterNodeCommand) error {
	if record.Version != afterNodeCommandVersion {
		return fmt.Errorf("unsupported after-node command version %d", record.Version)
	}
	if strings.TrimSpace(record.TaskID) == "" {
		return fmt.Errorf("after-node command task id is required")
	}
	if strings.TrimSpace(record.NodeID) == "" {
		return fmt.Errorf("after-node command node id is required")
	}
	return nil
}

func (record afterNodeCommand) command() core.Command {
	command := core.Command{}
	if len(record.Goto) > 0 {
		command.Goto = make([]core.NodeRef, len(record.Goto))
		for index, target := range record.Goto {
			command.Goto[index] = core.NodeRef(target)
		}
	}
	if len(record.Send) > 0 {
		command.Send = make([]core.Send, len(record.Send))
		for index, send := range record.Send {
			command.Send[index] = core.Send{
				Target:         core.NodeRef(send.Target),
				Input:          state.NewPatch(send.Input.Ops()...),
				CorrelationKey: send.CorrelationKey,
				OrderKey:       send.OrderKey,
			}
		}
	}
	if record.Suspend != nil {
		command.Suspend = &core.SuspendRequest{Value: record.Suspend.Value}
	}
	if record.Return != nil {
		command.Return = &core.ReturnCommand{Value: record.Return.Value}
	}
	return command
}

func (r *GraphRunner) resolveAfterNodeCommand(ctx context.Context, checkpoint CheckpointRecord, parent GraphTask, currentState *state.State) ([]GraphTask, *core.SuspendRequest, bool, error) {
	record, ok, err := loadAfterNodeCommand(currentState)
	if err != nil || !ok {
		return nil, nil, ok, err
	}
	if record.TaskID != parent.TaskID || record.NodeID != parent.NodeID || record.TaskID != checkpoint.TaskID || record.NodeID != checkpoint.NodeID {
		return nil, nil, true, fmt.Errorf("after-node command source %q/%q does not match checkpoint %q/%q", record.TaskID, record.NodeID, checkpoint.TaskID, checkpoint.NodeID)
	}
	command := record.command()
	if count := nodeCommandCount(command); count != 1 {
		return nil, nil, true, fmt.Errorf("task %q at node %q restored %d control commands", parent.TaskID, parent.NodeID, count)
	}

	var tasks []GraphTask
	var suspend *core.SuspendRequest
	switch {
	case command.Return != nil:
		if err := StoreGraphReturnValue(currentState, command.Return.Value); err != nil {
			return nil, nil, true, fmt.Errorf("store restored graph return value: %w", err)
		}
		if err := ClearGraphSchedule(currentState); err != nil {
			return nil, nil, true, err
		}
	case command.Suspend != nil:
		tasks, err = r.runnerGraph().ResolveNextTasks(ctx, parent, currentState)
		if err != nil {
			return nil, nil, true, err
		}
		suspend = &core.SuspendRequest{Value: command.Suspend.Value}
	case len(command.Goto) > 0:
		tasks, err = r.resolveRestoredGoto(parent, command.Goto)
		if err != nil {
			return nil, nil, true, err
		}
	case len(command.Send) > 0:
		tasks, err = r.resolveRestoredSend(parent, command.Send, currentState)
		if err != nil {
			return nil, nil, true, err
		}
	}
	if command.Return == nil {
		schedule, _ := LoadGraphSchedule(currentState)
		if err := StoreGraphSchedule(currentState, GraphSchedule{
			NextTasks:         tasks,
			PendingFanInNodes: schedule.PendingFanInNodes,
		}); err != nil {
			return nil, nil, true, err
		}
	}
	if err := clearAfterNodeCommand(currentState); err != nil {
		return nil, nil, true, err
	}
	return CloneGraphTasks(tasks), suspend, true, nil
}

func (r *GraphRunner) resolveRestoredGoto(parent GraphTask, targets []core.NodeRef) ([]GraphTask, error) {
	resolved := map[string]struct{}{}
	for _, target := range targets {
		nodeID, err := r.runnerGraph().ResolveEdgeTarget(string(target))
		if err != nil {
			return nil, fmt.Errorf("task %q goto target %q: %w", parent.TaskID, target, err)
		}
		resolved[nodeID] = struct{}{}
	}
	nodeIDs := make([]string, 0, len(resolved))
	for nodeID := range resolved {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	tasks := make([]GraphTask, 0, len(nodeIDs))
	for order, nodeID := range nodeIDs {
		tasks = append(tasks, NewStaticGraphTask(nodeID, order))
	}
	return tasks, nil
}

func (r *GraphRunner) resolveRestoredSend(parent GraphTask, sends []core.Send, currentState *state.State) ([]GraphTask, error) {
	tasks := make([]GraphTask, 0, len(sends))
	for index, send := range sends {
		nodeID, err := r.runnerGraph().ResolveNodeID(string(send.Target))
		if err != nil {
			return nil, fmt.Errorf("task %q send target %q: %w", parent.TaskID, send.Target, err)
		}
		issues := state.ValidatePatch(send.Input)
		if contract, ok := r.nodeContracts[nodeID]; ok {
			issues = state.ValidateInputPatchByContractWithReducers(currentState, send.Input, contract, r.reducers)
		}
		if len(issues) > 0 {
			return nil, fmt.Errorf("task %q send %d input: %w", parent.TaskID, index, state.NewValidationError("send input", issues))
		}
		tasks = append(tasks, GraphTask{
			TaskID:         restoredDynamicTaskID(parent, send, index),
			NodeID:         nodeID,
			Input:          state.NewPatch(send.Input.Ops()...),
			CorrelationKey: strings.TrimSpace(send.CorrelationKey),
			OrderKey:       strings.TrimSpace(send.OrderKey),
			Order:          index,
			Dynamic:        true,
		})
	}
	sort.SliceStable(tasks, func(leftIndex, rightIndex int) bool {
		left, right := tasks[leftIndex], tasks[rightIndex]
		if left.OrderKey != right.OrderKey {
			return left.OrderKey < right.OrderKey
		}
		if left.CorrelationKey != right.CorrelationKey {
			return left.CorrelationKey < right.CorrelationKey
		}
		return left.TaskID < right.TaskID
	})
	for index := range tasks {
		tasks[index].Order = index
	}
	return tasks, nil
}

func restoredDynamicTaskID(parent GraphTask, send core.Send, index int) string {
	identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", parent.TaskID, index, send.Target, strings.TrimSpace(send.CorrelationKey), strings.TrimSpace(send.OrderKey))
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("send-%x", digest[:12])
}

func hasNodeCommand(command core.Command) bool {
	return nodeCommandCount(command) > 0
}

func nodeCommandCount(command core.Command) int {
	count := 0
	if len(command.Goto) > 0 {
		count++
	}
	if len(command.Send) > 0 {
		count++
	}
	if command.Suspend != nil {
		count++
	}
	if command.Return != nil {
		count++
	}
	return count
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, stringValue(item))
		}
		return values
	default:
		return nil
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
