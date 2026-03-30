package runtime

import (
	"context"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const DefaultGraphVersion = "1.0"

type NodeInvoker func(context.Context, State) (State, error)

type RunnerExecution interface {
	InvokeNode(ctx context.Context, nodeID string, invoke NodeInvoker, state State) (State, error)
	OnGraphStep(ctx context.Context, stepNodeID string, state State) error
}

type RunnerGraph interface {
	Validate() error
	EntryPointID() string
	CompileForRunner(execution RunnerExecution) (*langgraph.StateRunnable[State], error)
	ResolveNodeID(nodeID string) (string, error)
	ResolveNextNode(currentNodeID string, state State) (string, error)
	NodeName(nodeID string) string
	NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeID string, state State, err error)
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}
