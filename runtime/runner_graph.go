package runtime

import (
	"context"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

const DefaultGraphVersion = "1.0"

type NodeInvoker func(context.Context, State) (State, error)

type RunnerExecution interface {
	InvokeNode(ctx context.Context, nodeName string, invoke NodeInvoker, state State) (State, error)
	OnGraphStep(ctx context.Context, stepNode string, state State) error
}

type RunnerGraph interface {
	Validate() error
	EntryPointName() string
	CompileForRunner(execution RunnerExecution) (*langgraph.StateRunnable[State], error)
	ResolveNodeRef(ref string) (string, error)
	ResolveNextNode(currentName string, state State) (string, error)
	NodeID(nodeName string) string
	NotifyListeners(ctx context.Context, event langgraph.NodeEvent, nodeName string, state State, err error)
	AfterInterruptNodes(breakpoints []Breakpoint) ([]string, error)
}
