package runtime

import (
	"context"
	"fmt"
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
	if err == nil {
		choice1 := res.Choices[0]
		_ = PublishRunnerContextEvent(ctx, EventLLMContent, choice1.Content)
		if choice1.FuncCall != nil {
			_ = PublishRunnerContextEvent(ctx, EventLLMFunctionCall, choice1.FuncCall)
		}
	}
	return res, err
}

func (m *llmWrap) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return m.m.Call(ctx, prompt, options...)
}

type llmResponseEventHandler struct {
	bufferReasoning  []byte
	bufferContent    []byte
	reasoningEmitted bool
	toolCallDetected bool
}

func withLLMStreamingResponseEvent() llms.CallOption {

	handler := llmResponseEventHandler{
		bufferReasoning: make([]byte, 0),
		bufferContent:   make([]byte, 0),
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
		if !l.reasoningEmitted {
			l.reasoningEmitted = true
			_ = PublishRunnerContextEvent(ctx, EventLLMReasoning, map[string]any{"text": string(l.bufferReasoning)})
		}
		// Detect tool-call payload (JSON array); skip content emission for those.
		if !l.toolCallDetected {
			l.toolCallDetected = strings.HasPrefix(content, "[{")
		}
		if l.toolCallDetected {
			return nil
		}
		l.bufferContent = append(l.bufferContent, chunk...)
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
