package falcon

import (
	"context"
)

const (
	IdStartNode = "id_start_node"
	IdEndNode   = "id_end_node"
)

type NodeDefinition[S BaseState] interface {
	ID() string
	Name() string
	Description() string
	Invoke(ctx context.Context, state S) (S, error)
}

type NodeInfo struct {
	NodeID          string `json:"id" yaml:"id"`
	NodeName        string `json:"name" yaml:"name"`
	NodeDescription string `json:"description" yaml:"description"`
}

func (n *NodeInfo) Name() string {
	return n.NodeName
}

func (n *NodeInfo) ID() string {
	return n.NodeID
}

func (n *NodeInfo) Description() string {
	return n.NodeDescription
}

type StartNode[S BaseState] struct {
	NodeInfo
}

func NewStartNode[S BaseState]() *StartNode[S] {
	return &StartNode[S]{
		NodeInfo: NodeInfo{
			NodeID:          IdStartNode,
			NodeName:        "Start Node",
			NodeDescription: "start",
		},
	}
}

func (s *StartNode[S]) Invoke(_ context.Context, state S) (S, error) {
	return state, nil
}

func NewEndNode[S BaseState]() *StartNode[S] {
	return &StartNode[S]{
		NodeInfo: NodeInfo{
			NodeID:          IdEndNode,
			NodeName:        "End Node",
			NodeDescription: "end node",
		},
	}
}

type EndNode[S BaseState] struct {
	NodeInfo
}

func (e *EndNode[S]) Invoke(_ context.Context, state S) (S, error) {
	return state, nil
}
