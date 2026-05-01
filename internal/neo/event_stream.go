package neo

import (
	"context"
	"encoding/json"
	"strings"
	fruntime "weaveflow/runtime"
)

type ChatEventType string

const (
	ChatEventTypeStep       ChatEventType = "step_event"
	ChatEventTypeThinking   ChatEventType = "thinking_chunk"
	ChatEventTypeGenerating ChatEventType = "generating_chunk"
	ChatEventTypeToolCall   ChatEventType = "tool_call"
	ChatEventTypeToolResult ChatEventType = "tool_result"
	ChatEventTypeComplete   ChatEventType = "complete"
	ChatEventTypeError      ChatEventType = "error"
)

type ChatEvent struct {
	Type    ChatEventType   `json:"type"`
	Content string          `json:"content,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type ChannelEventSink struct {
	ch chan fruntime.Event
}

func NewChannelEventSink() *ChannelEventSink {
	return &ChannelEventSink{ch: make(chan fruntime.Event, 256)}
}

func (s *ChannelEventSink) Publish(_ context.Context, event fruntime.Event) error {
	select {
	case s.ch <- event:
	default:
	}
	return nil
}

func (s *ChannelEventSink) PublishBatch(_ context.Context, events []fruntime.Event) error {
	for _, event := range events {
		select {
		case s.ch <- event:
		default:
		}
	}
	return nil
}

func (s *ChannelEventSink) Events() <-chan fruntime.Event {
	return s.ch
}

func (s *ChannelEventSink) Close() {
	close(s.ch)
}

// nodeActionMap maps node ID prefixes to human-readable action names and descriptions.
var nodeActionMap = []struct {
	prefix  string
	action  string
	content string
}{
	{"SessionBootstrap_", "initializing", "正在初始化会话..."},
	{"MemoryRecall_", "recalling", "正在回忆相关信息..."},
	{"OrchestrationRouter_", "routing", "正在分析请求..."},
	{"Planner_", "planning", "正在制定计划..."},
	{"PlanStepExecutor_", "executing", "正在执行计划步骤..."},
	{"ContextAssembler_", "assembling", "正在整理上下文..."},
	{"LLM_", "generating", "正在生成回复..."},
	{"ToolCall_", "calling_tool", "正在调用工具..."},
	{"ObservationRecorder_", "recording", "正在记录观察结果..."},
	{"Verifier_", "verifying", "正在验证结果..."},
	{"Finalizer_", "finalizing", "正在整理最终回复..."},
	{"MemoryWrite_", "saving", "正在保存记忆..."},
}

var streamableContentPrefixes = []string{
	"LLM_",
	"Finalizer_",
}

var streamableReasoningPrefixes = []string{
	"LLM_",
	"Finalizer_",
	"Planner_",
	"Verifier_",
	"OrchestrationRouter_",
}

func hasPrefix(nodeID string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(nodeID, p) {
			return true
		}
	}
	return false
}

func marshalData(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func TranslateEvent(event fruntime.Event) *ChatEvent {
	switch event.Type {
	case fruntime.EventNodeStarted:
		return translateNodeStarted(event)
	case fruntime.EventLLMContentChunk:
		if !hasPrefix(event.NodeID, streamableContentPrefixes) {
			return nil
		}
		text := extractPayloadString(event.Payload, "text")
		if text == "" {
			return nil
		}
		return &ChatEvent{Type: ChatEventTypeGenerating, Content: text}
	case fruntime.EventLLMContent:
		// Skipped: content already streamed via EventLLMContentChunk; emitting again would duplicate text.
		return nil
	case fruntime.EventLLMReasoningChunk:
		// Use EventLLMReasoning (complete text) instead of streaming chunks.
		return nil
	case fruntime.EventLLMReasoning:
		if !hasPrefix(event.NodeID, streamableReasoningPrefixes) {
			return nil
		}
		text := extractPayloadString(event.Payload, "text")
		if text == "" {
			return nil
		}
		return &ChatEvent{Type: ChatEventTypeThinking, Content: text}
	case fruntime.EventToolCalled:
		return translateToolCalled(event)
	case fruntime.EventToolReturned:
		return translateToolReturned(event)
	case fruntime.EventToolFailed:
		return translateToolFailed(event)
	case fruntime.EventRunFinished:
		return &ChatEvent{Type: ChatEventTypeComplete, Content: "完成"}
	case fruntime.EventRunFailed:
		errMsg := extractPayloadString(event.Payload, "error_message")
		msg := "执行失败"
		if errMsg != "" {
			msg = "执行失败: " + errMsg
		}
		return &ChatEvent{Type: ChatEventTypeError, Content: msg}
	case fruntime.EventRunCanceled:
		return &ChatEvent{Type: ChatEventTypeComplete, Content: "已取消"}
	default:
		return nil
	}
}

func translateNodeStarted(event fruntime.Event) *ChatEvent {
	for _, m := range nodeActionMap {
		if strings.HasPrefix(event.NodeID, m.prefix) {
			return &ChatEvent{
				Type:    ChatEventTypeStep,
				Content: m.content,
				Data:    marshalData(map[string]string{"action": m.action, "node_id": event.NodeID}),
			}
		}
	}
	return nil
}

func translateToolCalled(event fruntime.Event) *ChatEvent {
	name := extractPayloadString(event.Payload, "name")
	toolCallID := extractPayloadString(event.Payload, "tool_call_id")
	content := "正在调用工具..."
	if name != "" {
		content = "正在调用工具: " + name
	}
	return &ChatEvent{
		Type:    ChatEventTypeToolCall,
		Content: content,
		Data:    marshalData(map[string]string{"name": name, "tool_call_id": toolCallID}),
	}
}

func translateToolReturned(event fruntime.Event) *ChatEvent {
	name := extractPayloadString(event.Payload, "name")
	result := extractPayloadString(event.Payload, "content")
	toolCallID := extractPayloadString(event.Payload, "tool_call_id")
	return &ChatEvent{
		Type:    ChatEventTypeToolResult,
		Content: "工具调用完成",
		Data:    marshalData(map[string]string{"name": name, "result": result, "tool_call_id": toolCallID}),
	}
}

func translateToolFailed(event fruntime.Event) *ChatEvent {
	name := extractPayloadString(event.Payload, "name")
	errMsg := extractPayloadString(event.Payload, "error")
	msg := "工具调用失败"
	if errMsg != "" {
		msg = "工具调用失败: " + errMsg
	}
	return &ChatEvent{
		Type:    ChatEventTypeToolResult,
		Content: msg,
		Data:    marshalData(map[string]string{"name": name, "error": errMsg}),
	}
}

func extractPayloadString(payload json.RawMessage, key string) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}
