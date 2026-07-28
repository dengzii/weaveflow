package supervisor

import (
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	basenode "github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type Node = basenode.Node
type Base = basenode.Base
type Spec = basenode.Spec
type NodeOption = basenode.NodeOption

var (
	_ dsl.GraphNodeSpecProvider = (*SupervisorNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*SupervisorWorkerNode)(nil)
	_ dsl.GraphNodeSpecProvider = (*SupervisorSynthesisNode)(nil)
)

func WithID(id string) NodeOption {
	return basenode.WithID(id)
}

func WithName(name string) NodeOption {
	return basenode.WithName(name)
}

func NewBase(spec Spec) Base {
	return basenode.NewBase(spec)
}

func NewAgentNode(options ...NodeOption) *basenode.AgentNode {
	return basenode.NewAgentNode(options...)
}

func applyNodeOptions(base *Base, options []NodeOption) {
	core.ApplyNodeOptions(base, options)
}

func ApplyDefaultStatePaths(target Node) {
	if target == nil || strings.TrimSpace(target.ID()) == "" {
		return
	}
	setShared := func(path *state.Path, segments ...string) {
		if path.Empty() {
			*path = state.Shared(segments...)
		}
	}
	setScope := func(path *state.Path, owner string, segments ...string) {
		if path.Empty() {
			*path = state.Scope(defaultNodeOwner(owner), segments...)
		}
	}
	switch typed := target.(type) {
	case *SupervisorNode:
		setShared(&typed.ObjectivePath, "request", "input")
		setShared(&typed.SupervisorPath, "supervisor")
	case *SupervisorWorkerNode:
		setShared(&typed.SupervisorPath, "supervisor")
		setScope(&typed.ConversationPath, nodeOwner(typed, NodeTypeSupervisorWorker), "conversation")
	case *SupervisorSynthesisNode:
		setShared(&typed.SupervisorPath, "supervisor")
		setShared(&typed.ResultPath, "final", "answer")
	}
}

func (n *SupervisorNode) ApplyDefaultStatePaths() {
	ApplyDefaultStatePaths(n)
}

func (n *SupervisorWorkerNode) ApplyDefaultStatePaths() {
	ApplyDefaultStatePaths(n)
}

func (n *SupervisorSynthesisNode) ApplyDefaultStatePaths() {
	ApplyDefaultStatePaths(n)
}

func nodeOwner(target Node, fallback string) string {
	if target != nil {
		if id := strings.TrimSpace(target.ID()); id != "" {
			return id
		}
	}
	return fallback
}

func defaultNodeOwner(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "node"
	}
	return strings.ReplaceAll(owner, ".", "_")
}

func newGraphNodeSpec(base Base, nodeType string, config map[string]any, statePaths ...map[string]state.Path) dsl.GraphNodeSpec {
	if len(statePaths) > 0 {
		paths := make(map[string]state.Path, len(statePaths[0]))
		for name, path := range statePaths[0] {
			if path.Empty() {
				path = defaultGraphStatePath(base.ID(), nodeType, name)
			}
			paths[name] = path
		}
		statePaths[0] = paths
	}
	return basenode.NewGraphNodeSpec(base, nodeType, config, statePaths...)
}

func defaultGraphStatePath(nodeID, nodeType, port string) state.Path {
	switch port {
	case "objective":
		return state.Shared("request", "input")
	case "supervisor":
		return state.Shared("supervisor")
	case "conversation":
		return state.Scope(defaultNodeOwner(nodeOwnerFromID(nodeID, nodeType)), "conversation")
	case "result":
		return state.Shared("final", "answer")
	default:
		return state.Path{}
	}
}

func nodeOwnerFromID(nodeID, fallback string) string {
	if nodeID = strings.TrimSpace(nodeID); nodeID != "" {
		return nodeID
	}
	return fallback
}

func primitivePort(name, description, schemaType string, mode dsl.StateAccessMode, required bool) dsl.StatePortDefinition {
	return basenode.PrimitivePort(name, description, schemaType, mode, required)
}

func capabilityPort(name, description, capabilityID string, required bool, fields ...dsl.RelativeStateFieldRef) dsl.StatePortDefinition {
	return basenode.CapabilityPort(name, description, capabilityID, required, fields...)
}

func capabilityField(path string, mode dsl.StateAccessMode) dsl.RelativeStateFieldRef {
	return basenode.CapabilityField(path, mode)
}

func resolvedPath(spec registry.ResolvedNodeSpec, name string) (state.Path, error) {
	return basenode.ResolvedPath(spec, name)
}

func applyNodeMetadata(base *Base, spec dsl.GraphNodeSpec) {
	basenode.ApplyNodeMetadata(base, spec)
}

func effectiveModelID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	return id
}
