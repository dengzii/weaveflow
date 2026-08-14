package state

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
)

type SumReducer struct{}

func (SumReducer) Reduce(current, incoming any) (any, error) {
	left, leftOK := parseNumericValue(current)
	right, rightOK := parseNumericValue(incoming)
	if current != nil && !leftOK {
		return nil, fmt.Errorf("current value %T is not numeric", current)
	}
	if !rightOK {
		return nil, fmt.Errorf("incoming value %T is not numeric", incoming)
	}
	if left.integer != nil && right.integer != nil {
		return normalizeInteger(new(big.Int).Add(left.integer, right.integer)), nil
	}
	return left.float64() + right.float64(), nil
}

type MaxReducer struct{}

func (MaxReducer) Reduce(current, incoming any) (any, error) {
	if current == nil {
		return incoming, nil
	}
	left, leftOK := parseNumericValue(current)
	right, rightOK := parseNumericValue(incoming)
	if !leftOK || !rightOK {
		return nil, fmt.Errorf("max reducer requires numeric values")
	}
	if compareNumericValues(left, right) >= 0 {
		return current, nil
	}
	return incoming, nil
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
	if current == nil {
		return normalizeAppendSeed(incoming), nil
	}
	return appendValue(current, incoming)
}

type numericValue struct {
	integer  *big.Int
	floating float64
}

func parseNumericValue(value any) (numericValue, bool) {
	if value == nil {
		return numericValue{integer: new(big.Int)}, true
	}
	switch typed := value.(type) {
	case json.Number:
		return parseJSONNumber(typed)
	case big.Int:
		return numericValue{integer: new(big.Int).Set(&typed)}, true
	case *big.Int:
		if typed == nil {
			return numericValue{}, false
		}
		return numericValue{integer: new(big.Int).Set(typed)}, true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return numericValue{integer: big.NewInt(reflected.Int())}, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		integer := new(big.Int).SetUint64(reflected.Uint())
		return numericValue{integer: integer}, true
	case reflect.Float32, reflect.Float64:
		return numericValue{floating: reflected.Float()}, true
	default:
		return numericValue{}, false
	}
}

func parseJSONNumber(value json.Number) (numericValue, bool) {
	rational, ok := new(big.Rat).SetString(value.String())
	if !ok {
		return numericValue{}, false
	}
	if rational.IsInt() {
		return numericValue{integer: new(big.Int).Set(rational.Num())}, true
	}
	floating, err := strconv.ParseFloat(value.String(), 64)
	return numericValue{floating: floating}, err == nil
}

func (value numericValue) float64() float64 {
	if value.integer == nil {
		return value.floating
	}
	floating, _ := new(big.Float).SetInt(value.integer).Float64()
	return floating
}

func compareNumericValues(left, right numericValue) int {
	if left.integer != nil && right.integer != nil {
		return left.integer.Cmp(right.integer)
	}
	leftRational, leftOK := numericRational(left)
	rightRational, rightOK := numericRational(right)
	if leftOK && rightOK {
		return leftRational.Cmp(rightRational)
	}
	leftFloat := left.float64()
	rightFloat := right.float64()
	if leftFloat > rightFloat {
		return 1
	}
	if leftFloat < rightFloat {
		return -1
	}
	return 0
}

func numericRational(value numericValue) (*big.Rat, bool) {
	if value.integer != nil {
		return new(big.Rat).SetInt(value.integer), true
	}
	if math.IsNaN(value.floating) || math.IsInf(value.floating, 0) {
		return nil, false
	}
	return new(big.Rat).SetFloat64(value.floating), true
}

func normalizeInteger(value *big.Int) any {
	if value.IsInt64() {
		integer := value.Int64()
		if integer >= minIntValue() && integer <= maxIntValue() {
			return int(integer)
		}
		return integer
	}
	if value.Sign() >= 0 && value.IsUint64() {
		return value.Uint64()
	}
	return new(big.Int).Set(value)
}
