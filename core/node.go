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
	Execute(ctx Context, access *state.Access) (NodeResult, error)
}

type NodeRef string

type NodeResult struct {
	Patch     state.Patch
	Command   Command
	Events    []EventDraft
	Artifacts []ArtifactDraft
}

type Command struct {
	Goto    []NodeRef
	Send    []Send
	Suspend *SuspendRequest
	Return  *ReturnCommand
}

type Send struct {
	Target         NodeRef
	Input          state.Patch
	CorrelationKey string
	OrderKey       string
}

type SuspendRequest struct {
	Value any
}

type ReturnCommand struct {
	Value any
}

type EventDraft struct {
	Type    string
	Payload any
}

type ArtifactDraft struct {
	Type     string
	MIMEType string
	Data     []byte
}

func Success() NodeResult {
	return NodeResult{}
}

type NodeInterrupt struct {
	NodeID string
	Value  any
}

func (interrupt *NodeInterrupt) Error() string {
	if interrupt == nil {
		return "node interrupted"
	}
	return fmt.Sprintf("interrupt at node %s: %v", interrupt.NodeID, interrupt.Value)
}

type NodeInfo struct {
	NodeID          string `json:"id" yaml:"id"`
	NodeName        string `json:"name" yaml:"name"`
	NodeDescription string `json:"description" yaml:"description"`
}

func (n *NodeInfo) Name() string {
	if n == nil {
		return ""
	}
	if n.NodeName == "" {
		return n.NodeID
	}
	return n.NodeName
}

func (n *NodeInfo) ID() string {
	if n == nil {
		return ""
	}
	return n.NodeID
}

func (n *NodeInfo) Description() string {
	if n == nil {
		return ""
	}
	return n.NodeDescription
}

type NodeSpec struct {
	ID          string
	Name        string
	Description string
}

func (s NodeSpec) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("node spec id is required")
	}
	return nil
}

type NodeBase struct {
	Spec   NodeSpec
	Effect EffectClass
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

func WithEffectClass(class EffectClass) NodeOption {
	return func(base *NodeBase) {
		if base != nil {
			base.Effect = NormalizeEffectClass(class)
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
	if b == nil {
		return fmt.Errorf("node base is nil")
	}
	return b.Spec.Validate()
}

func (b *NodeBase) ID() string {
	if b == nil {
		return ""
	}
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
	if b == nil {
		return ""
	}
	if b.Spec.Name != "" {
		return b.Spec.Name
	}
	return b.Spec.ID
}

func (b *NodeBase) Description() string {
	if b == nil {
		return ""
	}
	return b.Spec.Description
}

func (b *NodeBase) EffectClass() EffectClass {
	if b == nil {
		return EffectUnspecified
	}
	return NormalizeEffectClass(b.Effect)
}

type ContractProvider interface {
	Contract() state.Contract
}

func ContractFor(node Node) (state.Contract, error) {
	if node == nil {
		return state.Contract{}, fmt.Errorf("node is nil")
	}
	if provider, ok := node.(ContractProvider); ok {
		return provider.Contract().Clone(), nil
	}
	return state.Contract{}, nil
}

type ExecutionResult struct {
	State    *state.State
	Patch    state.Patch
	Contract state.Contract
	Node     NodeResult
}

type NodeExecutionOptions struct {
	Contract               *state.Contract
	InputState             *state.State
	EnforceInputProjection bool
	ValidateRequiredReads  bool
	ValidateWrites         bool
	ApplyPatchToInput      bool
	OnRequiredReadIssues   func([]state.ValidationIssue)
	OnWriteIssues          func([]state.ValidationIssue)
	Reducers               map[string]state.Reducer
}

func ExecuteNode(ctx context.Context, base *state.State, node Node) (ExecutionResult, error) {
	return ExecuteNodeWithOptions(ctx, base, node, NodeExecutionOptions{})
}

func ExecuteNodeWithOptions(ctx context.Context, base *state.State, node Node, options NodeExecutionOptions) (ExecutionResult, error) {
	if node == nil {
		return ExecutionResult{}, fmt.Errorf("node is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	if specNode, ok := node.(interface{ Validate() error }); ok {
		if err := specNode.Validate(); err != nil {
			return ExecutionResult{}, err
		}
	}

	var contract state.Contract
	if options.Contract != nil {
		contract = options.Contract.Clone()
	} else {
		var err error
		contract, err = ContractFor(node)
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
	isolatedInput, err := inputState.CloneStrict()
	if err != nil {
		return ExecutionResult{}, NewExecutionError(ErrorInvalidInput, fmt.Sprintf("node input state cannot be safely cloned: %v", err), err, nil)
	}
	inputState = isolatedInput

	if options.ValidateRequiredReads {
		if issues := state.ValidateRequiredReads(inputState, contract); len(issues) > 0 {
			if options.OnRequiredReadIssues != nil {
				options.OnRequiredReadIssues(issues)
			}
			return ExecutionResult{}, fmt.Errorf("%s", issues[0].Message)
		}
	}

	access := state.NewEditingAccessWithReducers(inputState, options.Reducers)
	nodeResult, err := node.Execute(NewContext(ctx), access)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	accessPatch := access.Patch()
	if !nodeResult.Patch.Empty() && !accessPatch.Empty() {
		return ExecutionResult{}, fmt.Errorf("node %q returned a patch after mutating state access", node.ID())
	}
	patch := nodeResult.Patch
	if patch.Empty() {
		patch = accessPatch
	}
	nodeResult.Patch = patch
	isolatedNodeResult, err := cloneNodeResultStrict(nodeResult)
	if err != nil {
		return ExecutionResult{}, NewExecutionError(ErrorNonRetryable, fmt.Sprintf("node result cannot be safely cloned: %v", err), err, nil)
	}
	nodeResult = isolatedNodeResult
	patch = nodeResult.Patch
	if issues := state.ValidateNodePatch(patch); len(issues) > 0 {
		if options.OnWriteIssues != nil {
			options.OnWriteIssues(issues)
		}
		return ExecutionResult{}, state.NewValidationError("node output", issues)
	}
	patchBase := inputState
	if options.ApplyPatchToInput {
		patchBase = base
	}
	resultState, err := patch.ApplyWithReducers(patchBase, options.Reducers)
	if err != nil {
		return ExecutionResult{}, err
	}
	if options.ValidateWrites {
		if issues := state.ValidateAppliedPatchResultByContract(resultState, patch, contract); len(issues) > 0 {
			if options.OnWriteIssues != nil {
				options.OnWriteIssues(issues)
			}
			return ExecutionResult{}, state.NewValidationError("node output", issues)
		}
	}
	resultState, err = resultState.CloneStrict()
	if err != nil {
		return ExecutionResult{}, NewExecutionError(ErrorNonRetryable, fmt.Sprintf("node result state cannot be safely cloned: %v", err), err, nil)
	}
	return ExecutionResult{
		State:    resultState,
		Patch:    patch,
		Contract: contract,
		Node:     nodeResult,
	}, nil
}

func cloneNodeResultStrict(result NodeResult) (NodeResult, error) {
	patch, err := clonePatchStrict(result.Patch)
	if err != nil {
		return NodeResult{}, fmt.Errorf("patch: %w", err)
	}
	command, err := cloneCommandStrict(result.Command)
	if err != nil {
		return NodeResult{}, fmt.Errorf("command: %w", err)
	}

	events := make([]EventDraft, len(result.Events))
	for index, draft := range result.Events {
		payload, cloneErr := state.CloneValue(draft.Payload)
		if cloneErr != nil {
			return NodeResult{}, fmt.Errorf("event %d payload: %w", index, cloneErr)
		}
		events[index] = draft
		events[index].Payload = payload
	}
	if result.Events == nil {
		events = nil
	}

	artifacts := make([]ArtifactDraft, len(result.Artifacts))
	for index, draft := range result.Artifacts {
		artifacts[index] = draft
		if draft.Data != nil {
			artifacts[index].Data = make([]byte, len(draft.Data))
			copy(artifacts[index].Data, draft.Data)
		}
	}
	if result.Artifacts == nil {
		artifacts = nil
	}

	return NodeResult{
		Patch:     patch,
		Command:   command,
		Events:    events,
		Artifacts: artifacts,
	}, nil
}

func cloneCommandStrict(command Command) (Command, error) {
	cloned := command
	if command.Goto != nil {
		cloned.Goto = make([]NodeRef, len(command.Goto))
		copy(cloned.Goto, command.Goto)
	}
	if command.Send != nil {
		cloned.Send = make([]Send, len(command.Send))
		for index, send := range command.Send {
			input, err := clonePatchStrict(send.Input)
			if err != nil {
				return Command{}, fmt.Errorf("send %d input: %w", index, err)
			}
			cloned.Send[index] = send
			cloned.Send[index].Input = input
		}
	}
	if command.Suspend != nil {
		value, err := state.CloneValue(command.Suspend.Value)
		if err != nil {
			return Command{}, fmt.Errorf("suspend value: %w", err)
		}
		cloned.Suspend = &SuspendRequest{Value: value}
	}
	if command.Return != nil {
		value, err := state.CloneValue(command.Return.Value)
		if err != nil {
			return Command{}, fmt.Errorf("return value: %w", err)
		}
		cloned.Return = &ReturnCommand{Value: value}
	}
	return cloned, nil
}

func clonePatchStrict(patch state.Patch) (state.Patch, error) {
	operations := patch.Ops()
	for index := range operations {
		value, err := state.CloneValue(operations[index].Value)
		if err != nil {
			return state.Patch{}, fmt.Errorf("operation %d value: %w", index, err)
		}
		operations[index].Value = value
	}
	return state.NewPatch(operations...), nil
}
