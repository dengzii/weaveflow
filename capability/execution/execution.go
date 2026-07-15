package execution

import (
	"errors"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

const (
	CapabilityID = "weaveflow.execution.v1"

	FieldCurrentStep = "current_step"
	FieldStepResults = "step_results"
	FieldLastLLMStep = "last_llm_step_id"
)

type View struct {
	access         *state.Access
	root           state.Path
	currentStepRef state.Ref[map[string]any]
	stepResultsRef state.Ref[map[string]any]
	lastLLMStepRef state.Ref[string]
}

func Definition() dsl.StateCapabilityDefinition {
	return dsl.StateCapabilityDefinition{
		ID:     CapabilityID,
		Schema: dsl.JSONSchema{"type": "object"},
		Fields: []dsl.StateCapabilityFieldDefinition{
			{Name: FieldCurrentStep, Schema: dsl.JSONSchema{"type": "object"}, MergeStrategy: dsl.StateMergeReplace},
			{Name: FieldStepResults, Schema: dsl.JSONSchema{"type": "object"}, MergeStrategy: dsl.StateMergeMerge},
			{Name: FieldLastLLMStep, Schema: dsl.JSONSchema{"type": "string"}, MergeStrategy: dsl.StateMergeReplace},
		},
	}
}

func Bind(access *state.Access, root state.Path) (*View, error) {
	if access == nil {
		return nil, errors.New("state access is nil")
	}
	if root.Empty() {
		return nil, errors.New("execution root path is required")
	}
	return &View{
		access:         access,
		root:           root,
		currentStepRef: state.NewRef[map[string]any](root.MustChild(FieldCurrentStep)).WithMerge(state.MergeReplace),
		stepResultsRef: state.NewRef[map[string]any](root.MustChild(FieldStepResults)).WithMerge(state.MergeMerge),
		lastLLMStepRef: state.NewRef[string](root.MustChild(FieldLastLLMStep)).WithMerge(state.MergeReplace),
	}, nil
}

func (v *View) Path() state.Path {
	if v == nil {
		return state.Path{}
	}
	return v.root
}

func (v *View) CurrentStep() map[string]any {
	if v == nil {
		return map[string]any{}
	}
	value, _ := state.Read(v.access, v.currentStepRef)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (v *View) SetCurrentStep(step map[string]any) error {
	if v == nil {
		return errors.New("execution view is nil")
	}
	return state.Replace(v.access, v.currentStepRef, step)
}

func (v *View) StepResults() map[string]any {
	if v == nil {
		return map[string]any{}
	}
	value, _ := state.Read(v.access, v.stepResultsRef)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (v *View) SetStepResult(stepID string, value any) error {
	if v == nil {
		return errors.New("execution view is nil")
	}
	return state.Merge(v.access, v.stepResultsRef, map[string]any{stepID: value})
}

func (v *View) LastLLMStepID() string {
	if v == nil {
		return ""
	}
	value, _ := state.Read(v.access, v.lastLLMStepRef)
	return value
}

func (v *View) SetLastLLMStepID(stepID string) error {
	if v == nil {
		return errors.New("execution view is nil")
	}
	return state.Replace(v.access, v.lastLLMStepRef, stepID)
}
