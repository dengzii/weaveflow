package accessors

import (
	"fmt"

	"weaveflow/state"
)

type Object interface {
	Path() state.Path
	Value() map[string]any
	Field(name string) (any, bool)
	SetField(name string, value any) error
	DeleteField(name string) error
	Merge(values map[string]any) error
	Replace(values map[string]any) error
}

type objectAccessor struct {
	access *state.Access
	path   state.Path
	ref    state.Ref[map[string]any]
}

func newObjectAccessor(access *state.Access, path state.Path) objectAccessor {
	return objectAccessor{
		access: access,
		path:   path,
		ref:    state.NewRef[map[string]any](path).WithMerge(state.MergeMerge),
	}
}

func (o objectAccessor) Path() state.Path {
	return o.path
}

func (o objectAccessor) Value() map[string]any {
	value, _ := state.Read(o.access, o.ref)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (o objectAccessor) Field(name string) (any, bool) {
	path, err := o.path.Child(name)
	if err != nil {
		return nil, false
	}
	return o.access.ReadAny(path)
}

func (o objectAccessor) SetField(name string, value any) error {
	path, err := o.path.Child(name)
	if err != nil {
		return err
	}
	return o.access.SetAny(path, value)
}

func (o objectAccessor) DeleteField(name string) error {
	path, err := o.path.Child(name)
	if err != nil {
		return err
	}
	return o.access.Delete(path)
}

func (o objectAccessor) Merge(values map[string]any) error {
	return o.access.MergeAny(o.path, values)
}

func (o objectAccessor) Replace(values map[string]any) error {
	return state.Set(o.access, o.ref.WithMerge(state.MergeReplace), values)
}

func registerObject(registry *state.Registry, name string, key string) error {
	if key == "" {
		return fmt.Errorf("object accessor %q key is required", name)
	}
	path := state.Shared(key)
	return registry.RegisterAccessor(state.AccessorDefinition{
		Name: name,
		ContractFactory: func(string) state.Contract {
			return state.NewContract(sharedObjectRef(key).ReadWriteField())
		},
		Factory: func(access *state.Access) any {
			return newObjectAccessor(access, path)
		},
	})
}
