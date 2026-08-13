package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

const (
	DefaultGraphVersion = "1.0"
	EndNodeID           = "END"
)

type RunnerExecution interface {
	PrepareNode(ctx context.Context, task GraphTask, state *state.State) (context.Context, error)
	ExecuteNode(ctx context.Context, task GraphTask, executor core.Node, base *state.State, input *state.State) (core.ExecutionResult, error)
	OnGraphStep(ctx context.Context, completed []GraphTask, state *state.State) error
}

type BranchPatchRecorder interface {
	RecordBranchPatch(base *state.State, task GraphTask, patch state.Patch)
}

type ParallelWaveRecorder interface {
	OnParallelWave(ctx context.Context, base *state.State, tasks []GraphTask) error
}

type FailureRouteRecorder interface {
	OnFailureRouted(ctx context.Context, source GraphTask, err error, next []GraphTask) error
}

type BranchPatchRecorderSetter interface {
	SetBranchPatchRecorder(recorder BranchPatchRecorder)
}

type SchedulerConfig struct {
	StartTasks            []GraphTask
	InterruptAfterNodeIDs []string
	StepObserver          func(context.Context, []GraphTask, *state.State) error
	EventObserver         func(context.Context, SchedulerEvent) error
}

type GraphTask struct {
	TaskID           string          `json:"task_id"`
	NodeID           string          `json:"node_id"`
	Input            state.Patch     `json:"input,omitempty"`
	CorrelationKey   string          `json:"correlation_key,omitempty"`
	OrderKey         string          `json:"order_key,omitempty"`
	Order            int             `json:"order"`
	Dynamic          bool            `json:"dynamic,omitempty"`
	ParallelWaveSize int             `json:"parallel_wave_size,omitempty"`
	Failure          *FailureContext `json:"failure,omitempty"`
}

type FailureContext core.FailureContext

type GraphSchedule struct {
	CurrentTasks      []GraphTask `json:"current_tasks,omitempty"`
	NextTasks         []GraphTask `json:"next_tasks,omitempty"`
	PendingFanInNodes []string    `json:"pending_fan_in_nodes,omitempty"`
}

func NewStaticGraphTask(nodeID string, order int) GraphTask {
	return GraphTask{TaskID: nodeID, NodeID: nodeID, Order: order}
}

func GraphTaskNodeIDs(tasks []GraphTask) []string {
	if len(tasks) == 0 {
		return nil
	}
	nodeIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.NodeID != "" {
			nodeIDs = append(nodeIDs, task.NodeID)
		}
	}
	return nodeIDs
}

func CloneGraphTasks(tasks []GraphTask) []GraphTask {
	if len(tasks) == 0 {
		return nil
	}
	cloned := make([]GraphTask, len(tasks))
	for index, task := range tasks {
		cloned[index] = task
		cloned[index].Input = state.NewPatch(task.Input.Ops()...)
		if task.Failure != nil {
			failure := *task.Failure
			if len(task.Failure.Details) > 0 {
				failure.Details = cloneFailureDetails(task.Failure.Details)
			}
			cloned[index].Failure = &failure
		}
	}
	return cloned
}

func cloneFailureDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}

type SchedulerEventType string

const (
	SchedulerEventLimitExceeded   SchedulerEventType = "limit_exceeded"
	SchedulerEventRetryScheduled  SchedulerEventType = "retry_scheduled"
	SchedulerEventConditionFailed SchedulerEventType = "condition_failed"
	SchedulerEventRouteDecision   SchedulerEventType = "route_decision"
	SchedulerEventFailureRouted   SchedulerEventType = "failure_routed"
	SchedulerEventBackpressure    SchedulerEventType = "backpressure"
)

type SchedulerEvent struct {
	Type    SchedulerEventType
	NodeID  string
	Payload map[string]any
}

type RunnerRunnable interface {
	InvokeWithConfig(ctx context.Context, initialState *state.State, config SchedulerConfig) (*state.State, error)
}

type RunnerGraph interface {
	Validate() error
	EntryPointID() string
	CompileForRunner(execution RunnerExecution) (RunnerRunnable, error)
	ResolveNodeID(nodeID string) (string, error)
	ResolveNextNodes(ctx context.Context, currentNodeID string, state *state.State) ([]string, error)
	ResolveNextNode(ctx context.Context, currentNodeID string, state *state.State) (string, error)
	ResolveNextTasks(ctx context.Context, parent GraphTask, state *state.State) ([]GraphTask, error)
	ResolveFailure(ctx context.Context, task GraphTask, stage string, err error) ([]GraphTask, error)
	IsParallelBranchTarget(nodeID string) bool
	NodeName(nodeID string) string
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}

type GraphInterrupt struct {
	NodeID          string
	TaskID          string
	State           *state.State
	Value           any
	NextTasks       []GraphTask
	CheckpointStage CheckpointStage
}

func (interrupt *GraphInterrupt) Error() string {
	if interrupt == nil {
		return "graph interrupted"
	}
	if interrupt.Value != nil {
		return fmt.Sprintf("graph interrupted at node %s: %v", interrupt.NodeID, interrupt.Value)
	}
	return fmt.Sprintf("graph interrupted at node %s", interrupt.NodeID)
}

type GraphStepError struct {
	Err error
}

func (graphErr *GraphStepError) Error() string {
	if graphErr == nil || graphErr.Err == nil {
		return "graph step failed"
	}
	return graphErr.Err.Error()
}

func (graphErr *GraphStepError) Unwrap() error {
	if graphErr == nil {
		return nil
	}
	return graphErr.Err
}

const graphSchedulerNamespace = "graph_scheduler"
const graphResultNamespace = "graph_result"

var (
	graphSchedulerPendingPath = state.Internal(graphSchedulerNamespace, "pending_fan_in_nodes")
	graphSchedulerCurrentPath = state.Internal(graphSchedulerNamespace, "current_tasks")
	graphSchedulerNextPath    = state.Internal(graphSchedulerNamespace, "next_tasks")
	graphSchedulerStepsPath   = state.Internal(graphSchedulerNamespace, "super_steps")
	graphSchedulerNodesPath   = state.Internal(graphSchedulerNamespace, "node_executions")
	graphSchedulerElapsedPath = state.Internal(graphSchedulerNamespace, "elapsed_wall_time_ns")
	graphReturnValuePath      = state.Internal(graphResultNamespace, "return_value")
)

type GraphExecutionBudget struct {
	SuperSteps      int64
	NodeExecutions  int64
	ElapsedWallTime time.Duration
}

func StoreGraphSchedule(currentState *state.State, schedule GraphSchedule) error {
	if currentState == nil {
		return nil
	}
	for _, item := range []struct {
		path  state.Path
		tasks []GraphTask
	}{
		{path: graphSchedulerCurrentPath, tasks: schedule.CurrentTasks},
		{path: graphSchedulerNextPath, tasks: schedule.NextTasks},
	} {
		if len(item.tasks) == 0 {
			if err := state.DeletePath(currentState, item.path.String()); err != nil {
				return err
			}
			continue
		}
		if err := state.SetPath(currentState, item.path.String(), CloneGraphTasks(item.tasks)); err != nil {
			return err
		}
	}
	if len(schedule.PendingFanInNodes) == 0 {
		return state.DeletePath(currentState, graphSchedulerPendingPath.String())
	}
	return state.SetPath(currentState, graphSchedulerPendingPath.String(), append([]string(nil), schedule.PendingFanInNodes...))
}

func LoadGraphSchedule(currentState *state.State) (GraphSchedule, bool) {
	if currentState == nil {
		return GraphSchedule{}, false
	}
	access := state.NewAccess(currentState)
	currentValue, currentOK := access.ReadAny(graphSchedulerCurrentPath)
	nextValue, nextOK := access.ReadAny(graphSchedulerNextPath)
	pendingValue, pendingOK := access.ReadAny(graphSchedulerPendingPath)
	return GraphSchedule{
		CurrentTasks:      graphScheduleTasks(currentValue),
		NextTasks:         graphScheduleTasks(nextValue),
		PendingFanInNodes: graphScheduleNodeIDs(pendingValue),
	}, currentOK || nextOK || pendingOK
}

func StoreGraphExecutionBudget(currentState *state.State, budget GraphExecutionBudget) error {
	if currentState == nil {
		return nil
	}
	for _, item := range []struct {
		path  state.Path
		value int64
	}{
		{graphSchedulerStepsPath, budget.SuperSteps},
		{graphSchedulerNodesPath, budget.NodeExecutions},
		{graphSchedulerElapsedPath, int64(budget.ElapsedWallTime)},
	} {
		if err := state.SetPath(currentState, item.path.String(), item.value); err != nil {
			return err
		}
	}
	return nil
}

func LoadGraphExecutionBudget(currentState *state.State) (GraphExecutionBudget, bool) {
	if currentState == nil {
		return GraphExecutionBudget{}, false
	}
	access := state.NewAccess(currentState)
	superSteps, superStepsOK := access.ReadAny(graphSchedulerStepsPath)
	nodeExecutions, nodeExecutionsOK := access.ReadAny(graphSchedulerNodesPath)
	elapsedWallTime, elapsedWallTimeOK := access.ReadAny(graphSchedulerElapsedPath)
	return GraphExecutionBudget{
		SuperSteps:      graphSchedulerInt64(superSteps),
		NodeExecutions:  graphSchedulerInt64(nodeExecutions),
		ElapsedWallTime: time.Duration(graphSchedulerInt64(elapsedWallTime)),
	}, superStepsOK || nodeExecutionsOK || elapsedWallTimeOK
}

func ClearGraphSchedule(currentState *state.State) error {
	if currentState == nil {
		return nil
	}
	return state.DeletePath(currentState, state.Internal(graphSchedulerNamespace).String())
}

func StoreGraphReturnValue(currentState *state.State, value any) error {
	if currentState == nil {
		return nil
	}
	return state.SetPath(currentState, graphReturnValuePath.String(), value)
}

func LoadGraphReturnValue(currentState *state.State) (any, bool) {
	if currentState == nil {
		return nil, false
	}
	return state.ReadPath(currentState, graphReturnValuePath.String())
}

func ClearGraphReturnValue(currentState *state.State) error {
	if currentState == nil {
		return nil
	}
	return state.DeletePath(currentState, state.Internal(graphResultNamespace).String())
}

func graphScheduleNodeIDs(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		nodeIDs := make([]string, 0, len(typed))
		for _, item := range typed {
			if nodeID, ok := item.(string); ok && nodeID != "" {
				nodeIDs = append(nodeIDs, nodeID)
			}
		}
		return nodeIDs
	default:
		return nil
	}
}

func graphScheduleTasks(value any) []GraphTask {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var tasks []GraphTask
	if err := json.Unmarshal(payload, &tasks); err != nil {
		return nil
	}
	valid := tasks[:0]
	for _, task := range tasks {
		if task.TaskID == "" || task.NodeID == "" {
			continue
		}
		valid = append(valid, task)
	}
	return CloneGraphTasks(valid)
}

func graphSchedulerInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		if typed <= uint64(^uint64(0)>>1) {
			return int64(typed)
		}
	case float64:
		return int64(typed)
	}
	return 0
}
