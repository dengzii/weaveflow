package accessors

import state "weaveflow/state"

type Records interface {
	Path() state.Path
	Items() []map[string]any
	Append(item map[string]any) error
	Replace(items []map[string]any) error
	Clear() error
}

type recordsAccessor struct {
	access *state.Access
	path   state.Path
	ref    state.Ref[[]map[string]any]
}

func (r recordsAccessor) Path() state.Path {
	return r.path
}

func (r recordsAccessor) Items() []map[string]any {
	items, _ := state.Read(r.access, r.ref)
	if items == nil {
		return nil
	}
	return items
}

func (r recordsAccessor) Append(item map[string]any) error {
	items := append(r.Items(), item)
	return state.Set(r.access, r.ref, items)
}

func (r recordsAccessor) Replace(items []map[string]any) error {
	return state.Set(r.access, r.ref, items)
}

func (r recordsAccessor) Clear() error {
	return r.access.Delete(r.path)
}

func registerRecords(registry *state.Registry, name string, key string) error {
	path := state.Shared(key)
	ref := state.NewRef[[]map[string]any](path).WithMerge(state.MergeAppend)
	return registry.RegisterAccessor(state.AccessorDefinition{
		Name: name,
		ContractFactory: func(string) state.Contract {
			return state.NewContract(ref.ReadWriteField())
		},
		Factory: func(access *state.Access) any {
			return recordsAccessor{
				access: access,
				path:   path,
				ref:    ref,
			}
		},
	})
}
