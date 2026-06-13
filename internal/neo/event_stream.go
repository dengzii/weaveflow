package neo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

type ChatEventType string

const (
	ChatEventTypeStep          ChatEventType = "step_event"
	ChatEventTypeThinking      ChatEventType = "thinking_chunk"
	ChatEventTypeGenerating    ChatEventType = "generating_chunk"
	ChatEventTypeToolCall      ChatEventType = "tool_call"
	ChatEventTypeToolResult    ChatEventType = "tool_result"
	ChatEventTypePlan          ChatEventType = "planner_progress"
	ChatEventTypeClarification ChatEventType = "clarification_question"
	ChatEventTypeComplete      ChatEventType = "complete"
	ChatEventTypeError         ChatEventType = "error"
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

var nodeActionMap = []struct {
	prefix  string
	action  string
	content string
}{
	{"SessionBootstrap_", "initializing", "正在初始化会话..."},
	{"MemoryRecall_", "recalling", "正在回忆相关信息..."},
	{"OrchestrationRouter_", "routing", "正在分析请求..."},
	{"Clarification_", "clarifying", "正在准备澄清问题..."},
	{"Planner_", "planning", "正在制定计划..."},
	{"PlanStepExecutor_", "executing", "正在执行计划步骤..."},
	{"ContextAssembler_", "assembling", "正在整理上下文..."},
	{"LLM_", "generating", "正在生成回复..."},
	{"ToolCall_", "calling_tool", "正在调用工具..."},
	{"ObservationRecorder_", "recording", "正在记录观察结果..."},
	{"Verifier_", "verifying", "正在验证结果..."},
	{"Finalizer_", "finalizing", "正在整理最终回答..."},
	{"MemoryWrite_", "saving", "正在保存记忆..."},
	{"Explore_", "exploring", "正在浏览文件..."},
}

var streamableContentPrefixes = []string{
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
	for _, prefix := range prefixes {
		if strings.HasPrefix(nodeID, prefix) {
			return true
		}
	}
	return false
}

func marshalData(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
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
		if !hasPrefix(event.NodeID, streamableContentPrefixes) {
			return nil
		}
		text := extractPayloadString(event.Payload, "text")
		if text == "" {
			return nil
		}
		return &ChatEvent{Type: ChatEventTypeGenerating, Content: text}
	case fruntime.EventLLMReasoningChunk:
		if !hasPrefix(event.NodeID, streamableReasoningPrefixes) {
			return nil
		}
		text := extractPayloadString(event.Payload, "text")
		if text == "" {
			return nil
		}
		return &ChatEvent{Type: ChatEventTypeThinking, Content: text}
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
	case fruntime.EventNodeCustom:
		return translateNodeCustom(event)
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
		return &ChatEvent{Type: ChatEventTypeComplete, Content: "已停止"}
	default:
		return nil
	}
}

func translateNodeStarted(event fruntime.Event) *ChatEvent {
	for _, item := range nodeActionMap {
		if strings.HasPrefix(event.NodeID, item.prefix) {
			return &ChatEvent{
				Type:    ChatEventTypeStep,
				Content: item.content,
				Data:    marshalData(map[string]string{"action": item.action, "node_id": event.NodeID}),
			}
		}
	}
	return nil
}

func translateNodeCustom(event fruntime.Event) *ChatEvent {
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil
	}
	kind, _ := payload["kind"].(string)
	switch kind {
	case "planner_progress":
		content := stringFromAny(payload["message"])
		if content == "" {
			content = stringFromAny(payload["summary"])
		}
		if content == "" {
			content = stringFromAny(payload["status"])
		}
		return &ChatEvent{
			Type:    ChatEventTypePlan,
			Content: content,
			Data:    event.Payload,
		}
	case "clarification_question":
		content := stringFromAny(payload["question"])
		return &ChatEvent{
			Type:    ChatEventTypeClarification,
			Content: content,
			Data:    event.Payload,
		}
	default:
		return nil
	}
}

func translateToolCalled(event fruntime.Event) *ChatEvent {
	items := toolPayloadItems(event.Payload)
	if len(items) > 1 {
		return &ChatEvent{
			Type:    ChatEventTypeToolCall,
			Content: fmt.Sprintf("正在调用 %d 个工具...", len(items)),
			Data:    event.Payload,
		}
	}

	name := extractPayloadString(event.Payload, "name")
	toolCallID := extractPayloadString(event.Payload, "tool_call_id")
	arguments := extractPayloadString(event.Payload, "arguments")
	content := "正在调用工具..."
	if name != "" {
		content = "正在调用工具: " + name
	}
	return &ChatEvent{
		Type:    ChatEventTypeToolCall,
		Content: content,
		Data:    marshalData(map[string]string{"name": name, "tool_call_id": toolCallID, "arguments": arguments}),
	}
}

func translateToolReturned(event fruntime.Event) *ChatEvent {
	items := toolPayloadItems(event.Payload)
	if len(items) > 1 {
		return &ChatEvent{
			Type:    ChatEventTypeToolResult,
			Content: fmt.Sprintf("%d 调用完成", len(items)),
			Data:    event.Payload,
		}
	}

	name := extractPayloadString(event.Payload, "name")
	result := extractPayloadString(event.Payload, "content")
	toolCallID := extractPayloadString(event.Payload, "tool_call_id")
	arguments := extractPayloadString(event.Payload, "arguments")
	data := map[string]string{"name": name, "result": result, "tool_call_id": toolCallID}
	if arguments != "" {
		data["arguments"] = arguments
	}
	return &ChatEvent{
		Type:    ChatEventTypeToolResult,
		Content: "工具调用完成",
		Data:    marshalData(data),
	}
}

func translateToolFailed(event fruntime.Event) *ChatEvent {
	items := toolPayloadItems(event.Payload)
	if len(items) > 1 {
		failed := 0
		for _, item := range items {
			if item.failed() {
				failed++
			}
		}
		succeeded := len(items) - failed
		return &ChatEvent{
			Type:    ChatEventTypeToolResult,
			Content: fmt.Sprintf("工具调用完成: %d 成功, %d 失败", succeeded, failed),
			Data:    event.Payload,
		}
	}

	name := extractPayloadString(event.Payload, "name")
	errMsg := extractPayloadString(event.Payload, "error")
	toolCallID := extractPayloadString(event.Payload, "tool_call_id")
	arguments := extractPayloadString(event.Payload, "arguments")
	data := map[string]string{"name": name, "error": errMsg, "result": errMsg, "tool_call_id": toolCallID}
	if arguments != "" {
		data["arguments"] = arguments
	}
	msg := "工具调用失败"
	if errMsg != "" {
		msg = "工具调用失败: " + errMsg
	}
	return &ChatEvent{
		Type:    ChatEventTypeToolResult,
		Content: msg,
		Data:    marshalData(data),
	}
}

type toolPayloadItem struct {
	ToolCallID string
	Name       string
	Arguments  string
	Result     string
	Error      string
	Status     string
}

func (i toolPayloadItem) failed() bool {
	if strings.TrimSpace(i.Error) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(i.Status)) {
	case "failed", "error":
		return true
	default:
		return false
	}
}

func toolPayloadItems(payload json.RawMessage) []toolPayloadItem {
	if len(payload) == 0 {
		return nil
	}

	var mapped map[string]any
	if err := json.Unmarshal(payload, &mapped); err != nil {
		return nil
	}

	if rawTools, ok := mapped["tools"].([]any); ok {
		items := make([]toolPayloadItem, 0, len(rawTools))
		for _, rawTool := range rawTools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, toolPayloadItemFromMap(tool))
		}
		return items
	}

	return []toolPayloadItem{toolPayloadItemFromMap(mapped)}
}

func toolPayloadItemFromMap(mapped map[string]any) toolPayloadItem {
	result := stringFromAny(mapped["content"])
	if result == "" {
		result = stringFromAny(mapped["result"])
	}
	return toolPayloadItem{
		ToolCallID: stringFromAny(mapped["tool_call_id"]),
		Name:       stringFromAny(mapped["name"]),
		Arguments:  stringFromAny(mapped["arguments"]),
		Result:     result,
		Error:      stringFromAny(mapped["error"]),
		Status:     stringFromAny(mapped["status"]),
	}
}

func extractPayloadString(payload json.RawMessage, key string) string {
	return extractEventPayloadString(payload, key)
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
