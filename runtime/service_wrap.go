package runtime

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/dengzii/weaveflow/core"

	"github.com/tmc/langchaingo/llms"
)

func withRunnerEventContext(ctx context.Context, runner *GraphRunner, runID, stepID, nodeID string) core.Context {
	coreCtx := core.NewContext(ctx)
	if coreCtx.Model() == nil && len(coreCtx.Models()) == 0 && coreCtx.Tools() == nil {
		return coreCtx
	}
	if models := wrapLlms(coreCtx.Models()); len(models) > 0 {
		ctx = core.WithModels(ctx, models)
	} else {
		ctx = core.WithModel(ctx, wrapLlm(coreCtx.Model()))
	}
	ctx = core.WithTools(ctx, wrapToolCallEventTools(coreCtx.Tools(), runner, runID, stepID, nodeID))
	ctx = core.WithMemory(ctx, coreCtx.Memory())
	return core.NewContext(ctx)
}

func wrapToolCallEventTools(available map[string]core.Tool, runner *GraphRunner, runID, stepID, nodeID string) map[string]core.Tool {
	if available == nil {
		return nil
	}
	wrapped := make(map[string]core.Tool, len(available))
	for key, tool := range available {
		wrapped[key] = wrapToolCallEventTool(key, tool, runner, runID, stepID, nodeID)
	}
	return wrapped
}

func wrapToolCallEventTool(key string, tool core.Tool, runner *GraphRunner, runID, stepID, nodeID string) core.Tool {
	if tool.Handler == nil {
		return tool
	}
	original := tool.Handler
	toolName := strings.TrimSpace(tool.Name())
	if toolName == "" {
		toolName = strings.TrimSpace(key)
	}
	tool.Handler = func(ctx context.Context, input string) (string, error) {
		metadata, _ := core.ToolCallMetadataFromContext(ctx)
		name := strings.TrimSpace(metadata.Name)
		if name == "" {
			name = toolName
		}
		arguments := metadata.Arguments
		if arguments == "" {
			arguments = input
		}

		_ = runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolCalled, map[string]any{
			"tool_call_id": metadata.ToolCallID,
			"name":         name,
			"arguments":    arguments,
		})

		_, _ = SaveJSONArtifactBestEffort(ctx, "tool.input", map[string]any{
			"tool_call_id": metadata.ToolCallID,
			"name":         name,
			"arguments":    arguments,
			"input":        input,
		})

		result, err := original(ctx, input)
		if err != nil {
			_ = runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolFailed, map[string]any{
				"tool_call_id": metadata.ToolCallID,
				"name":         name,
				"error":        err.Error(),
			})
			_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", map[string]any{
				"tool_call_id": metadata.ToolCallID,
				"name":         name,
				"error":        err.Error(),
			})
			return result, err
		}

		_ = runner.publishEvent(ctx, RunRecord{RunID: runID}, stepID, nodeID, EventToolReturned, map[string]any{
			"tool_call_id": metadata.ToolCallID,
			"name":         name,
			"content":      result,
		})
		_, _ = SaveJSONArtifactBestEffort(ctx, "tool.output", map[string]any{
			"tool_call_id": metadata.ToolCallID,
			"name":         name,
			"content":      result,
		})
		return result, nil
	}
	return tool
}

func wrapLlms(models map[string]llms.Model) map[string]llms.Model {
	if len(models) == 0 {
		return nil
	}
	wrapped := make(map[string]llms.Model, len(models))
	for id, model := range models {
		if strings.TrimSpace(id) == "" || model == nil {
			continue
		}
		wrapped[id] = wrapLlm(model)
	}
	if len(wrapped) == 0 {
		return nil
	}
	return wrapped
}

type llmWrap struct {
	m llms.Model
}

func wrapLlm(m llms.Model) llms.Model {
	if m == nil {
		return nil
	}
	if _, ok := m.(*llmWrap); ok {
		return m
	}
	return &llmWrap{m: m}
}

func (m *llmWrap) SupportsReasoning() bool {
	r, ok := m.m.(llms.ReasoningModel)
	return ok && r.SupportsReasoning()
}

func (m *llmWrap) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {

	options = append(options, withLLMStreamingResponseEvent())
	res, err := m.m.GenerateContent(ctx, messages, options...)
	if err == nil && res != nil && len(res.Choices) > 0 && res.Choices[0] != nil {
		choice1 := res.Choices[0]
		if strings.TrimSpace(choice1.ReasoningContent) != "" {
			_ = PublishRunnerContextEvent(ctx, EventLLMReasoning, map[string]any{"text": choice1.ReasoningContent})
		}
		if strings.TrimSpace(choice1.Content) != "" {
			_ = PublishRunnerContextEvent(ctx, EventLLMContent, map[string]any{"text": choice1.Content})
		}
		if choice1.FuncCall != nil {
			_ = PublishRunnerContextEvent(ctx, EventLLMFunctionCall, choice1.FuncCall)
		}
		_ = PublishRunnerContextEvent(ctx, EventLLMCall, buildLLMCallStatsPayload(m.m, choice1))
	}
	return res, err
}

func buildLLMCallStatsPayload(model llms.Model, choice *llms.ContentChoice) map[string]any {
	payload := map[string]any{
		"model":       llmWrapModelLabel(model),
		"stop_reason": strings.TrimSpace(choice.StopReason),
		"calls":       1,
	}
	prompt, _ := llmWrapIntFromKeys(choice.GenerationInfo,
		"PromptTokens", "prompt_tokens", "input_tokens")
	completion, _ := llmWrapIntFromKeys(choice.GenerationInfo,
		"CompletionTokens", "completion_tokens", "output_tokens")
	total, _ := llmWrapIntFromKeys(choice.GenerationInfo,
		"TotalTokens", "total_tokens")
	reasoning, _ := llmWrapIntFromKeys(choice.GenerationInfo,
		"ReasoningTokens", "ThinkingTokens", "CompletionReasoningTokens", "reasoning_tokens")
	cached, _ := llmWrapIntFromKeys(choice.GenerationInfo,
		"PromptCachedTokens", "prompt_cached_tokens", "cached_input_tokens")

	if total <= 0 && (prompt > 0 || completion > 0) {
		total = prompt + completion
	}
	if completion <= 0 && total > 0 && total >= prompt {
		completion = total - prompt
	}
	if sum := prompt + completion; sum > total {
		total = sum
	}

	payload["prompt_tokens"] = prompt
	payload["completion_tokens"] = completion
	payload["total_tokens"] = total
	payload["reasoning_tokens"] = reasoning
	payload["prompt_cached_tokens"] = cached
	return payload
}

func llmWrapModelLabel(model llms.Model) string {
	if model == nil {
		return ""
	}
	if named, ok := model.(interface{ Name() string }); ok {
		if name := strings.TrimSpace(named.Name()); name != "" {
			return name
		}
	}
	typed := reflect.TypeOf(model)
	if typed == nil {
		return ""
	}
	return typed.String()
}

func llmWrapIntFromKeys(values map[string]any, keys ...string) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case int:
			return v, true
		case int32:
			return int(v), true
		case int64:
			return int(v), true
		case float32:
			return int(v), true
		case float64:
			return int(v), true
		}
	}
	return 0, false
}

func (m *llmWrap) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return m.m.Call(ctx, prompt, options...)
}

type llmResponseEventHandler struct {
	bufferReasoning  []byte
	toolCallDetected bool
}

func withLLMStreamingResponseEvent() llms.CallOption {

	handler := llmResponseEventHandler{
		bufferReasoning: make([]byte, 0),
	}

	return func(o *llms.CallOptions) {
		o.StreamingReasoningFunc = handler.emitStreamingResponse
	}
}

func (l *llmResponseEventHandler) emitStreamingResponse(ctx context.Context, reasoningChunk, chunk []byte) error {
	if l.toolCallDetected {
		return nil
	}
	reasoning := string(reasoningChunk)
	if strings.TrimSpace(reasoning) != "" {
		l.bufferReasoning = append(l.bufferReasoning, reasoningChunk...)
		if err := PublishRunnerContextEvent(ctx, EventLLMReasoningChunk, map[string]any{"text": reasoning}); err != nil {
			return err
		}
		if !HasRunnerEventPublisher(ctx) {
			fmt.Print(reasoning)
		}
	}
	content := string(chunk)
	if strings.TrimSpace(content) != "" {
		// Detect tool-call payload (JSON array); skip content emission for those.
		if !l.toolCallDetected {
			l.toolCallDetected = strings.HasPrefix(content, "[{")
		}
		if l.toolCallDetected {
			return nil
		}
		if err := PublishRunnerContextEvent(ctx, EventLLMContentChunk, map[string]any{"text": content}); err != nil {
			return err
		}
		if !HasRunnerEventPublisher(ctx) {
			fmt.Print(content)
		}
	}
	return nil
}
