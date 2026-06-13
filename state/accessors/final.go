package accessors

import state "github.com/dengzii/weaveflow/state"

const FinalFieldAnswer = "answer"

type Final interface {
	Object
	Answer() string
	SetAnswer(answer string) error
}

type finalAccessor struct {
	objectAccessor
	answerRef state.Ref[string]
}

func (f finalAccessor) Answer() string {
	value, _ := state.Read(f.access, f.answerRef)
	return value
}

func (f finalAccessor) SetAnswer(answer string) error {
	return state.Set(f.access, f.answerRef, answer)
}

func registerFinal(registry *state.Registry) error {
	path := state.Shared(KeyFinal)
	answerRef := state.NewRef[string](path.MustChild(FinalFieldAnswer))
	return registry.RegisterAccessor(state.AccessorDefinition{
		Name: FinalID.Name(),
		ContractFactory: func(string) state.Contract {
			return state.NewContract(
				sharedObjectRef(KeyFinal).ReadWriteField(),
				answerRef.ReadWriteField(),
			)
		},
		Factory: func(access *state.Access) any {
			return finalAccessor{
				objectAccessor: newObjectAccessor(access, path),
				answerRef:      answerRef,
			}
		},
	})
}
