package accessors

import state "github.com/dengzii/weaveflow/state"

const (
	ExecutionFieldCurrentStep = "current_step"
	ExecutionFieldStepResults = "step_results"
	ExecutionFieldLastLLMStep = "last_llm_step_id"
)

type Execution interface {
	Object
	CurrentStep() map[string]any
	SetCurrentStep(step map[string]any) error
	StepResults() map[string]any
	SetStepResult(stepID string, value any) error
	LastLLMStepID() string
	SetLastLLMStepID(stepID string) error
}

type executionAccessor struct {
	objectAccessor
	currentStepRef state.Ref[map[string]any]
	stepResultsRef state.Ref[map[string]any]
	lastLLMStepRef state.Ref[string]
}

func (e executionAccessor) CurrentStep() map[string]any {
	value, _ := state.Read(e.access, e.currentStepRef)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (e executionAccessor) SetCurrentStep(step map[string]any) error {
	return state.Replace(e.access, e.currentStepRef, step)
}

func (e executionAccessor) StepResults() map[string]any {
	value, _ := state.Read(e.access, e.stepResultsRef)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (e executionAccessor) SetStepResult(stepID string, value any) error {
	path, err := e.stepResultsRef.Path().Child(stepID)
	if err != nil {
		return err
	}
	return e.access.SetAny(path, value)
}

func (e executionAccessor) LastLLMStepID() string {
	value, _ := state.Read(e.access, e.lastLLMStepRef)
	return value
}

func (e executionAccessor) SetLastLLMStepID(stepID string) error {
	return state.Replace(e.access, e.lastLLMStepRef, stepID)
}

func registerExecution(registry *state.Registry) error {
	path := state.Shared(KeyExecution)
	currentStepRef := state.NewRef[map[string]any](path.MustChild(ExecutionFieldCurrentStep)).WithMerge(state.MergeMerge)
	stepResultsRef := state.NewRef[map[string]any](path.MustChild(ExecutionFieldStepResults)).WithMerge(state.MergeMerge)
	lastLLMStepRef := state.NewRef[string](path.MustChild(ExecutionFieldLastLLMStep))
	return registry.RegisterAccessor(state.AccessorDefinition{
		Name: ExecutionID.Name(),
		ContractFactory: func(string) state.Contract {
			return state.NewContract(
				sharedObjectRef(KeyExecution).ReadWriteField(),
				currentStepRef.ReadWriteField(),
				stepResultsRef.ReadWriteField(),
				lastLLMStepRef.ReadWriteField(),
			)
		},
		Factory: func(access *state.Access) any {
			return executionAccessor{
				objectAccessor: newObjectAccessor(access, path),
				currentStepRef: currentStepRef,
				stepResultsRef: stepResultsRef,
				lastLLMStepRef: lastLLMStepRef,
			}
		},
	})
}
