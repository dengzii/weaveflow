package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// PatchOpKind identifies how a patch operation updates its path.
type PatchOpKind string

const (
	// OpSet replaces the value at Path.
	OpSet PatchOpKind = "set"
	// OpDelete removes the value at Path.
	OpDelete PatchOpKind = "delete"
	// OpMerge deep-merges an object value into Path.
	OpMerge PatchOpKind = "merge"
	// OpAppend appends a value or slice of values to Path.
	OpAppend PatchOpKind = "append"
	OpReduce PatchOpKind = "reduce"
)

// PatchOp is one state mutation recorded by an Editor.
type PatchOp struct {
	Kind    PatchOpKind `json:"kind"`
	Path    Path        `json:"path"`
	Value   any         `json:"value,omitempty"`
	Reducer string      `json:"reducer,omitempty"`
}

// Patch is an ordered list of state mutations. It is immutable from callers'
// perspective: constructors and readers clone operation values.
type Patch struct {
	ops []PatchOp
}

func (p Patch) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Ops())
}

func (p *Patch) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("state patch target is nil")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var operations []PatchOp
	if err := decoder.Decode(&operations); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("state patch contains multiple JSON values")
		}
		return err
	}
	for index := range operations {
		operations[index].Value = normalizeDecodedValue(operations[index].Value)
	}
	patch := NewPatch(operations...)
	if issues := ValidatePatch(patch); len(issues) > 0 {
		return fmt.Errorf("invalid state patch: %s", issues[0].Message)
	}
	*p = patch
	return nil
}

// NewPatch constructs a patch from cloned operations.
func NewPatch(ops ...PatchOp) Patch {
	return Patch{ops: clonePatchOps(ops)}
}

// Ops returns a cloned copy of patch operations.
func (p Patch) Ops() []PatchOp {
	return clonePatchOps(p.ops)
}

// Empty reports whether the patch has no operations.
func (p Patch) Empty() bool {
	return len(p.ops) == 0
}

// Apply replays the patch against a clone of base and returns the resulting
// state.
func (p Patch) Apply(base *State) (*State, error) {
	return p.ApplyWithReducers(base, nil)
}

type Reducer interface {
	Reduce(current, incoming any) (any, error)
}

func IsNilReducer(reducer Reducer) bool {
	if reducer == nil {
		return true
	}
	value := reflect.ValueOf(reducer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (p Patch) ApplyWithReducers(base *State, reducers map[string]Reducer) (*State, error) {
	target := NewState()
	if base != nil {
		target = base.Clone()
	}
	for _, op := range p.ops {
		if err := applyPatchOpWithReducers(target, op, reducers); err != nil {
			return nil, err
		}
	}
	return target, nil
}

func applyPatchOpWithReducers(target *State, op PatchOp, reducers map[string]Reducer) error {
	if target == nil {
		return fmt.Errorf("target state is nil")
	}
	if op.Path.Empty() {
		return fmt.Errorf("patch op path is required")
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
	case OpReduce:
		if op.Reducer == "" {
			return fmt.Errorf("reduce op reducer is required")
		}
		reducer := reducers[op.Reducer]
		if IsNilReducer(reducer) {
			return fmt.Errorf("unknown state reducer %q", op.Reducer)
		}
		current, _ := target.read(op.Path)
		isolatedCurrent, err := cloneReducerValue(current)
		if err != nil {
			return fmt.Errorf("state reducer %q current value at %q: %w", op.Reducer, op.Path.String(), err)
		}
		isolatedIncoming, err := cloneReducerValue(op.Value)
		if err != nil {
			return fmt.Errorf("state reducer %q incoming value at %q: %w", op.Reducer, op.Path.String(), err)
		}
		value, err := reducer.Reduce(isolatedCurrent, isolatedIncoming)
		if err != nil {
			return fmt.Errorf("state reducer %q failed at %q: %w", op.Reducer, op.Path.String(), err)
		}
		isolatedValue, err := cloneReducerValue(value)
		if err != nil {
			return fmt.Errorf("state reducer %q result at %q: %w", op.Reducer, op.Path.String(), err)
		}
		return target.set(op.Path, isolatedValue)
	default:
		return fmt.Errorf("unknown patch op kind %q", op.Kind)
	}
}

func cloneReducerValue(value any) (any, error) {
	cloned, err := CloneValue(value)
	if err != nil {
		return nil, fmt.Errorf("cannot be safely cloned: %w", err)
	}
	return cloned, nil
}

func appendPathValue(target *State, path Path, value any) error {
	existing, found := target.read(path)
	if !found {
		return target.set(path, normalizeAppendSeed(value))
	}
	combined, err := appendValue(existing, value)
	if err != nil {
		return fmt.Errorf("append path %q: %w", path.String(), err)
	}
	return target.set(path, combined)
}

func appendValue(existing any, value any) (any, error) {
	left := reflect.ValueOf(existing)
	right := reflect.ValueOf(value)
	if left.IsValid() && left.Kind() == reflect.Slice {
		if right.IsValid() && right.Kind() == reflect.Slice && right.Type() == left.Type() {
			if right.Len() > maxInt()-left.Len() {
				return nil, fmt.Errorf("append result is too large")
			}
			combined := reflect.MakeSlice(left.Type(), left.Len(), left.Len()+right.Len())
			reflect.Copy(combined, left)
			combined = reflect.AppendSlice(combined, right)
			return combined.Interface(), nil
		}
		if right.IsValid() && right.Type().AssignableTo(left.Type().Elem()) {
			combined := reflect.MakeSlice(left.Type(), left.Len(), left.Len()+1)
			reflect.Copy(combined, left)
			combined = reflect.Append(combined, right)
			return combined.Interface(), nil
		}
	}

	leftItems, ok := anySlice(existing)
	if !ok {
		return nil, fmt.Errorf("existing value has type %T, want slice", existing)
	}
	rightItems, ok := anySlice(value)
	if !ok {
		rightItems = []any{value}
	}
	if len(rightItems) > maxInt()-len(leftItems) {
		return nil, fmt.Errorf("append result is too large")
	}
	combined := make([]any, 0)
	combined = append(combined, leftItems...)
	combined = append(combined, rightItems...)
	return combined, nil
}

func normalizeAppendSeed(value any) any {
	if _, ok := anySlice(value); ok {
		return value
	}
	return []any{value}
}

func anySlice(value any) ([]any, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || reflected.Kind() != reflect.Slice || reflected.Len() > maxCollectionSize {
		return nil, false
	}
	items := make([]any, 0, reflected.Len())
	for i := 0; i < reflected.Len(); i++ {
		items = append(items, reflected.Index(i).Interface())
	}
	return items, true
}

const maxCollectionSize = 1_000_000

func maxInt() int {
	return int(^uint(0) >> 1)
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
