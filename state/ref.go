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

func NewRef[T any](path Path) Ref[T] {
	return Ref[T]{path: path}
}

func (r Ref[T]) Path() Path {
	return r.path
}

func (r Ref[T]) Required() Ref[T] {
	r.required = true
	return r
}

func (r Ref[T]) WithMerge(strategy MergeStrategy) Ref[T] {
	r.merge = strategy
	return r
}

func (r Ref[T]) WithDescription(description string) Ref[T] {
	r.description = description
	return r
}

func (r Ref[T]) ReadField() FieldAccess {
	return r.field(AccessRead)
}

func (r Ref[T]) WriteField() FieldAccess {
	return r.field(AccessWrite)
}

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
