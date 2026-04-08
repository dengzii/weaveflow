package nodes

import (
	"context"
	"fmt"
	"weaveflow/dsl"
	fruntime "weaveflow/runtime"

	"github.com/google/uuid"
)

type SubgraphInvoker func(context.Context, fruntime.State) (fruntime.State, error)

type SubgraphNode struct {
	NodeInfo
	GraphRef       string
	InvokeSubgraph SubgraphInvoker
}

func NewSubgraphNode() *SubgraphNode {
	id := uuid.New()
	return &SubgraphNode{
		NodeInfo: NodeInfo{
			NodeID:          "Subgraph_" + id.String(),
			NodeName:        "Subgraph",
			NodeDescription: "Invoke another graph by graph_ref with the current state.",
		},
	}
}

func (n *SubgraphNode) Invoke(ctx context.Context, state fruntime.State) (fruntime.State, error) {
	if n.InvokeSubgraph == nil {
		return state, fmt.Errorf("subgraph node %q has no invoker for graph_ref %q", n.ID(), n.GraphRef)
	}

	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphStarted, map[string]any{
		"graph_ref": n.GraphRef,
	})
	nextState, err := n.InvokeSubgraph(ctx, state)
	if err != nil {
		_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFailed, map[string]any{
			"graph_ref": n.GraphRef,
			"error":     err.Error(),
		})
		return state, err
	}
	_ = fruntime.PublishRunnerContextEvent(ctx, fruntime.EventSubgraphFinished, map[string]any{
		"graph_ref": n.GraphRef,
	})
	return nextState, nil
}

func (n *SubgraphNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return dsl.GraphNodeSpec{
		ID:          n.ID(),
		Name:        n.Name(),
		Type:        "subgraph",
		Description: n.Description(),
		Config: map[string]any{
			"graph_ref": n.GraphRef,
		},
	}
}
