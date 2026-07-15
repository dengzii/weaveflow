package state

import "fmt"

// Reader is the minimal read-only state surface accepted by typed refs.
type Reader interface {
	ReadAny(path Path) (any, bool)
}

// Writer is the minimal mutation surface used by typed refs and capability views.
// Implementations are expected to record explicit patch operations.
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
	state  *State
	editor *Editor
}

// NewAccess returns a read-only copy of state for inspection or condition
// evaluation. Mutating methods on the returned access fail.
func NewAccess(state *State) *Access {
	if state == nil {
		state = NewState()
	}
	return &Access{state: state.Clone()}
}

// NewEditingAccess returns a copy-on-write view over state. Mutations update
// the working copy and are captured as a Patch for replay or parallel merging.
func NewEditingAccess(state *State) *Access {
	access := NewAccess(state)
	access.editor = NewEditor(access.state)
	return access
}

// ReadAny reads a cloned value at path. The returned value can be mutated by
// the caller without changing state-owned data.
func (a *Access) ReadAny(path Path) (any, bool) {
	if a == nil {
		return nil, false
	}
	if a.editor != nil {
		return a.editor.ReadAny(path)
	}
	return a.state.read(path)
}

// SetAny records a replace operation at path.
func (a *Access) SetAny(path Path, value any) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.SetAny(path, value)
}

// Delete records a delete operation at path.
func (a *Access) Delete(path Path) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.Delete(path)
}

// MergeAny records an object merge operation at path.
func (a *Access) MergeAny(path Path, value map[string]any) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.MergeAny(path, value)
}

// AppendAny records an append operation at path. Prefer the typed Append
// helper when appending to a Ref[[]T].
func (a *Access) AppendAny(path Path, value any) error {
	if a == nil || a.editor == nil {
		return fmt.Errorf("state access is read-only")
	}
	return a.editor.AppendAny(path, value)
}

// Patch returns the structured mutations recorded by editing access.
func (a *Access) Patch() Patch {
	if a == nil || a.editor == nil {
		return Patch{}
	}
	return a.editor.Patch()
}

// State returns the current working state as a clone.
func (a *Access) State() *State {
	if a == nil {
		return NewState()
	}
	if a.editor != nil {
		return a.editor.State()
	}
	return a.state.Clone()
}

// Read returns the value at ref and false when the path is missing or the value
// has a different Go type. JSON checkpoint restore preserves JSON-compatible
// shapes, but does not reconstruct arbitrary Go slice, map, or struct types;
// capability views that expose typed values should convert restored JSON shapes
// explicitly.
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

// Get returns the value at ref or a descriptive error that distinguishes
// missing paths from type mismatches.
func Get[T any](reader Reader, ref Ref[T]) (T, error) {
	var zero T
	if reader == nil {
		return zero, fmt.Errorf("state reader is nil")
	}
	value, ok := reader.ReadAny(ref.Path())
	if !ok {
		return zero, fmt.Errorf("state path %q is missing", ref.Path().String())
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("state path %q type mismatch: got %T, want %s", ref.Path().String(), value, typeName[T]())
	}
	return typed, nil
}

// ReadRequired is the error-returning form of Read kept for call-site clarity.
func ReadRequired[T any](reader Reader, ref Ref[T]) (T, error) {
	return Get(reader, ref)
}

// Replace records a replace operation for ref.
func Replace[T any](writer Writer, ref Ref[T], value T) error {
	if writer == nil {
		return fmt.Errorf("state writer is nil")
	}
	return writer.SetAny(ref.Path(), value)
}

// Delete records a delete operation for ref.
func Delete[T any](writer Writer, ref Ref[T]) error {
	if writer == nil {
		return fmt.Errorf("state writer is nil")
	}
	return writer.Delete(ref.Path())
}

// Append records an append operation for a slice ref while preserving the
// concrete []T type when the path is initially missing.
func Append[T any](writer Writer, ref Ref[[]T], value T) error {
	if writer == nil {
		return fmt.Errorf("state writer is nil")
	}
	return writer.AppendAny(ref.Path(), []T{value})
}

// Merge records an object merge operation for a map ref.
func Merge(writer Writer, ref Ref[map[string]any], value map[string]any) error {
	if writer == nil {
		return fmt.Errorf("state writer is nil")
	}
	return writer.MergeAny(ref.Path(), value)
}

// Editor applies patch operations to a working state while recording them for
// later replay. Most node code should use Access instead.
type Editor struct {
	work *State
	ops  []PatchOp
}

// NewEditor creates an editor over a cloned copy of base.
func NewEditor(base *State) *Editor {
	work := NewState()
	if base != nil {
		work = base.Clone()
	}
	return &Editor{work: work}
}

// ReadAny reads from the editor's working state.
func (e *Editor) ReadAny(path Path) (any, bool) {
	if e == nil || e.work == nil {
		return nil, false
	}
	return e.work.read(path)
}

// SetAny applies and records a replace operation.
func (e *Editor) SetAny(path Path, value any) error {
	return e.apply(PatchOp{Kind: OpSet, Path: path, Value: value})
}

// Delete applies and records a delete operation.
func (e *Editor) Delete(path Path) error {
	return e.apply(PatchOp{Kind: OpDelete, Path: path})
}

// MergeAny applies and records an object merge operation.
func (e *Editor) MergeAny(path Path, value map[string]any) error {
	return e.apply(PatchOp{Kind: OpMerge, Path: path, Value: value})
}

// AppendAny applies and records an append operation.
func (e *Editor) AppendAny(path Path, value any) error {
	return e.apply(PatchOp{Kind: OpAppend, Path: path, Value: value})
}

// Patch returns a clone of the operations recorded so far.
func (e *Editor) Patch() Patch {
	if e == nil {
		return Patch{}
	}
	return NewPatch(e.ops...)
}

// State returns a clone of the editor's working state.
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
