package tinyscript

import (
	"strings"
	"testing"
)

func TestExecuteLanguageFeatures(t *testing.T) {
	source := `
func sumTo(limit int) int {
    var total int = 0;
    var current int = 0;
    while current < limit {
        total = total + current;
        current = current + 1;
    }
    return total;
}

func choose(flag bool, yes string, no string) string {
    if flag {
        return yes;
    } else {
        return no;
    }
}

var result int = sumTo(6);
var valid bool = result == 15 && true;
var message string = choose(valid, "passed", "failed");
if message != "passed" {
    result = 0;
}
`

	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	tests := []struct {
		name string
		got  Value
		want Value
	}{
		{name: "integer loop result", got: result.Globals["result"], want: NewIntValue(15)},
		{name: "boolean expression", got: result.Globals["valid"], want: NewBoolValue(true)},
		{name: "typed function result", got: result.Globals["message"], want: NewStringValue("passed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("value = %#v, want %#v", test.got, test.want)
			}
		})
	}
}

func TestTypeErrorsIncludeSourceLocations(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "assignment mismatch",
			source: "var count int = 1;\ncount = \"wrong\";",
			want:   []string{"at 2:0", "type mismatch in assignment"},
		},
		{
			name:   "function argument mismatch",
			source: "func twice(value int) int { return value * 2; }\nvar result int = twice(false);",
			want:   []string{"at 2:23", "expected int, got bool"},
		},
		{
			name:   "return mismatch",
			source: "func invalid() int { return \"wrong\"; }",
			want:   []string{"at 1:21", "return type mismatch"},
		},
		{
			name:   "non boolean condition",
			source: "if 1 { var value int = 1; }",
			want:   []string{"at 1:3", "if condition must be boolean"},
		},
		{
			name:   "non boolean while condition",
			source: "while 1 { var x int = 1; }",
			want:   []string{"at 1:6", "while condition must be boolean"},
		},
		{
			name:   "undefined variable",
			source: "var x int = y;",
			want:   []string{"at 1:12", "undefined variable"},
		},
		{
			name:   "var decl type mismatch",
			source: "var x int = true;",
			want:   []string{"at 1:0", "type mismatch in variable"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Execute(test.source)
			if err == nil {
				t.Fatal("Execute succeeded, want a static type error")
			}
			for _, fragment := range test.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Fatalf("error = %q, want fragment %q", err, fragment)
				}
			}
		})
	}
}

func TestExpressionPrecedenceAndRuntimeErrors(t *testing.T) {
	result, err := Execute("var result int = 2 + 3 * 4; var flag bool = false && (1 / 0 == 0);")
	if err != nil {
		t.Fatalf("execute precedence and short circuit: %v", err)
	}
	if got := result.Globals["result"]; got != NewIntValue(14) {
		t.Fatalf("result = %#v, want 14", got)
	}

	_, err = Execute("var result int = 10 / (3 - 3);")
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("error = %v, want division by zero", err)
	}
}

func TestParseErrorsIncludeSourceLocations(t *testing.T) {
	_, err := Execute("var result int = @;")
	if err == nil || !strings.Contains(err.Error(), "at 1:17") || !strings.Contains(err.Error(), "unexpected character") {
		t.Fatalf("error = %v, want located invalid-token error", err)
	}
}

func TestBooleanVarDecl(t *testing.T) {
	result, err := Execute("var flag bool = true;")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["flag"]; got != NewBoolValue(true) {
		t.Fatalf("flag = %#v, want true", got)
	}
}

func TestStringVarDecl(t *testing.T) {
	result, err := Execute(`var msg string = "hello";`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["msg"]; got != NewStringValue("hello") {
		t.Fatalf("msg = %#v, want hello", got)
	}
}

func TestUnaryNegation(t *testing.T) {
	result, err := Execute("var x int = -5;")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["x"]; got != NewIntValue(-5) {
		t.Fatalf("x = %#v, want -5", got)
	}
}

func TestComparisonOperators(t *testing.T) {
	source := `
var eq bool = 2 + 2 == 4;
var ne bool = 2 + 2 != 5;
var lt bool = 1 < 2;
var le bool = 2 <= 2;
var gt bool = 3 > 2;
var ge bool = 3 >= 3;
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	tests := []struct {
		name string
		got  Value
		want Value
	}{
		{"==", result.Globals["eq"], NewBoolValue(true)},
		{"!=", result.Globals["ne"], NewBoolValue(true)},
		{"<", result.Globals["lt"], NewBoolValue(true)},
		{"<=", result.Globals["le"], NewBoolValue(true)},
		{">", result.Globals["gt"], NewBoolValue(true)},
		{">=", result.Globals["ge"], NewBoolValue(true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %#v, want %#v", test.got, test.want)
			}
		})
	}
}

func TestIfElseFlow(t *testing.T) {
	source := `
var result string = "none";
if false {
    result = "if";
} else {
    result = "else";
}
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["result"]; got != NewStringValue("else") {
		t.Fatalf("result = %#v, want else", got)
	}
}

func TestElseIfChain(t *testing.T) {
	source := `
var result string = "none";
var x int = 2;
if x == 1 {
    result = "one";
} else if x == 2 {
    result = "two";
} else {
    result = "other";
}
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["result"]; got != NewStringValue("two") {
		t.Fatalf("result = %#v, want two", got)
	}
}

func TestWhileLoopZeroIterations(t *testing.T) {
	source := `
var count int = 0;
while false {
    count = count + 1;
}
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["count"]; got != NewIntValue(0) {
		t.Fatalf("count = %#v, want 0", got)
	}
}

func TestStringConcatenationNotSupported(t *testing.T) {
	_, err := Execute(`var s string = "hello" + "world";`)
	if err == nil || !strings.Contains(err.Error(), "type error") {
		t.Fatalf("expected type error for string concatenation, got: %v", err)
	}
}

func TestScopeIsolation(t *testing.T) {
	source := `
var outer int = 1;
if true {
    var inner int = 2;
    outer = outer + inner;
}
// inner should not be accessible here; we'll verify by seeing if outer is 3
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["outer"]; got != NewIntValue(3) {
		t.Fatalf("outer = %#v, want 3", got)
	}
}

func TestMultipleParameters(t *testing.T) {
	source := `
func add3(a int, b int, c int) int {
    return a + b + c;
}
var result int = add3(1, 2, 3);
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["result"]; got != NewIntValue(6) {
		t.Fatalf("result = %#v, want 6", got)
	}
}

func TestFuncReturningBool(t *testing.T) {
	source := `
func isPositive(x int) bool {
    return x > 0;
}
var result bool = isPositive(5);
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["result"]; got != NewBoolValue(true) {
		t.Fatalf("result = %#v, want true", got)
	}
}

func TestFuncReturningString(t *testing.T) {
	source := `
func greet(name string) string {
    return name;
}
var result string = greet("world");
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["result"]; got != NewStringValue("world") {
		t.Fatalf("result = %#v, want world", got)
	}
}

func TestBoolAndOrOperators(t *testing.T) {
	source := `
var a bool = true && true;
var b bool = true && false;
var c bool = false || true;
var d bool = false || false;
var e bool = !true;
var f bool = !false;
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	tests := []struct {
		name string
		got  Value
		want Value
	}{
		{"true && true", result.Globals["a"], NewBoolValue(true)},
		{"true && false", result.Globals["b"], NewBoolValue(false)},
		{"false || true", result.Globals["c"], NewBoolValue(true)},
		{"false || false", result.Globals["d"], NewBoolValue(false)},
		{"!true", result.Globals["e"], NewBoolValue(false)},
		{"!false", result.Globals["f"], NewBoolValue(true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %#v, want %#v", test.got, test.want)
			}
		})
	}
}

func TestNestedIfElse(t *testing.T) {
	source := `
var x int = 10;
var result string = "none";
if x > 0 {
    if x > 5 {
        result = "big";
    } else {
        result = "small";
    }
} else {
    result = "non-positive";
}
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["result"]; got != NewStringValue("big") {
		t.Fatalf("result = %#v, want big", got)
	}
}

func TestShortCircuitOr(t *testing.T) {
	source := `
var flag bool = true || (1 / 0 == 0);
`
	result, err := Execute(source)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := result.Globals["flag"]; got != NewBoolValue(true) {
		t.Fatalf("flag = %#v, want true", got)
	}
}

func TestDuplicateVariableError(t *testing.T) {
	_, err := Execute("var x int = 1;\nvar x int = 2;")
	if err == nil || !strings.Contains(err.Error(), "duplicate variable") {
		t.Fatalf("expected duplicate variable error, got: %v", err)
	}
}

func TestDuplicateFunctionError(t *testing.T) {
	_, err := Execute("func f() int { return 1; } func f() int { return 2; }")
	if err == nil || !strings.Contains(err.Error(), "duplicate function") {
		t.Fatalf("expected duplicate function error, got: %v", err)
	}
}

func TestUndefinedFunctionError(t *testing.T) {
	_, err := Execute("var x int = foo();")
	if err == nil || !strings.Contains(err.Error(), "undefined function") {
		t.Fatalf("expected undefined function error, got: %v", err)
	}
}

func TestWrongArgumentCountError(t *testing.T) {
	_, err := Execute("func f(a int) int { return a; }\nvar x int = f(1, 2);")
	if err == nil || !strings.Contains(err.Error(), "expects 1 arguments, got 2") {
		t.Fatalf("expected wrong argument count error, got: %v", err)
	}
}
