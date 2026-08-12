package runtime

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

const (
	DefaultGraphVersion = "1.0"
	EndNodeID           = "END"
)

type RunnerExecution interface {
	ExecuteNode(ctx context.Context, nodeID string, executor core.Node, state *state.State) (*state.State, error)
	OnGraphStep(ctx context.Context, stepNodeID string, state *state.State) error
}

type BranchPatchRecorder interface {
	RecordBranchPatch(base *state.State, nodeID string, patch state.Patch)
}

type ParallelWaveRecorder interface {
	OnParallelWave(ctx context.Context, base *state.State, nodeIDs []string) error
}

type BranchPatchRecorderSetter interface {
	SetBranchPatchRecorder(recorder BranchPatchRecorder)
}

type SchedulerConfig struct {
	StartNodeIDs          []string
	InterruptAfterNodeIDs []string
	StepObserver          func(context.Context, string, *state.State) error
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
	IsParallelBranchTarget(nodeID string) bool
	NodeName(nodeID string) string
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}

type GraphInterrupt struct {
	NodeID      string
	State       *state.State
	Value       any
	NextNodeIDs []string
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

var (
	graphSchedulerPendingPath = state.Internal(graphSchedulerNamespace, "pending_fan_in_nodes")
	graphSchedulerNextPath    = state.Internal(graphSchedulerNamespace, "next_nodes")
)

func StoreGraphSchedule(currentState *state.State, nextNodeIDs, pendingFanInNodeIDs []string) error {
	if currentState == nil {
		return nil
	}
	if len(nextNodeIDs) == 0 {
		if err := state.DeletePath(currentState, graphSchedulerNextPath.String()); err != nil {
			return err
		}
	} else if err := state.SetPath(currentState, graphSchedulerNextPath.String(), append([]string(nil), nextNodeIDs...)); err != nil {
		return err
	}
	if len(pendingFanInNodeIDs) == 0 {
		return state.DeletePath(currentState, graphSchedulerPendingPath.String())
	}
	return state.SetPath(currentState, graphSchedulerPendingPath.String(), append([]string(nil), pendingFanInNodeIDs...))
}

func LoadGraphSchedule(currentState *state.State) (nextNodeIDs, pendingFanInNodeIDs []string, ok bool) {
	if currentState == nil {
		return nil, nil, false
	}
	access := state.NewAccess(currentState)
	nextValue, nextOK := access.ReadAny(graphSchedulerNextPath)
	pendingValue, pendingOK := access.ReadAny(graphSchedulerPendingPath)
	return graphScheduleNodeIDs(nextValue), graphScheduleNodeIDs(pendingValue), nextOK || pendingOK
}

func ClearGraphSchedule(currentState *state.State) error {
	if currentState == nil {
		return nil
	}
	return state.DeletePath(currentState, state.Internal(graphSchedulerNamespace).String())
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
