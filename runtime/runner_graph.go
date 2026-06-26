package runtime

import (
	"context"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const DefaultGraphVersion = "1.0"

type RunnerExecution interface {
	ExecuteNode(ctx context.Context, nodeID string, executor RunnerNode, state *state.State) (*state.State, error)
	OnGraphStep(ctx context.Context, stepNodeID string, state *state.State) error
}

type BranchPatchRecorder interface {
	RecordBranchPatch(base *state.State, nodeID string, patch state.Patch)
}

type ParallelWaveRecorder interface {
	OnParallelWave(base *state.State, nodeIDs []string)
}

type BranchPatchRecorderSetter interface {
	SetBranchPatchRecorder(recorder BranchPatchRecorder)
}

type RunnerNode = core.Node

type RunnerGraph interface {
	Validate() error
	EntryPointID() string
	CompileForRunner(execution RunnerExecution) (*langgraph.StateRunnable[*state.State], error)
	ResolveNodeID(nodeID string) (string, error)
	ResolveNextNodes(ctx context.Context, currentNodeID string, state *state.State) ([]string, error)
	ResolveNextNode(ctx context.Context, currentNodeID string, state *state.State) (string, error)
	IsParallelBranchTarget(nodeID string) bool
	NodeName(nodeID string) string
	NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state *state.State, err error)
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}
