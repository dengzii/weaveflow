package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/state"
)

type staticClarificationModel struct {
	content string
	request llms.ModelRequest
}

func (model *staticClarificationModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	model.request = request
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: model.content}}}, nil
}

func TestClarificationReadyNormalizesObjectiveAndRecordsAssumptions(t *testing.T) {
	model := &staticClarificationModel{content: `{"decision":"ready","normalized_objective":"Compare Go and Rust for backend services over the next three years.","question":"","assumptions":["Use global adoption data","Use global adoption data"],"reason":"The scope is sufficient."}`}
	target := NewClarificationNode(core.WithID("clarify"))
	access := state.NewEditingAccess(state.FromShared(map[string]any{
		"request": map[string]any{"input": "Compare Go and Rust for backend services."},
	}))
	ctx := core.NewContext(core.WithModel(context.Background(), model))

	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute clarification: %v", err)
	}
	objective, _ := access.ReadAny(target.ObjectivePath)
	if objective != "Compare Go and Rust for backend services over the next three years." {
		t.Fatalf("objective = %#v", objective)
	}
	original, _ := access.ReadAny(target.OriginalObjectivePath)
	if original != "Compare Go and Rust for backend services." {
		t.Fatalf("original objective = %#v", original)
	}
	assumptions, _ := access.ReadAny(target.AssumptionsPath)
	values, ok := assumptions.([]string)
	if !ok || len(values) != 1 || values[0] != "Use global adoption data" {
		t.Fatalf("assumptions = %#v", assumptions)
	}
	if model.request.ResponseName != "plan_clarification" || !model.request.StrictResponse {
		t.Fatalf("model request = %#v", model.request)
	}
}

func TestClarificationInterruptsWithModelQuestion(t *testing.T) {
	model := &staticClarificationModel{content: `{"decision":"needs_clarification","normalized_objective":"","question":"Which market and time range should the comparison cover?","assumptions":[],"reason":"The missing scope changes the evidence."}`}
	target := NewClarificationNode(core.WithID("clarify"))
	access := state.NewEditingAccess(state.FromShared(map[string]any{
		"request": map[string]any{"input": "Compare the leading products."},
	}))
	ctx := core.NewContext(core.WithModel(context.Background(), model))

	_, err := target.Execute(ctx, access)
	var interrupt *core.NodeInterrupt
	if !errors.As(err, &interrupt) {
		t.Fatalf("execute error = %v, want node interrupt", err)
	}
	if interrupt.NodeID != "clarify" || interrupt.Value != "Which market and time range should the comparison cover?" {
		t.Fatalf("interrupt = %#v", interrupt)
	}
	objective, _ := access.ReadAny(target.ObjectivePath)
	if objective != "Compare the leading products." {
		t.Fatalf("interrupted objective = %#v", objective)
	}
}

func TestClarificationConsumesResumeAnswerAndProceedsAfterOneRound(t *testing.T) {
	model := &staticClarificationModel{content: `{"decision":"needs_clarification","normalized_objective":"","question":"Another question","assumptions":["Use public information"],"reason":"Still ambiguous."}`}
	target := NewClarificationNode(core.WithID("clarify"))
	access := state.NewEditingAccess(state.FromShared(map[string]any{
		"request": map[string]any{
			"input":         "Compare the leading products.",
			"pending_input": "Global market during 2025.",
		},
	}))
	ctx := core.NewContext(core.WithModel(context.Background(), model))

	if _, err := target.Execute(ctx, access); err != nil {
		t.Fatalf("execute resumed clarification: %v", err)
	}
	objective, _ := access.ReadAny(target.ObjectivePath)
	text, _ := objective.(string)
	if !strings.Contains(text, "Compare the leading products.") || !strings.Contains(text, "Global market during 2025.") {
		t.Fatalf("fallback objective = %#v", objective)
	}
	answer, _ := access.ReadAny(target.AnswerPath)
	if answer != "Global market during 2025." {
		t.Fatalf("clarification answer = %#v", answer)
	}
	if _, exists := access.ReadAny(target.PendingInputPath); exists {
		t.Fatal("pending input was not consumed")
	}
}

func TestClarificationDefinitionBuildsConfiguredNode(t *testing.T) {
	built, err := ClarificationNodeTypeDefinition().Build(&registry.BuildContext{}, registry.ResolvedNodeSpec{
		Spec: dsl.GraphNodeSpec{ID: "clarify", Config: map[string]any{
			"model_id": "intake", "max_tokens": 900, "temperature": 0.1, "thinking": "medium",
		}},
		State: map[string]registry.ResolvedStateBinding{
			"objective":          {Path: state.Shared("request", "objective")},
			"pending_input":      {Path: state.Shared("request", "answer")},
			"original_objective": {Path: state.Shared("intake", "original")},
			"answer":             {Path: state.Shared("intake", "answer")},
			"assumptions":        {Path: state.Shared("intake", "assumptions")},
		},
	})
	if err != nil {
		t.Fatalf("build clarification: %v", err)
	}
	target := built.(*ClarificationNode)
	if target.ModelID != "intake" || target.MaxTokens != 900 || target.Temperature != 0.1 || target.Thinking != llms.ThinkingModeMedium {
		t.Fatalf("clarification controls = %#v", target)
	}
}
