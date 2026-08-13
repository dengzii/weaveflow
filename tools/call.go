package tools

import (
	"context"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"
)

type Call = llms.ToolCall
type Result = llms.ToolResult

func Execute(ctx context.Context, tool Tool, call Call) (Result, error) {
	return core.ExecuteTool(ctx, tool, call)
}

func FindAvailable(available map[string]Tool, name string) (Tool, bool) {
	return core.FindTool(available, name)
}
