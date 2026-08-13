package node

import (
	"fmt"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/state"
)

type SetFinalAnswerNode struct {
	Base
	Answer      string
	FromRequest bool
	InputPath   state.Path
	OutputPath  state.Path
}

func NewSetFinalAnswerNode(answer string, options ...NodeOption) *SetFinalAnswerNode {
	target := &SetFinalAnswerNode{Base: NewBase(Spec{Name: "set_final_answer", Description: "Write a final answer."}), Answer: answer}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func NewRequestToFinalAnswerNode(options ...NodeOption) *SetFinalAnswerNode {
	target := &SetFinalAnswerNode{Base: NewBase(Spec{Name: "request_to_final_answer", Description: "Copy an input value into the final answer."}), FromRequest: true}
	applyNodeOptions(&target.Base, options)
	ApplyDefaultStatePaths(target)
	return target
}

func (n *SetFinalAnswerNode) Validate() error {
	if n == nil {
		return fmt.Errorf("set final answer node is nil")
	}
	if err := n.Base.Validate(); err != nil {
		return err
	}
	if n.OutputPath.Empty() {
		return fmt.Errorf("set final answer node %q requires output path", n.ID())
	}
	if n.FromRequest && n.InputPath.Empty() {
		return fmt.Errorf("set final answer node %q requires input path", n.ID())
	}
	return nil
}

func (n *SetFinalAnswerNode) Execute(ctx core.Context, access *state.Access) (core.NodeResult, error) {
	return core.NodeResult{}, n.execute(ctx, access)
}

func (n *SetFinalAnswerNode) execute(_ core.Context, access *state.Access) error {
	answer := n.Answer
	if n.FromRequest {
		value, err := state.Get(access, state.NewRef[string](n.InputPath))
		if err != nil {
			return err
		}
		answer = value
	}
	return state.Replace(access, state.NewRef[string](n.OutputPath), answer)
}

func (n *SetFinalAnswerNode) Contract() state.Contract {
	if n == nil {
		return state.Contract{}
	}
	fields := []state.FieldAccess{{Path: n.OutputPath, Mode: state.AccessWrite, Merge: state.MergeReplace, Type: "string"}}
	if n.FromRequest {
		fields = append(fields, state.FieldAccess{Path: n.InputPath, Mode: state.AccessRead, Required: true, Merge: state.MergeReplace, Type: "string"})
	}
	return state.NewContract(fields...)
}
