package falcon

type serializableNode struct {
	Node[State]
	spec GraphNodeSpec
}

func WithNodeSpec(node Node[State], spec GraphNodeSpec) Node[State] {
	if node == nil {
		return nil
	}
	if len(spec.Config) > 0 {
		spec.Config = cloneMap(spec.Config)
	}
	return &serializableNode{
		Node: node,
		spec: spec,
	}
}

func WithNodeType(node Node[State], nodeType string, config map[string]any) Node[State] {
	return WithNodeSpec(node, GraphNodeSpec{
		Type:   nodeType,
		Config: cloneMap(config),
	})
}

func (n *serializableNode) GraphNodeSpec() GraphNodeSpec {
	if n == nil {
		return GraphNodeSpec{}
	}
	spec := n.spec
	if len(spec.Config) > 0 {
		spec.Config = cloneMap(spec.Config)
	}
	return spec
}
