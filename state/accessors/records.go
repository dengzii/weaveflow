package accessors

import "github.com/dengzii/weaveflow/state"

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
	value, ok := r.access.ReadAny(r.path)
	if !ok {
		return nil
	}
	return recordItemsFromStateValue(value)
}

func (r recordsAccessor) Append(item map[string]any) error {
	return state.Append(r.access, r.ref, item)
}

func (r recordsAccessor) Replace(items []map[string]any) error {
	return state.Replace(r.access, r.ref, items)
}

func (r recordsAccessor) Clear() error {
	return state.Delete(r.access, r.ref)
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

func recordItemsFromStateValue(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return cloneRecordItems(typed)
	case []any:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			mapped, ok := item.(map[string]any)
			if !ok {
				return nil
			}
			items = append(items, cloneRecordItem(mapped))
		}
		return items
	case nil:
		return nil
	default:
		return nil
	}
}

func cloneRecordItems(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]map[string]any, len(items))
	for i, item := range items {
		cloned[i] = cloneRecordItem(item)
	}
	return cloned
}

func cloneRecordItem(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	cloned := make(map[string]any, len(item))
	for key, value := range item {
		cloned[key] = value
	}
	return cloned
}
