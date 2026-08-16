package state

import (
	"fmt"
	"math/big"
	"reflect"
	"time"
)

var (
	bigFloatType = reflect.TypeOf(big.Float{})
	bigIntType   = reflect.TypeOf(big.Int{})
	bigRatType   = reflect.TypeOf(big.Rat{})
	patchType    = reflect.TypeOf(Patch{})
	timeType     = reflect.TypeOf(time.Time{})
)

type cloneVisit struct {
	kind      reflect.Kind
	valueType reflect.Type
	pointer   uintptr
	length    int
	capacity  int
}

type valueCloner struct {
	visited         map[cloneVisit]reflect.Value
	unsupportedType reflect.Type
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	clonedValue, _ := CloneValue(input)
	cloned, _ := clonedValue.(map[string]any)
	return cloned
}

// CloneValue returns a cycle-safe best-effort copy of value. Pointer and map
// identity, plus repeated references to the same slice view, are preserved
// within the copied object graph, including cycles. Distinct overlapping slice
// views are copied independently and need not share backing storage. The
// returned error means full isolation could not be proven because the graph
// contains an opaque reference value. In that case the returned copy may retain
// aliases and must not be exposed as an isolated view.
func CloneValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	cloner := valueCloner{visited: make(map[cloneVisit]reflect.Value)}
	cloned, safe := cloner.clone(reflect.ValueOf(value))
	if !cloned.IsValid() {
		return nil, nil
	}
	result := cloned.Interface()
	if !safe {
		return result, fmt.Errorf("value contains opaque reference type %s that cannot be safely cloned", cloner.unsupportedType)
	}
	return result, nil
}

// cloneValue is the state package's best-effort copy boundary. State accepts
// arbitrary Go values, so callers that require proven isolation must use
// CloneValue and handle its error instead.
func cloneValue(value any) any {
	cloned, _ := CloneValue(value)
	return cloned
}

func (cloner *valueCloner) clone(src reflect.Value) (reflect.Value, bool) {
	if !src.IsValid() {
		return src, true
	}
	switch src.Type() {
	case bigFloatType:
		value := src.Interface().(big.Float)
		return reflect.ValueOf(*new(big.Float).Copy(&value)), true
	case bigIntType:
		value := src.Interface().(big.Int)
		return reflect.ValueOf(*new(big.Int).Set(&value)), true
	case bigRatType:
		value := src.Interface().(big.Rat)
		return reflect.ValueOf(*new(big.Rat).Set(&value)), true
	case patchType:
		patch := src.Interface().(Patch)
		operations := make([]PatchOp, len(patch.ops))
		safe := true
		for index, operation := range patch.ops {
			operations[index] = operation
			operations[index].Path = Path{
				section:  operation.Path.section,
				segments: append([]string(nil), operation.Path.segments...),
			}
			value, valueSafe := cloner.clone(reflect.ValueOf(operation.Value))
			if value.IsValid() {
				operations[index].Value = value.Interface()
			} else {
				operations[index].Value = nil
			}
			safe = safe && valueSafe
		}
		return reflect.ValueOf(Patch{ops: operations}), safe
	case timeType:
		return src, true
	}
	switch src.Kind() {
	case reflect.Ptr:
		if src.IsNil() {
			return src, true
		}
		visit := cloneVisit{kind: src.Kind(), valueType: src.Type(), pointer: src.Pointer()}
		if cloned, ok := cloner.visited[visit]; ok {
			return cloned, true
		}
		cloned := reflect.New(src.Type().Elem())
		cloner.visited[visit] = cloned
		element, safe := cloner.clone(src.Elem())
		cloned.Elem().Set(element)
		return cloned, safe
	case reflect.Interface:
		if src.IsNil() {
			return reflect.Zero(src.Type()), true
		}
		concrete, safe := cloner.clone(src.Elem())
		wrapper := reflect.New(src.Type()).Elem()
		wrapper.Set(concrete)
		return wrapper, safe
	case reflect.Slice:
		if src.IsNil() {
			return src, true
		}
		visit := cloneVisit{
			kind:      src.Kind(),
			valueType: src.Type(),
			pointer:   src.Pointer(),
			length:    src.Len(),
			capacity:  src.Cap(),
		}
		if cloned, ok := cloner.visited[visit]; ok {
			return cloned, true
		}
		cloned := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		cloner.visited[visit] = cloned
		safe := true
		for index := 0; index < src.Len(); index++ {
			item, itemSafe := cloner.clone(src.Index(index))
			cloned.Index(index).Set(item)
			safe = safe && itemSafe
		}
		return cloned, safe
	case reflect.Array:
		cloned := reflect.New(src.Type()).Elem()
		safe := true
		for index := 0; index < src.Len(); index++ {
			item, itemSafe := cloner.clone(src.Index(index))
			cloned.Index(index).Set(item)
			safe = safe && itemSafe
		}
		return cloned, safe
	case reflect.Map:
		if src.IsNil() {
			return src, true
		}
		visit := cloneVisit{kind: src.Kind(), valueType: src.Type(), pointer: src.Pointer()}
		if cloned, ok := cloner.visited[visit]; ok {
			return cloned, true
		}
		cloned := reflect.MakeMapWithSize(src.Type(), src.Len())
		cloner.visited[visit] = cloned
		safe := true
		iter := src.MapRange()
		for iter.Next() {
			key, keySafe := cloner.clone(iter.Key())
			value, valueSafe := cloner.clone(iter.Value())
			cloned.SetMapIndex(key, value)
			safe = safe && keySafe && valueSafe
		}
		return cloned, safe
	case reflect.Struct:
		typeInfo := src.Type()
		for index := 0; index < typeInfo.NumField(); index++ {
			fieldInfo := typeInfo.Field(index)
			if fieldInfo.PkgPath != "" && !isShallowCopyIsolated(fieldInfo.Type) {
				cloner.markUnsupported(typeInfo)
				return src, false
			}
		}
		cloned := reflect.New(typeInfo).Elem()
		cloned.Set(src)
		safe := true
		for index := 0; index < src.NumField(); index++ {
			if typeInfo.Field(index).PkgPath != "" {
				continue
			}
			field, fieldSafe := cloner.clone(src.Field(index))
			cloned.Field(index).Set(field)
			safe = safe && fieldSafe
		}
		return cloned, safe
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if src.IsNil() {
			return src, true
		}
		cloner.markUnsupported(src.Type())
		return src, false
	default:
		return src, true
	}
}

func (cloner *valueCloner) markUnsupported(typeInfo reflect.Type) {
	if cloner.unsupportedType == nil {
		cloner.unsupportedType = typeInfo
	}
}

func isShallowCopyIsolated(typeInfo reflect.Type) bool {
	if typeInfo == timeType {
		return true
	}
	switch typeInfo.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.String:
		return true
	case reflect.Array:
		return isShallowCopyIsolated(typeInfo.Elem())
	case reflect.Struct:
		for index := 0; index < typeInfo.NumField(); index++ {
			if !isShallowCopyIsolated(typeInfo.Field(index).Type) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func mergeMap(target map[string]any, overlay map[string]any) {
	if target == nil || overlay == nil {
		return
	}
	for key, value := range overlay {
		if existing, ok := target[key].(map[string]any); ok {
			if next, ok := value.(map[string]any); ok {
				mergeMap(existing, next)
				continue
			}
		}
		target[key] = cloneValue(value)
	}
}

func asMap(value any) (map[string]any, bool) {
	typed, ok := value.(map[string]any)
	return typed, ok
}
