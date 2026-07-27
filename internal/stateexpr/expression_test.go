package stateexpr

import (
	"context"
	"strings"
	"testing"
)

func TestProgramEvaluatesInputsAndLegacyInput(t *testing.T) {
	t.Parallel()
	program, err := Compile("{'sum': inputs.left + inputs.right, 'legacy': input}", CompileOptions{LegacyInput: true})
	if err != nil {
		t.Fatalf("Compile(): %v", err)
	}
	value, err := program.EvalJSON(context.Background(), map[string]any{"left": 2, "right": 3}, "ok", true)
	if err != nil {
		t.Fatalf("EvalJSON(): %v", err)
	}
	object := value.(map[string]any)
	if object["sum"] != float64(5) || object["legacy"] != "ok" {
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
