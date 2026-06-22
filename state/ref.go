package state

import "reflect"

// Ref is a typed handle to one state path.
// It is intended to be declared once by an accessor and reused by business code.
type Ref[T any] struct {
	path        Path
	required    bool
	merge       MergeStrategy
	description string
}

// NewRef creates a typed reference to path.
func NewRef[T any](path Path) Ref[T] {
	return Ref[T]{path: path}
}

// Path returns the state path addressed by the ref.
func (r Ref[T]) Path() Path {
	return r.path
}

// Required marks the field as required when the ref is converted into a
// contract field.
func (r Ref[T]) Required() Ref[T] {
	r.required = true
	return r
}

// WithMerge sets the parallel merge strategy emitted in contract fields.
func (r Ref[T]) WithMerge(strategy MergeStrategy) Ref[T] {
	r.merge = strategy
	return r
}

// WithDescription sets human-readable contract documentation.
func (r Ref[T]) WithDescription(description string) Ref[T] {
	r.description = description
	return r
}

// ReadField converts the ref into a read-only contract field.
func (r Ref[T]) ReadField() FieldAccess {
	return r.field(AccessRead)
}

// WriteField converts the ref into a write-only contract field.
func (r Ref[T]) WriteField() FieldAccess {
	return r.field(AccessWrite)
}

// ReadWriteField converts the ref into a read-write contract field.
func (r Ref[T]) ReadWriteField() FieldAccess {
	return r.field(AccessReadWrite)
}

func (r Ref[T]) field(mode AccessMode) FieldAccess {
	return FieldAccess{
		Path:        r.path,
		Mode:        mode,
		Required:    r.required,
		Merge:       r.merge,
		Type:        typeName[T](),
		Description: r.description,
	}
}

func typeName[T any]() string {
	t := reflect.TypeOf((*T)(nil)).Elem()
	return t.String()
}
