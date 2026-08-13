package stateops

import (
	"fmt"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type StateSetNode struct {
	core.NodeBase
	TargetPath      state.Path
	Value           any
	ValueConfigured bool
}

func NewStateSetNode(value any, options ...core.NodeOption) *StateSetNode {
	return &StateSetNode{
		NodeBase:        newBase(NodeTypeStateSet, "Set a JSON value at a bound state path.", options...),
		Value:           value,
		ValueConfigured: true,
	}
}

func (n *StateSetNode) Validate() error {
	if n == nil {
		return fmt.Errorf("%s node is nil", NodeTypeStateSet)
	}
	if err := validatePath(&n.NodeBase, NodeTypeStateSet, "target", n.TargetPath); err != nil {
		return err
	}
	if !n.ValueConfigured {
		return fmt.Errorf("%s node %q requires config field %q", NodeTypeStateSet, n.ID(), "value")
	}
	if _, _, err := normalizeJSON(n.Value); err != nil {
		return fmt.Errorf("%s node %q config value: %w", NodeTypeStateSet, n.ID(), err)
	}
	return nil
}

func (n *StateSetNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *StateSetNode) execute(_ core.Context, access *state.Access) error {
	value, _, err := normalizeJSON(n.Value)
	if err != nil {
		return fmt.Errorf("%s node %q config value: %w", NodeTypeStateSet, n.ID(), err)
	}
	if err := access.SetAny(n.TargetPath, value); err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateSet, n.ID(), err)
	}
	return nil
}

func (n *StateSetNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return replaceContract(n.TargetPath, state.AccessWrite, false, "State value to replace.")
}

func (n *StateSetNode) GraphNodeSpec() dsl.GraphNodeSpec {
	if n == nil {
		return dsl.GraphNodeSpec{Type: NodeTypeStateSet}
	}
	config := map[string]any{}
	if n.ValueConfigured {
		config["value"] = n.Value
	}
	return graphNodeSpec(n.NodeBase, NodeTypeStateSet, config, map[string]state.Path{"target": n.TargetPath})
}

type StateCopyNode struct {
	core.NodeBase
	SourcePath state.Path
	TargetPath state.Path
}

func NewStateCopyNode(options ...core.NodeOption) *StateCopyNode {
	return &StateCopyNode{NodeBase: newBase(NodeTypeStateCopy, "Copy a bound state value to another bound path.", options...)}
}

func (n *StateCopyNode) Validate() error {
	if n == nil {
		return fmt.Errorf("%s node is nil", NodeTypeStateCopy)
	}
	return validatePathPair(&n.NodeBase, NodeTypeStateCopy, "source", n.SourcePath, "target", n.TargetPath)
}

func (n *StateCopyNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *StateCopyNode) execute(_ core.Context, access *state.Access) error {
	value, err := readRequired(access, n.SourcePath, NodeTypeStateCopy, n.ID(), "source")
	if err != nil {
		return err
	}
	value, _, err = normalizeJSON(value)
	if err != nil {
		return fmt.Errorf("%s node %q source: %w", NodeTypeStateCopy, n.ID(), err)
	}
	if err := access.SetAny(n.TargetPath, value); err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateCopy, n.ID(), err)
	}
	return nil
}

func (n *StateCopyNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return twoPathContract(n.SourcePath, n.TargetPath, state.MergeReplace, "State value to read.", "State value to replace.")
}

func (n *StateCopyNode) GraphNodeSpec() dsl.GraphNodeSpec {
	if n == nil {
		return dsl.GraphNodeSpec{Type: NodeTypeStateCopy}
	}
	return graphNodeSpec(n.NodeBase, NodeTypeStateCopy, nil, map[string]state.Path{"source": n.SourcePath, "target": n.TargetPath})
}

type StateDeleteNode struct {
	core.NodeBase
	TargetPath state.Path
}

func NewStateDeleteNode(options ...core.NodeOption) *StateDeleteNode {
	return &StateDeleteNode{NodeBase: newBase(NodeTypeStateDelete, "Delete a bound state path.", options...)}
}

func (n *StateDeleteNode) Validate() error {
	if n == nil {
		return fmt.Errorf("%s node is nil", NodeTypeStateDelete)
	}
	return validatePath(&n.NodeBase, NodeTypeStateDelete, "target", n.TargetPath)
}

func (n *StateDeleteNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *StateDeleteNode) execute(_ core.Context, access *state.Access) error {
	if err := access.Delete(n.TargetPath); err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateDelete, n.ID(), err)
	}
	return nil
}

func (n *StateDeleteNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return replaceContract(n.TargetPath, state.AccessWrite, false, "State path to delete.")
}

func (n *StateDeleteNode) GraphNodeSpec() dsl.GraphNodeSpec {
	if n == nil {
		return dsl.GraphNodeSpec{Type: NodeTypeStateDelete}
	}
	return graphNodeSpec(n.NodeBase, NodeTypeStateDelete, nil, map[string]state.Path{"target": n.TargetPath})
}

type StateMergeNode struct {
	core.NodeBase
	SourcePath state.Path
	TargetPath state.Path
}

func NewStateMergeNode(options ...core.NodeOption) *StateMergeNode {
	return &StateMergeNode{NodeBase: newBase(NodeTypeStateMerge, "Deep-merge a bound object into another bound state path.", options...)}
}

func (n *StateMergeNode) Validate() error {
	if n == nil {
		return fmt.Errorf("%s node is nil", NodeTypeStateMerge)
	}
	if err := validatePathPair(&n.NodeBase, NodeTypeStateMerge, "source", n.SourcePath, "target", n.TargetPath); err != nil {
		return err
	}
	if n.SourcePath.String() == n.TargetPath.String() {
		return fmt.Errorf("%s node %q source and target paths must differ", NodeTypeStateMerge, n.ID())
	}
	return nil
}

func (n *StateMergeNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *StateMergeNode) execute(_ core.Context, access *state.Access) error {
	value, err := readRequired(access, n.SourcePath, NodeTypeStateMerge, n.ID(), "source")
	if err != nil {
		return err
	}
	value, _, err = normalizeJSON(value)
	if err != nil {
		return fmt.Errorf("%s node %q source: %w", NodeTypeStateMerge, n.ID(), err)
	}
	overlay, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%s node %q source path %q requires a JSON object, got %T", NodeTypeStateMerge, n.ID(), n.SourcePath.String(), value)
	}
	if err := access.MergeAny(n.TargetPath, overlay); err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateMerge, n.ID(), err)
	}
	return nil
}

func (n *StateMergeNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return twoPathContract(n.SourcePath, n.TargetPath, state.MergeMerge, "JSON object to read.", "JSON object to merge.")
}

func (n *StateMergeNode) GraphNodeSpec() dsl.GraphNodeSpec {
	if n == nil {
		return dsl.GraphNodeSpec{Type: NodeTypeStateMerge}
	}
	return graphNodeSpec(n.NodeBase, NodeTypeStateMerge, nil, map[string]state.Path{"source": n.SourcePath, "target": n.TargetPath})
}

type StateAppendNode struct {
	core.NodeBase
	SourcePath state.Path
	TargetPath state.Path
}

func NewStateAppendNode(options ...core.NodeOption) *StateAppendNode {
	return &StateAppendNode{NodeBase: newBase(NodeTypeStateAppend, "Append a bound JSON value or array to a bound state array.", options...)}
}

func (n *StateAppendNode) Validate() error {
	if n == nil {
		return fmt.Errorf("%s node is nil", NodeTypeStateAppend)
	}
	if err := validatePathPair(&n.NodeBase, NodeTypeStateAppend, "source", n.SourcePath, "target", n.TargetPath); err != nil {
		return err
	}
	if n.SourcePath.String() == n.TargetPath.String() {
		return fmt.Errorf("%s node %q source and target paths must differ", NodeTypeStateAppend, n.ID())
	}
	return nil
}

func (n *StateAppendNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *StateAppendNode) execute(_ core.Context, access *state.Access) error {
	value, err := readRequired(access, n.SourcePath, NodeTypeStateAppend, n.ID(), "source")
	if err != nil {
		return err
	}
	value, _, err = normalizeJSON(value)
	if err != nil {
		return fmt.Errorf("%s node %q source: %w", NodeTypeStateAppend, n.ID(), err)
	}
	if err := access.AppendAny(n.TargetPath, value); err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateAppend, n.ID(), err)
	}
	return nil
}

func (n *StateAppendNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	return twoPathContract(n.SourcePath, n.TargetPath, state.MergeAppend, "JSON value or array to read.", "State array to append.")
}

func (n *StateAppendNode) GraphNodeSpec() dsl.GraphNodeSpec {
	if n == nil {
		return dsl.GraphNodeSpec{Type: NodeTypeStateAppend}
	}
	return graphNodeSpec(n.NodeBase, NodeTypeStateAppend, nil, map[string]state.Path{"source": n.SourcePath, "target": n.TargetPath})
}
