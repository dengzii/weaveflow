package tools

import (
	"context"

	"github.com/dengzii/weaveflow/core"
)

type CallMetadata = core.ToolCallMetadata

func WithCallMetadata(ctx context.Context, metadata CallMetadata) context.Context {
	return core.WithToolCallMetadata(ctx, metadata)
}

func CallMetadataFromContext(ctx context.Context) (CallMetadata, bool) {
	return core.ToolCallMetadataFromContext(ctx)
}

func DecodeInput(arguments string) string {
	return core.DecodeToolInput(arguments)
}

func FindAvailable(available map[string]Tool, name string) (Tool, bool) {
	return core.FindTool(available, name)
}
