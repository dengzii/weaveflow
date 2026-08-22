// Package tiny_script implements a small typed scripting language with
// static type checking and a tree-walking interpreter.
package tinyscript

import (
	"fmt"
)

// Pos represents a source position (line, column).
type Pos struct {
	Line int // 1-based line number
	Col  int // 0-based column offset
}

func (p Pos) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Col)
}

// Type enumerates the supported value types.
type Type int

const (
	TypeInvalid Type = iota
	TypeInt
	TypeBool
	TypeString
)

func (t Type) String() string {
	switch t {
	case TypeInvalid:
		return "<invalid>"
	case TypeInt:
		return "int"
	case TypeBool:
		return "bool"
	case TypeString:
		return "string"
	default:
		return fmt.Sprintf("<type %d>", int(t))
	}
}

// ParseType parses a type name.
func ParseType(s string) (Type, error) {
	switch s {
	case "int":
		return TypeInt, nil
	case "bool":
		return TypeBool, nil
	case "string":
		return TypeString, nil
	default:
		return TypeInvalid, fmt.Errorf("unknown type %q", s)
	}
}

// Value holds a typed runtime value.
type Value struct {
	Type Type
	Int  int64
	Bool bool
	Str  string
}

// NewIntValue creates an integer value.
func NewIntValue(v int64) Value { return Value{Type: TypeInt, Int: v} }

// NewBoolValue creates a boolean value.
func NewBoolValue(v bool) Value { return Value{Type: TypeBool, Bool: v} }

// NewStringValue creates a string value.
func NewStringValue(v string) Value { return Value{Type: TypeString, Str: v} }

// String returns the string representation of the value.
func (v Value) String() string {
	switch v.Type {
	case TypeInt:
		return fmt.Sprintf("%d", v.Int)
	case TypeBool:
		return fmt.Sprintf("%t", v.Bool)
	case TypeString:
		return v.Str
	default:
		return "<invalid>"
	}
}
