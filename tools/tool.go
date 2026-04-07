package tools

import (
	"context"

	"github.com/tmc/langchaingo/llms"
)

type ToolHandler func(ctx context.Context, input string) (string, error)

type Tool struct {
	Function *llms.FunctionDefinition
	Handler  ToolHandler
}

func (t Tool) Name() string {

	if t.Function == nil {
		return ""
	}
	return t.Function.Name
}

func (t Tool) NewTool() llms.Tool {
	return llms.Tool{
		Type:     "function",
		Function: cloneFunctionDefinition(t.Function),
	}
}

func cloneFunctionDefinition(function *llms.FunctionDefinition) *llms.FunctionDefinition {
	if function == nil {
		return nil
	}
	cloned := *function
	return &cloned
}
