package tools

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

func NewCalculator() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name:        "calculator",
			Description: "Evaluate a basic arithmetic expression such as 12*(3+4) or 18/6+7.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": map[string]any{
						"type":        "string",
						"description": "The arithmetic expression to evaluate.",
					},
				},
				"required":             []string{"expression"},
				"additionalProperties": false,
			},
		},
		Handler: calculatorTool,
	}
}

func calculatorTool(_ context.Context, input string) (string, error) {
	expression := strings.TrimSpace(input)
	if expression == "" {
		return "", errors.New("expression is required")
	}

	tree, err := parser.ParseExpr(expression)
	if err != nil {
		return "", err
	}

	value, err := evalArithmeticExpr(tree)
	if err != nil {
		return "", err
	}

	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10), nil
	}
	return strconv.FormatFloat(value, 'f', -1, 64), nil
}
func evalArithmeticExpr(expr ast.Expr) (float64, error) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.INT && typed.Kind != token.FLOAT {
			return 0, fmt.Errorf("unsupported literal %q", typed.Value)
		}
		return strconv.ParseFloat(typed.Value, 64)
	case *ast.BinaryExpr:
		left, err := evalArithmeticExpr(typed.X)
		if err != nil {
			return 0, err
		}
		right, err := evalArithmeticExpr(typed.Y)
		if err != nil {
			return 0, err
		}
		switch typed.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			return left / right, nil
		default:
			return 0, fmt.Errorf("unsupported operator %q", typed.Op)
		}
	case *ast.ParenExpr:
		return evalArithmeticExpr(typed.X)
	case *ast.UnaryExpr:
		value, err := evalArithmeticExpr(typed.X)
		if err != nil {
			return 0, err
		}
		switch typed.Op {
		case token.ADD:
			return value, nil
		case token.SUB:
			return -value, nil
		default:
			return 0, fmt.Errorf("unsupported unary operator %q", typed.Op)
		}
	default:
		return 0, fmt.Errorf("unsupported expression %T", expr)
	}
}
