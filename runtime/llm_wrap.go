package runtime

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type llmWrap struct {
	m llms.Model
}

func WrapLLM(m llms.Model) llms.Model {
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

	//str := StringifyMessages(messages)
	//fmt.Println(str)

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

func StringifyMessages(messages []llms.MessageContent) string {
	writer := &strings.Builder{}
	llms.ShowMessageContents(writer, messages)
	return writer.String()
}
