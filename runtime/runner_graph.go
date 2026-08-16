package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
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

const (
	nodeAttemptPending uint32 = iota
	nodeAttemptAccepted
	nodeAttemptAbandoned
)

type nodeAttemptKey struct{}

// NodeAttempt arbitrates whether a node result or its deadline owns an attempt.
type NodeAttempt struct {
	decision atomic.Uint32
}

func NewNodeAttempt() *NodeAttempt {
	return &NodeAttempt{}
}

// TryAccept reserves the attempt for result delivery or durable result commit.
func (attempt *NodeAttempt) TryAccept() bool {
	if attempt == nil {
		return false
	}
	for {
		switch attempt.decision.Load() {
		case nodeAttemptAccepted:
			return true
		case nodeAttemptAbandoned:
			return false
		case nodeAttemptPending:
			if attempt.decision.CompareAndSwap(nodeAttemptPending, nodeAttemptAccepted) {
				return true
			}
		}
	}
}

// TryAbandon reserves the attempt for timeout or cancellation handling.
func (attempt *NodeAttempt) TryAbandon() bool {
	return attempt != nil && attempt.decision.CompareAndSwap(nodeAttemptPending, nodeAttemptAbandoned)
}

func (attempt *NodeAttempt) IsAbandoned() bool {
	return attempt != nil && attempt.decision.Load() == nodeAttemptAbandoned
}

func WithNodeAttempt(ctx context.Context, attempt *NodeAttempt) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if attempt == nil {
		return ctx
	}
	return context.WithValue(ctx, nodeAttemptKey{}, attempt)
}

func NodeAttemptFromContext(ctx context.Context) (*NodeAttempt, bool) {
	if ctx == nil {
		return nil, false
	}
	attempt, ok := ctx.Value(nodeAttemptKey{}).(*NodeAttempt)
	return attempt, ok && attempt != nil
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

type TaskErrorRecorder interface {
	OnTaskError(task GraphTask, err error)
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
	PendingFanInTasks []GraphTask `json:"pending_fan_in_tasks,omitempty"`
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
	clonedValue, _ := state.CloneValue(details)
	cloned, _ := clonedValue.(map[string]any)
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
	ResolveEdgeTarget(target string) (string, error)
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
const graphSchedulerVersion = "2"
const graphResultNamespace = "graph_result"

var (
	graphSchedulerVersionPath       = state.Internal(graphSchedulerNamespace, "version")
	graphSchedulerPendingTasksPath  = state.Internal(graphSchedulerNamespace, "pending_fan_in_tasks")
	graphSchedulerLegacyPendingPath = state.Internal(graphSchedulerNamespace, "pending_fan_in_nodes")
	graphSchedulerCurrentPath       = state.Internal(graphSchedulerNamespace, "current_tasks")
	graphSchedulerNextPath          = state.Internal(graphSchedulerNamespace, "next_tasks")
	graphSchedulerStepsPath         = state.Internal(graphSchedulerNamespace, "super_steps")
	graphSchedulerNodesPath         = state.Internal(graphSchedulerNamespace, "node_executions")
	graphSchedulerElapsedPath       = state.Internal(graphSchedulerNamespace, "elapsed_wall_time_ns")
	graphReturnValuePath            = state.Internal(graphResultNamespace, "return_value")
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
	if err := state.SetPath(currentState, graphSchedulerVersionPath.String(), graphSchedulerVersion); err != nil {
		return err
	}
	for _, item := range []struct {
		path  state.Path
		tasks []GraphTask
	}{
		{path: graphSchedulerCurrentPath, tasks: schedule.CurrentTasks},
		{path: graphSchedulerNextPath, tasks: schedule.NextTasks},
		{path: graphSchedulerPendingTasksPath, tasks: schedule.PendingFanInTasks},
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
	return nil
}

func LoadGraphSchedule(currentState *state.State) (GraphSchedule, bool, error) {
	if currentState == nil {
		return GraphSchedule{}, false, nil
	}
	access := state.NewAccess(currentState)
	if _, found := access.ReadAny(state.Internal(graphSchedulerNamespace)); !found {
		return GraphSchedule{}, false, nil
	}
	if _, legacy := access.ReadAny(graphSchedulerLegacyPendingPath); legacy {
		return GraphSchedule{}, true, fmt.Errorf("unsupported graph scheduler metadata field %q", graphSchedulerLegacyPendingPath.String())
	}
	versionValue, _ := access.ReadAny(graphSchedulerVersionPath)
	currentValue, currentOK := access.ReadAny(graphSchedulerCurrentPath)
	nextValue, nextOK := access.ReadAny(graphSchedulerNextPath)
	pendingValue, pendingOK := access.ReadAny(graphSchedulerPendingTasksPath)
	version, ok := versionValue.(string)
	if !ok || version != graphSchedulerVersion {
		return GraphSchedule{}, true, fmt.Errorf("unsupported graph scheduler metadata version %q", fmt.Sprint(versionValue))
	}
	currentTasks, err := graphScheduleTasks("current_tasks", currentValue, currentOK)
	if err != nil {
		return GraphSchedule{}, true, err
	}
	nextTasks, err := graphScheduleTasks("next_tasks", nextValue, nextOK)
	if err != nil {
		return GraphSchedule{}, true, err
	}
	pendingTasks, err := graphScheduleTasks("pending_fan_in_tasks", pendingValue, pendingOK)
	if err != nil {
		return GraphSchedule{}, true, err
	}
	return GraphSchedule{
		CurrentTasks:      currentTasks,
		NextTasks:         nextTasks,
		PendingFanInTasks: pendingTasks,
	}, true, nil
}

func StoreGraphExecutionBudget(currentState *state.State, budget GraphExecutionBudget) error {
	if currentState == nil {
		return nil
	}
	if err := state.SetPath(currentState, graphSchedulerVersionPath.String(), graphSchedulerVersion); err != nil {
		return err
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

func LoadGraphExecutionBudget(currentState *state.State) (GraphExecutionBudget, bool, error) {
	if currentState == nil {
		return GraphExecutionBudget{}, false, nil
	}
	access := state.NewAccess(currentState)
	if _, found := access.ReadAny(state.Internal(graphSchedulerNamespace)); !found {
		return GraphExecutionBudget{}, false, nil
	}
	versionValue, _ := access.ReadAny(graphSchedulerVersionPath)
	version, ok := versionValue.(string)
	if !ok || version != graphSchedulerVersion {
		return GraphExecutionBudget{}, false, fmt.Errorf("unsupported graph scheduler metadata version %q", fmt.Sprint(versionValue))
	}
	superSteps, superStepsOK := access.ReadAny(graphSchedulerStepsPath)
	nodeExecutions, nodeExecutionsOK := access.ReadAny(graphSchedulerNodesPath)
	elapsedWallTime, elapsedWallTimeOK := access.ReadAny(graphSchedulerElapsedPath)
	if !superStepsOK && !nodeExecutionsOK && !elapsedWallTimeOK {
		return GraphExecutionBudget{}, false, nil
	}
	if !superStepsOK || !nodeExecutionsOK || !elapsedWallTimeOK {
		return GraphExecutionBudget{}, true, fmt.Errorf("graph scheduler execution budget is incomplete")
	}
	superStepCount, err := graphSchedulerInt64("super_steps", superSteps)
	if err != nil {
		return GraphExecutionBudget{}, true, err
	}
	nodeExecutionCount, err := graphSchedulerInt64("node_executions", nodeExecutions)
	if err != nil {
		return GraphExecutionBudget{}, true, err
	}
	elapsedNanoseconds, err := graphSchedulerInt64("elapsed_wall_time_ns", elapsedWallTime)
	if err != nil {
		return GraphExecutionBudget{}, true, err
	}
	return GraphExecutionBudget{
		SuperSteps:      superStepCount,
		NodeExecutions:  nodeExecutionCount,
		ElapsedWallTime: time.Duration(elapsedNanoseconds),
	}, true, nil
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

func graphScheduleTasks(field string, value any, present bool) ([]GraphTask, error) {
	if !present {
		return nil, nil
	}
	if value == nil {
		return nil, fmt.Errorf("graph scheduler field %q cannot be null", field)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode graph scheduler field %q: %w", field, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var tasks []GraphTask
	if err := decoder.Decode(&tasks); err != nil {
		return nil, fmt.Errorf("decode graph scheduler field %q: %w", field, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("graph scheduler field %q contains multiple JSON values", field)
		}
		return nil, fmt.Errorf("decode graph scheduler field %q: %w", field, err)
	}
	seenTaskIDs := make(map[string]struct{}, len(tasks))
	seenNodeIDs := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if strings.TrimSpace(task.TaskID) == "" || task.TaskID != strings.TrimSpace(task.TaskID) {
			return nil, fmt.Errorf("graph scheduler field %q task %d has invalid task ID %q", field, index, task.TaskID)
		}
		if _, exists := seenTaskIDs[task.TaskID]; exists {
			return nil, fmt.Errorf("graph scheduler field %q has duplicate task ID %q", field, task.TaskID)
		}
		seenTaskIDs[task.TaskID] = struct{}{}
		if strings.TrimSpace(task.NodeID) == "" || task.NodeID != strings.TrimSpace(task.NodeID) {
			return nil, fmt.Errorf("graph scheduler field %q task %d has invalid node ID %q", field, index, task.NodeID)
		}
		if field == "pending_fan_in_tasks" {
			if _, exists := seenNodeIDs[task.NodeID]; exists {
				return nil, fmt.Errorf("graph scheduler field %q has duplicate node ID %q", field, task.NodeID)
			}
			seenNodeIDs[task.NodeID] = struct{}{}
		}
		if issues := state.ValidateInputPatch(task.Input); len(issues) > 0 {
			return nil, fmt.Errorf("graph scheduler field %q task %d input: %w", field, index, state.NewValidationError("task input", issues))
		}
	}
	return CloneGraphTasks(tasks), nil
}

func graphSchedulerInt64(field string, value any) (int64, error) {
	var result int64
	switch typed := value.(type) {
	case int:
		result = int64(typed)
	case int8:
		result = int64(typed)
	case int16:
		result = int64(typed)
	case int32:
		result = int64(typed)
	case int64:
		result = typed
	case uint:
		if uint64(typed) > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("graph scheduler field %q overflows int64", field)
		}
		result = int64(typed)
	case uint8:
		result = int64(typed)
	case uint16:
		result = int64(typed)
	case uint32:
		result = int64(typed)
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("graph scheduler field %q overflows int64", field)
		}
		result = int64(typed)
	case float64:
		result = int64(typed)
		if float64(result) != typed {
			return 0, fmt.Errorf("graph scheduler field %q must be an integer", field)
		}
	default:
		return 0, fmt.Errorf("graph scheduler field %q has invalid type %T", field, value)
	}
	if result < 0 {
		return 0, fmt.Errorf("graph scheduler field %q cannot be negative", field)
	}
	return result, nil
}
