package stateops

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/stateexpr"
	"github.com/dengzii/weaveflow/state"
)

const (
	maxTransformInputBytes  = stateexpr.MaxInputBytes
	maxTransformOutputBytes = stateexpr.MaxOutputBytes
)

type StateTransformNode struct {
	core.NodeBase
	InputPaths map[string]state.Path
	OutputPath state.Path
	Expression string
	program    *stateexpr.Program
}

func NewStateTransformNode(expression string, options ...core.NodeOption) (*StateTransformNode, error) {
	program, err := stateexpr.Compile(expression, stateexpr.CompileOptions{})
	if err != nil {
		return nil, err
	}
	return &StateTransformNode{
		NodeBase:   newBase(NodeTypeStateTransform, "Transform explicitly bound state inputs with a restricted CEL expression.", options...),
		Expression: strings.TrimSpace(expression),
		program:    program,
	}, nil
}

func (n *StateTransformNode) Validate() error {
	if n == nil {
		return fmt.Errorf("%s node is nil", NodeTypeStateTransform)
	}
	if err := validateBase(&n.NodeBase, NodeTypeStateTransform); err != nil {
		return err
	}
	if n.OutputPath.Empty() {
		return fmt.Errorf("%s node %q requires resolved state port %q", NodeTypeStateTransform, n.ID(), "output")
	}
	if len(n.InputPaths) == 0 {
		return fmt.Errorf("%s node %q requires at least one dynamic input", NodeTypeStateTransform, n.ID())
	}
	for name, path := range n.InputPaths {
		if name == "output" {
			return fmt.Errorf("%s node %q dynamic input alias %q is reserved", NodeTypeStateTransform, n.ID(), name)
		}
		if path.Empty() {
			return fmt.Errorf("%s node %q dynamic input %q has no resolved path", NodeTypeStateTransform, n.ID(), name)
		}
	}
	if strings.TrimSpace(n.Expression) == "" {
		return fmt.Errorf("%s node %q requires config field %q", NodeTypeStateTransform, n.ID(), "expression")
	}
	if n.program == nil {
		return fmt.Errorf("%s node %q expression is not compiled", NodeTypeStateTransform, n.ID())
	}
	return nil
}

func (n *StateTransformNode) Execute(ctx core.Context, access *state.Access) error {
	inputs := make(map[string]any, len(n.InputPaths))
	for _, name := range sortedInputNames(n.InputPaths) {
		value, err := readRequired(access, n.InputPaths[name], NodeTypeStateTransform, n.ID(), name)
		if err != nil {
			return err
		}
		inputs[name] = value
	}
	output, err := n.program.EvalJSON(ctx, inputs)
	if err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateTransform, n.ID(), err)
	}
	if err := access.SetAny(n.OutputPath, output); err != nil {
		return fmt.Errorf("%s node %q: %w", NodeTypeStateTransform, n.ID(), err)
	}
	return nil
}

func (n *StateTransformNode) Contract() state.Contract {
	if n == nil || n.OutputPath.Empty() {
		return state.Contract{}
	}
	fields := make([]state.FieldAccess, 0, len(n.InputPaths)+1)
	indexes := map[string]int{}
	appendRead := func(path state.Path, description string) {
		if path.Empty() {
			return
		}
		key := path.String()
		if _, exists := indexes[key]; exists {
			return
		}
		indexes[key] = len(fields)
		fields = append(fields, state.FieldAccess{Path: path, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Description: description})
	}
	for _, name := range sortedInputNames(n.InputPaths) {
		appendRead(n.InputPaths[name], "JSON value exposed as inputs."+name+".")
	}
	outputKey := n.OutputPath.String()
	if index, exists := indexes[outputKey]; exists {
		fields[index].Mode = state.AccessReadWrite
		fields[index].Description += " Transformed JSON value to replace."
	} else {
		fields = append(fields, state.FieldAccess{Path: n.OutputPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Description: "Transformed JSON value to replace."})
	}
	return state.NewContract(fields...)
}

func (n *StateTransformNode) GraphNodeSpec() dsl.GraphNodeSpec {
	if n == nil {
		return dsl.GraphNodeSpec{Type: NodeTypeStateTransform}
	}
	paths := make(map[string]state.Path, len(n.InputPaths)+1)
	for name, path := range n.InputPaths {
		paths[name] = path
	}
	paths["output"] = n.OutputPath
	return graphNodeSpec(n.NodeBase, NodeTypeStateTransform, map[string]any{"expression": n.Expression}, paths)
}

func sortedInputNames(paths map[string]state.Path) []string {
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
