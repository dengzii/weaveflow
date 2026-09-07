package plan

import (
	"errors"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

const NodeTypePlanRouteFailure = "plan_route_failure"

type RouteFailureNode struct {
	core.NodeBase
}

func NewRouteFailureNode(options ...core.NodeOption) *RouteFailureNode {
	target := &RouteFailureNode{NodeBase: core.NewNodeBase(core.NodeSpec{
		Name:        NodeTypePlanRouteFailure,
		Description: "Fail when no valid plan status route matches.",
	})}
	applyNodeOptions(&target.NodeBase, options)
	return target
}

func (n *RouteFailureNode) GraphNodeSpec() dsl.GraphNodeSpec {
	return newGraphNodeSpec(n.NodeBase, NodeTypePlanRouteFailure, map[string]any{}, nil)
}

func (n *RouteFailureNode) Execute(core.Context, *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, errors.New("plan route failure: no valid plan status route matched")
}

func RouteFailureNodeTypeDefinition() registry.NodeTypeDefinition {
	return registry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:         NodeTypePlanRouteFailure,
			Title:        "Plan Route Failure",
			Description:  "Fail when no valid plan status route matches.",
			ConfigSchema: dsl.JSONSchema{"type": "object", "additionalProperties": false},
		},
		Build: func(_ *registry.BuildContext, resolved registry.ResolvedNodeSpec) (core.Node, error) {
			target := NewRouteFailureNode(core.WithID(resolved.Spec.ID))
			applyNodeMetadata(&target.NodeBase, resolved.Spec)
			return target, nil
		},
	}
}
