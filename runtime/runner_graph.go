package runtime

import (
	"context"
	wfstate "weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const DefaultGraphVersion = "1.0"

type RunnerExecution interface {
	ExecuteNode(ctx context.Context, nodeID string, executor wfstate.ExecutableNode, state wfstate.State) (wfstate.State, error)
	OnGraphStep(ctx context.Context, stepNodeID string, state wfstate.State) error
}

type RunnerGraph interface {
	Validate() error
	EntryPointID() string
	CompileForRunner(execution RunnerExecution) (*langgraph.StateRunnable[wfstate.State], error)
	ResolveNodeID(nodeID string) (string, error)
	ResolveNextNode(currentNodeID string, state wfstate.State) (string, error)
	NodeName(nodeID string) string
	NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state wfstate.State, err error)
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}
