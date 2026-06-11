package tools

import (
	"context"
	"encoding/json"
	"strings"
)

type CallMetadata struct {
	ToolCallID string
	Name       string
	Arguments  string
}

type callMetadataKey struct{}

func WithCallMetadata(ctx context.Context, metadata CallMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, callMetadataKey{}, metadata)
}

func CallMetadataFromContext(ctx context.Context) (CallMetadata, bool) {
	if ctx == nil {
		return CallMetadata{}, false
	}
	metadata, ok := ctx.Value(callMetadataKey{}).(CallMetadata)
	return metadata, ok
}

func DecodeInput(arguments string) string {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	if len(payload) == 1 {
		if input, ok := payload["input"].(string); ok {
			return input
		}
		if expression, ok := payload["expression"].(string); ok {
			return expression
		}
	}
	return raw
}

func FindAvailable(available map[string]Tool, name string) (Tool, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Tool{}, false
	}
	if tool, ok := available[name]; ok {
		return tool, true
	}
	for key, tool := range available {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return tool, true
		}
		if strings.EqualFold(strings.TrimSpace(tool.Name()), name) {
			return tool, true
		}
	}
	return Tool{}, false
}
