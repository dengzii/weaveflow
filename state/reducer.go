package state

import (
	"fmt"
	"reflect"
)

type SumReducer struct{}

func (SumReducer) Reduce(current, incoming any) (any, error) {
	left, leftOK := numericValue(current)
	right, rightOK := numericValue(incoming)
	if current != nil && !leftOK {
		return nil, fmt.Errorf("current value %T is not numeric", current)
	}
	if !rightOK {
		return nil, fmt.Errorf("incoming value %T is not numeric", incoming)
	}
	return left + right, nil
}

type MaxReducer struct{}

func (MaxReducer) Reduce(current, incoming any) (any, error) {
	if current == nil {
		return incoming, nil
	}
	left, leftOK := numericValue(current)
	right, rightOK := numericValue(incoming)
	if !leftOK || !rightOK {
		return nil, fmt.Errorf("max reducer requires numeric values")
	}
	if left >= right {
		return left, nil
	}
	return right, nil
}

type MessagesReducer struct{}

func (MessagesReducer) Reduce(current, incoming any) (any, error) {
	_, leftOK := anySlice(current)
	if current != nil && !leftOK {
		return nil, fmt.Errorf("messages reducer requires a slice")
	}
	_, ok := anySlice(incoming)
	if incoming != nil && !ok {
		return nil, fmt.Errorf("messages reducer requires a slice")
	}
	return appendValue(current, incoming), nil
}

func numericValue(value any) (float64, bool) {
	if value == nil {
		return 0, true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflected.Uint()), true
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	default:
		return 0, false
	}
}
