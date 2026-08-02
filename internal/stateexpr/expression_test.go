package stateexpr

import (
	"context"
	"strings"
	"testing"
)

func TestProgramEvaluatesInputs(t *testing.T) {
	t.Parallel()
	program, err := Compile("{'sum': inputs.left + inputs.right}", CompileOptions{})
	if err != nil {
		t.Fatalf("Compile(): %v", err)
	}
	value, err := program.EvalJSON(context.Background(), map[string]any{"left": 2, "right": 3})
	if err != nil {
		t.Fatalf("EvalJSON(): %v", err)
	}
	object := value.(map[string]any)
	if object["sum"] != float64(5) {
		t.Fatalf("value = %#v", value)
	}
}

func TestCompileBooleanRejectsStaticNonBoolean(t *testing.T) {
	t.Parallel()
	_, err := Compile("42", CompileOptions{RequireBoolean: true})
	if err == nil || !strings.Contains(err.Error(), "not boolean") {
		t.Fatalf("Compile() error = %v", err)
	}
}
