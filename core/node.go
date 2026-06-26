package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/state"
)

type Node interface {
	ID() string
	Name() string
	Description() string
	Scope() string
	AccessorUses() []AccessorUse
	Execute(ctx Context, access *state.Access) error
}

type NodeInfo struct {
	NodeID          string `json:"id" yaml:"id"`
	NodeName        string `json:"name" yaml:"name"`
	NodeDescription string `json:"description" yaml:"description"`
}

func (n *NodeInfo) Name() string {
	if n.NodeName == "" {
		return n.NodeID
	}
	return n.NodeName
}

func (n *NodeInfo) ID() string {
	if n.NodeID == "" {
		panic("NodeID is empty " + n.Name())
	}
	return n.NodeID
}

func (n *NodeInfo) Description() string {
	return n.NodeDescription
}

type AccessorUse struct {
	Name             string
	Scope            string
	InheritNodeScope bool
}

func Use(accessorName string) AccessorUse {
	return AccessorUse{Name: accessorName, InheritNodeScope: true}
}

func UseRoot(accessorName string) AccessorUse {
	return AccessorUse{Name: accessorName}
}

func UseScoped(accessorName string, scope string) AccessorUse {
	return AccessorUse{Name: accessorName, Scope: scope}
}

func (u AccessorUse) EffectiveScope(nodeScope string) string {
	if u.InheritNodeScope {
		return nodeScope
	}
	return u.Scope
}

type NodeSpec struct {
	ID           string
	Name         string
	Description  string
	Scope        string
	AccessorUses []AccessorUse
}

func (s NodeSpec) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("node spec id is required")
	}
	return nil
}

type NodeBase struct {
	Spec NodeSpec
}

type NodeOption func(*NodeBase)

func WithID(id string) NodeOption {
	return func(base *NodeBase) {
		if base != nil {
			base.SetID(id)
		}
	}
}

func WithName(name string) NodeOption {
	return func(base *NodeBase) {
		if base != nil {
			base.Spec.Name = strings.TrimSpace(name)
		}
	}
}

func WithScope(scope string) NodeOption {
	return func(base *NodeBase) {
		if base != nil {
			base.Spec.Scope = strings.TrimSpace(scope)
		}
	}
}

func NewNodeBase(spec NodeSpec) NodeBase {
	return NodeBase{Spec: spec}
}

func ApplyNodeOptions(base *NodeBase, options []NodeOption) {
	for _, option := range options {
		if option != nil {
			option(base)
		}
	}
}

func (b *NodeBase) Validate() error {
	return b.Spec.Validate()
}

func (b *NodeBase) ID() string {
	return b.Spec.ID
}

func (b *NodeBase) SetID(id string) {
	if b == nil {
		return
	}
	b.Spec.ID = strings.TrimSpace(id)
	if b.Spec.ID != "" {
		b.Spec.Name = b.Spec.ID
	}
}

func (b *NodeBase) Name() string {
	if b.Spec.Name != "" {
		return b.Spec.Name
	}
	return b.Spec.ID
}

func (b *NodeBase) Description() string {
	return b.Spec.Description
}

func (b *NodeBase) Scope() string {
	return b.Spec.Scope
}

func (b *NodeBase) AccessorUses() []AccessorUse {
	if len(b.Spec.AccessorUses) == 0 {
		return nil
	}
	return append([]AccessorUse(nil), b.Spec.AccessorUses...)
}

type ContractProvider interface {
	Contract(*state.Registry) (state.Contract, error)
}

func ContractFor(registry *state.Registry, node Node) (state.Contract, error) {
	if registry == nil {
		return state.Contract{}, fmt.Errorf("state registry is required")
	}
	if node == nil {
		return state.Contract{}, fmt.Errorf("node node is nil")
	}
	if provider, ok := node.(ContractProvider); ok {
		return provider.Contract(registry)
	}
	return ContractFromAccessorUses(registry, node.ID(), node.Scope(), node.AccessorUses())
}

func ContractFromAccessorUses(registry *state.Registry, nodeID string, nodeScope string, uses []AccessorUse) (state.Contract, error) {
	fields := make([]state.FieldAccess, 0)
	wildcardRead := false
	wildcardWrite := false
	for _, use := range uses {
		if use.Name == "" {
			return state.Contract{}, fmt.Errorf("node %q declares an empty accessor use", nodeID)
		}
		contract, ok := registry.AccessorContract(use.Name, use.EffectiveScope(nodeScope))
		if !ok {
			return state.Contract{}, fmt.Errorf("node %q requires unregistered state accessor %q", nodeID, use.Name)
		}
		fields = append(fields, contract.Fields...)
		wildcardRead = wildcardRead || contract.WildcardRead
		wildcardWrite = wildcardWrite || contract.WildcardWrite
	}

	contract := state.NewContract(fields...)
	contract.WildcardRead = wildcardRead
	contract.WildcardWrite = wildcardWrite
	return contract, nil
}

type ExecutionResult struct {
	State    *state.State
	Patch    state.Patch
	Contract state.Contract
}

type NodeExecutionOptions struct {
	Registry               *state.Registry
	Contract               *state.Contract
	InputState             *state.State
	EnforceInputProjection bool
	ValidateRequiredReads  bool
	ValidateWrites         bool
	ApplyPatchToInput      bool
	OnRequiredReadIssues   func([]state.ValidationIssue)
	OnWriteIssues          func([]state.ValidationIssue)
}

func ExecuteNode(ctx context.Context, registry *state.Registry, base *state.State, node Node) (ExecutionResult, error) {
	return ExecuteNodeWithOptions(ctx, base, node, NodeExecutionOptions{
		Registry: registry,
	})
}

func ExecuteNodeWithOptions(ctx context.Context, base *state.State, node Node, options NodeExecutionOptions) (ExecutionResult, error) {
	if node == nil {
		return ExecutionResult{}, fmt.Errorf("node node is nil")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	if specNode, ok := node.(interface{ Validate() error }); ok {
		if err := specNode.Validate(); err != nil {
			return ExecutionResult{}, err
		}
	}

	registry := options.Registry
	contract := state.Contract{}
	if options.Contract != nil {
		contract = options.Contract.Clone()
	} else {
		var err error
		contract, err = ContractFor(registry, node)
		if err != nil {
			return ExecutionResult{}, err
		}
	}

	inputState := base
	if options.InputState != nil {
		inputState = options.InputState
	} else if options.EnforceInputProjection {
		inputState = state.ProjectStateByContract(base, contract)
	}

	if options.ValidateRequiredReads {
		if issues := state.ValidateRequiredReads(inputState, contract); len(issues) > 0 {
			if options.OnRequiredReadIssues != nil {
				options.OnRequiredReadIssues(issues)
			}
			return ExecutionResult{}, fmt.Errorf("%s", issues[0].Message)
		}
	}

	access := state.NewEditingAccess(registry, inputState).WithScope(node.Scope())
	if err := node.Execute(NewContext(ctx), access); err != nil {
		return ExecutionResult{}, err
	}
	patch := access.Patch()
	if options.ValidateWrites {
		if issues := state.ValidatePatchByContract(patch, contract); len(issues) > 0 {
			if options.OnWriteIssues != nil {
				options.OnWriteIssues(issues)
			}
			return ExecutionResult{}, fmt.Errorf("%s", issues[0].Message)
		}
	}
	resultState := access.State()
	if options.ApplyPatchToInput {
		merged, err := patch.Apply(base)
		if err != nil {
			return ExecutionResult{}, err
		}
		resultState = merged
	}
	return ExecutionResult{
		State:    resultState,
		Patch:    patch,
		Contract: contract,
	}, nil
}
