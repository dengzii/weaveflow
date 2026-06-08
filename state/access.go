package state

import "fmt"

type Reader interface {
	ReadAny(path Path) (any, bool)
}

type Writer interface {
	SetAny(path Path, value any) error
	Delete(path Path) error
	MergeAny(path Path, value map[string]any) error
	AppendAny(path Path, value any) error
}

// Access is the node-facing state API. Read-only access wraps a State directly;
// editing access records structured patch ops and applies them to a working copy
// so later reads observe earlier writes.
type Access struct {
	registry *Registry
	state    *State
	editor   *Editor
	scope    string
}

func NewAccess(registry *Registry, state *State) *Access {
	if registry == nil {
		registry = NewRegistry()
	}
	if state == nil {
		state = NewState()
	}
	return &Access{registry: registry, state: state.Clone()}
}

func NewEditingAccess(registry *Registry, state *State) *Access {
	access := NewAccess(registry, state)
	access.editor = NewEditor(access.state)
	return access
}

func (a *Access) WithScope(scope string) *Access {
	if a == nil {
		return nil
	}
	cloned := *a
	cloned.scope = normalizeSegment(scope)
	return &cloned
}

func (a *Access) Scope() string {
	if a == nil {
		return ""
	}
	return a.scope
}

func (a *Access) Registry() *Registry {
	if a == nil || a.registry == nil {
		return NewRegistry()
	}
	return a.registry
}

func (a *Access) ReadAny(path Path) (any, bool) {
	if a == nil {
		return nil, false
	}
	if a.editor != nil {
		return a.editor.ReadAny(path)
	}
	return a.state.read(path)
}

func (a *Access) SetAny(path Path, value any) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.SetAny(path, value)
}

func (a *Access) Delete(path Path) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.Delete(path)
}

func (a *Access) MergeAny(path Path, value map[string]any) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.MergeAny(path, value)
}

func (a *Access) AppendAny(path Path, value any) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.AppendAny(path, value)
}

func (a *Access) Patch() Patch {
	if a == nil || a.editor == nil {
		return Patch{}
	}
	return a.editor.Patch()
}

func (a *Access) State() *State {
	if a == nil {
		return NewState()
	}
	if a.editor != nil {
		return a.editor.State()
	}
	return a.state.Clone()
}

func Read[T any](reader Reader, ref Ref[T]) (T, bool) {
	var zero T
	if reader == nil {
		return zero, false
	}
	value, ok := reader.ReadAny(ref.Path())
	if !ok {
		return zero, false
	}
	typed, ok := value.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

func ReadRequired[T any](reader Reader, ref Ref[T]) (T, error) {
	value, ok := Read(reader, ref)
	if !ok {
		var zero T
		return zero, fmt.Errorf("required state path %q is missing or has incompatible type %s", ref.Path().String(), typeName[T]())
	}
	return value, nil
}

func Set[T any](writer Writer, ref Ref[T], value T) error {
	if writer == nil {
		return fmt.Errorf("state writer is nil")
	}
	return writer.SetAny(ref.Path(), value)
}

type Editor struct {
	work *State
	ops  []PatchOp
}

func NewEditor(base *State) *Editor {
	work := NewState()
	if base != nil {
		work = base.Clone()
	}
	return &Editor{work: work}
}

func (e *Editor) ReadAny(path Path) (any, bool) {
	if e == nil || e.work == nil {
		return nil, false
	}
	return e.work.read(path)
}

func (e *Editor) SetAny(path Path, value any) error {
	return e.apply(PatchOp{Kind: OpSet, Path: path, Value: value})
}

func (e *Editor) Delete(path Path) error {
	return e.apply(PatchOp{Kind: OpDelete, Path: path})
}

func (e *Editor) MergeAny(path Path, value map[string]any) error {
	return e.apply(PatchOp{Kind: OpMerge, Path: path, Value: value})
}

func (e *Editor) AppendAny(path Path, value any) error {
	return e.apply(PatchOp{Kind: OpAppend, Path: path, Value: value})
}

func (e *Editor) Patch() Patch {
	if e == nil {
		return Patch{}
	}
	return NewPatch(e.ops...)
}

func (e *Editor) State() *State {
	if e == nil || e.work == nil {
		return NewState()
	}
	return e.work.Clone()
}

func (e *Editor) apply(op PatchOp) error {
	if e == nil {
		return fmt.Errorf("state editor is nil")
	}
	if e.work == nil {
		e.work = NewState()
	}
	if err := applyPatchOp(e.work, op); err != nil {
		return err
	}
	op.Value = cloneValue(op.Value)
	e.ops = append(e.ops, op)
	return nil
}
