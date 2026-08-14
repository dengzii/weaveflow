package state

import (
	"math/big"
	"reflect"
)

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

// cloneValue returns a deep copy of value so callers can mutate the result
// without affecting state-owned data. Primitives short-circuit; common
// containers are handled directly; everything else falls through to a
// reflection walk that clones pointers, interfaces, slices, arrays, maps, and
// structs whose fields are all exported. Structs with any unexported field are
// treated as opaque shared references — safe for value-semantic types such as
// time.Time, but callers storing types with mutable unexported state must
// clone before handing the value to state. Cycles are not detected.
func cloneValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case big.Int:
		return *new(big.Int).Set(&typed)
	case *big.Int:
		if typed == nil {
			return (*big.Int)(nil)
		}
		return new(big.Int).Set(typed)
	case bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, uintptr,
		float32, float64, complex64, complex128:
		return typed
	case map[string]any:
		return cloneMap(typed)
	case []any:
		if typed == nil {
			return []any(nil)
		}
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneValue(item)
		}
		return cloned
	case []string:
		if typed == nil {
			return []string(nil)
		}
		return append([]string(nil), typed...)
	case []byte:
		if typed == nil {
			return []byte(nil)
		}
		return append([]byte(nil), typed...)
	}
	cloned := cloneReflect(reflect.ValueOf(value))
	if !cloned.IsValid() {
		return value
	}
	return cloned.Interface()
}

func cloneReflect(src reflect.Value) reflect.Value {
	if !src.IsValid() {
		return src
	}
	switch src.Kind() {
	case reflect.Ptr:
		if src.IsNil() {
			return src
		}
		clone := reflect.New(src.Type().Elem())
		clone.Elem().Set(cloneReflect(src.Elem()))
		return clone
	case reflect.Interface:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		concrete := cloneReflect(src.Elem())
		wrapper := reflect.New(src.Type()).Elem()
		wrapper.Set(concrete)
		return wrapper
	case reflect.Slice:
		if src.IsNil() {
			return src
		}
		clone := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			clone.Index(i).Set(cloneReflect(src.Index(i)))
		}
		return clone
	case reflect.Array:
		clone := reflect.New(src.Type()).Elem()
		for i := 0; i < src.Len(); i++ {
			clone.Index(i).Set(cloneReflect(src.Index(i)))
		}
		return clone
	case reflect.Map:
		if src.IsNil() {
			return src
		}
		clone := reflect.MakeMapWithSize(src.Type(), src.Len())
		iter := src.MapRange()
		for iter.Next() {
			clone.SetMapIndex(cloneReflect(iter.Key()), cloneReflect(iter.Value()))
		}
		return clone
	case reflect.Struct:
		t := src.Type()
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				return src
			}
		}
		clone := reflect.New(t).Elem()
		for i := 0; i < src.NumField(); i++ {
			clone.Field(i).Set(cloneReflect(src.Field(i)))
		}
		return clone
	default:
		return src
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
