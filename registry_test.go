package weaveflow

import (
	"context"
	"strings"
	"testing"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type stubBuildModel struct{}

func (stubBuildModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return nil, nil
}

func (stubBuildModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
}

func TestBuildGraphRequiresEntryPoint(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		FinishPoint: "tools",
		Nodes: []GraphNodeSpec{
			{ID: "tools", Type: "tools"},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{})
	if err == nil || !strings.Contains(err.Error(), "entry point") {
		t.Fatalf("expected missing entry point error, got %v", err)
	}
}

func TestBuildGraphRejectsUnknownToolIDs(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	def := GraphDefinition{
		EntryPoint:  "llm",
		FinishPoint: "llm",
		Nodes: []GraphNodeSpec{
			{
				ID:   "llm",
				Type: "llm",
				Config: map[string]any{
					"tool_ids": []any{"missing_tool"},
				},
			},
		},
	}

	_, err := registry.BuildGraph(def, &BuildContext{
		Model: stubBuildModel{},
		Tools: map[string]tools.Tool{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing_tool") {
		t.Fatalf("expected unknown tool_id error, got %v", err)
	}
}
