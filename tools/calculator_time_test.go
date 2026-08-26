package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCalculatorToolEvaluatesArithmeticAndPreservesCallMetadata(t *testing.T) {
	tests := []struct {
		expression string
		want       string
	}{
		{expression: "12*(3+4)", want: "84"},
		{expression: "7/2", want: "3.5"},
		{expression: "-(-2)+3", want: "5"},
		{expression: "+4.25-1.25", want: "3"},
	}
	for _, testCase := range tests {
		t.Run(testCase.expression, func(t *testing.T) {
			result, err := calculatorTool(context.Background(), toolCallForTest("calculator", `{"expression":"`+testCase.expression+`"}`))
			if err != nil {
				t.Fatalf("calculatorTool() error = %v", err)
			}
			if result.Content != testCase.want || result.Value != testCase.want || result.ToolCallID != "test-call" || result.Name != "calculator" {
				t.Fatalf("calculator result = %#v", result)
			}
		})
	}
}

func TestCalculatorToolRejectsInvalidAndUnsupportedExpressions(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		contains  string
	}{
		{name: "invalid json", arguments: `{"expression":`, contains: "calculator input"},
		{name: "empty", arguments: `{"expression":"  "}`, contains: "expression is required"},
		{name: "parse", arguments: `{"expression":"1+"}`, contains: "expected operand"},
		{name: "division by zero", arguments: `{"expression":"1/0"}`, contains: "division by zero"},
		{name: "operator", arguments: `{"expression":"5%2"}`, contains: "unsupported operator"},
		{name: "unary", arguments: `{"expression":"^1"}`, contains: "unsupported unary operator"},
		{name: "literal", arguments: `{"expression":"'x'"}`, contains: "unsupported literal"},
		{name: "call", arguments: `{"expression":"max(1,2)"}`, contains: "unsupported expression"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := calculatorTool(context.Background(), toolCallForTest("calculator", testCase.arguments))
			if err == nil || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("calculatorTool() error = %v, want %q", err, testCase.contains)
			}
		})
	}
}

func TestCurrentTimeToolReturnsMatchingLocalAndUTCInstants(t *testing.T) {
	result, err := currentTimeTool(context.Background(), toolCallForTest("current_time", `{}`))
	if err != nil {
		t.Fatalf("currentTimeTool() error = %v", err)
	}
	parts := strings.Split(result.Content, "; ")
	if len(parts) != 2 {
		t.Fatalf("current time content = %q", result.Content)
	}
	local, err := time.Parse(time.RFC3339, strings.TrimPrefix(parts[0], "local="))
	if err != nil {
		t.Fatalf("parse local time: %v", err)
	}
	utc, err := time.Parse(time.RFC3339, strings.TrimPrefix(parts[1], "utc="))
	if err != nil {
		t.Fatalf("parse UTC time: %v", err)
	}
	if !local.Equal(utc) || utc.Location() != time.UTC {
		t.Fatalf("local = %v, UTC = %v", local, utc)
	}
	if _, err := currentTimeTool(context.Background(), toolCallForTest("current_time", `{"unknown":true}`)); err == nil || !strings.Contains(err.Error(), "current_time input") {
		t.Fatalf("currentTimeTool() invalid input error = %v", err)
	}
}

func TestToolConvenienceAPIsReturnFreshFactoriesAndExecute(t *testing.T) {
	first := BuiltinFactories()
	second := BuiltinFactories()
	delete(first, "read")
	if second["read"] == nil || len(second) != 6 {
		t.Fatalf("BuiltinFactories() returned shared or incomplete map: %#v", second)
	}
	calculator := NewCalculator()
	available := map[string]Tool{"calculator": calculator}
	found, ok := FindAvailable(available, "calculator")
	if !ok || found.Name() != "calculator" {
		t.Fatalf("FindAvailable() = %#v, %v", found, ok)
	}
	result, err := Execute(context.Background(), found, toolCallForTest("calculator", `{"expression":"2+3"}`))
	if err != nil || result.Content != "5" {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
}
