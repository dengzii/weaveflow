package accessors

import state "github.com/dengzii/weaveflow/state"

const (
	RequestFieldInput    = "input"
	RequestFieldMetadata = "metadata"
)

type Request interface {
	Object
	Input() string
	SetInput(input string) error
	Metadata() map[string]any
	SetMetadata(metadata map[string]any) error
}

type requestAccessor struct {
	objectAccessor
	inputRef    state.Ref[string]
	metadataRef state.Ref[map[string]any]
}

func (r requestAccessor) Input() string {
	value, _ := state.Read(r.access, r.inputRef)
	return value
}

func (r requestAccessor) SetInput(input string) error {
	return state.Set(r.access, r.inputRef, input)
}

func (r requestAccessor) Metadata() map[string]any {
	value, _ := state.Read(r.access, r.metadataRef)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (r requestAccessor) SetMetadata(metadata map[string]any) error {
	return state.Set(r.access, r.metadataRef, metadata)
}

func registerRequest(registry *state.Registry) error {
	path := state.Shared(KeyRequest)
	inputRef := state.NewRef[string](path.MustChild(RequestFieldInput))
	metadataRef := state.NewRef[map[string]any](path.MustChild(RequestFieldMetadata)).WithMerge(state.MergeMerge)
	return registry.RegisterAccessor(state.AccessorDefinition{
		Name: RequestID.Name(),
		ContractFactory: func(string) state.Contract {
			return state.NewContract(
				sharedObjectRef(KeyRequest).ReadWriteField(),
				inputRef.ReadWriteField(),
				metadataRef.ReadWriteField(),
			)
		},
		Factory: func(access *state.Access) any {
			return requestAccessor{
				objectAccessor: newObjectAccessor(access, path),
				inputRef:       inputRef,
				metadataRef:    metadataRef,
			}
		},
	})
}
