package state

import (
	"fmt"
	"reflect"
)

type PatchOpKind string

const (
	OpSet    PatchOpKind = "set"
	OpDelete PatchOpKind = "delete"
	OpMerge  PatchOpKind = "merge"
	OpAppend PatchOpKind = "append"
)

type PatchOp struct {
	Kind  PatchOpKind
	Path  Path
	Value any
}

type Patch struct {
	ops []PatchOp
}

func NewPatch(ops ...PatchOp) Patch {
	return Patch{ops: clonePatchOps(ops)}
}

func (p Patch) Ops() []PatchOp {
	return clonePatchOps(p.ops)
}

func (p Patch) Empty() bool {
	return len(p.ops) == 0
}

func (p Patch) Apply(base *State) (*State, error) {
	target := NewState()
	if base != nil {
		target = base.Clone()
	}
	for _, op := range p.ops {
		if err := applyPatchOp(target, op); err != nil {
			return nil, err
		}
	}
	return target, nil
}

func applyPatchOp(target *State, op PatchOp) error {
	if target == nil {
		return fmt.Errorf("target state is nil")
	}
	switch op.Kind {
	case OpSet:
		return target.set(op.Path, op.Value)
	case OpDelete:
		return target.delete(op.Path)
	case OpMerge:
		return target.merge(op.Path, op.Value)
	case OpAppend:
		return appendPathValue(target, op.Path, op.Value)
	default:
		return fmt.Errorf("unknown patch op kind %q", op.Kind)
	}
}

func appendPathValue(target *State, path Path, value any) error {
	existing, found := target.read(path)
	if !found {
		return target.set(path, normalizeAppendSeed(value))
	}
	return target.set(path, appendValue(existing, value))
}

func appendValue(existing any, value any) any {
	left := reflect.ValueOf(existing)
	right := reflect.ValueOf(value)
	if left.IsValid() && left.Kind() == reflect.Slice {
		if right.IsValid() && right.Kind() == reflect.Slice && right.Type() == left.Type() {
			combined := reflect.MakeSlice(left.Type(), left.Len(), left.Len()+right.Len())
			reflect.Copy(combined, left)
			combined = reflect.AppendSlice(combined, right)
			return combined.Interface()
		}
		if right.IsValid() && right.Type().AssignableTo(left.Type().Elem()) {
			combined := reflect.MakeSlice(left.Type(), left.Len(), left.Len()+1)
			reflect.Copy(combined, left)
			combined = reflect.Append(combined, right)
			return combined.Interface()
		}
	}

	leftItems, ok := anySlice(existing)
	if !ok {
		return normalizeAppendSeed(value)
	}
	rightItems, ok := anySlice(value)
	if !ok {
		rightItems = []any{value}
	}
	combined := make([]any, 0, len(leftItems)+len(rightItems))
	combined = append(combined, leftItems...)
	combined = append(combined, rightItems...)
	return combined
}

func normalizeAppendSeed(value any) any {
	if _, ok := anySlice(value); ok {
		return value
	}
	return []any{value}
}

func anySlice(value any) ([]any, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || reflected.Kind() != reflect.Slice {
		return nil, false
	}
	items := make([]any, 0, reflected.Len())
	for i := 0; i < reflected.Len(); i++ {
		items = append(items, reflected.Index(i).Interface())
	}
	return items, true
}

func clonePatchOps(ops []PatchOp) []PatchOp {
	if len(ops) == 0 {
		return nil
	}
	cloned := make([]PatchOp, len(ops))
	for i, op := range ops {
		cloned[i] = op
		cloned[i].Value = cloneValue(op.Value)
	}
	return cloned
}
