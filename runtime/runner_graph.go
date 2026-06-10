package runtime

import (
	"context"
	"weaveflow/core"
	"weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const DefaultGraphVersion = "1.0"

type RunnerExecution interface {
	ExecuteNode(ctx context.Context, nodeID string, executor RunnerNode, state *state.State) (*state.State, error)
	OnGraphStep(ctx context.Context, stepNodeID string, state *state.State) error
}

type RunnerNode interface {
	ID() string
	Name() string
	Description() string
	Scope() string
	Execute(ctx core.Context, access *state.Access) error
}

type RunnerGraph interface {
	Validate() error
	EntryPointID() string
	CompileForRunner(execution RunnerExecution) (*langgraph.StateRunnable[*state.State], error)
	ResolveNodeID(nodeID string) (string, error)
	ResolveNextNode(currentNodeID string, state *state.State) (string, error)
	NodeName(nodeID string) string
	NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state *state.State, err error)
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}
