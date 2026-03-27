package falcon

import "context"

type Node[S any] interface {
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
	if n.NodeID != "" {
		return n.NodeID
	}
	return n.NodeName
}

func (n *NodeInfo) Description() string {
	return n.NodeDescription
}
