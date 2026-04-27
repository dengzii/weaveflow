package neo

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	fruntime "weaveflow/runtime"
)

type ActionType string

const (
	ActionThinking    ActionType = "thinking"
	ActionPlanning    ActionType = "planning"
	ActionCallingTool ActionType = "calling_tool"
	ActionToolResult  ActionType = "tool_result"
	ActionGenerating  ActionType = "generating"
	ActionVerifying   ActionType = "verifying"
	ActionFinalizing  ActionType = "finalizing"
	ActionComplete    ActionType = "complete"
	ActionError       ActionType = "error"
)

type ChatEvent struct {
	Type      ActionType `json:"type"`
	Message   string     `json:"message,omitempty"`
	Content   string     `json:"content,omitempty"`
	NodeID    string     `json:"node_id,omitempty"`
	Timestamp time.Time  `json:"timestamp"`
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
	action  ActionType
	message string
}{
	{"SessionBootstrap_", ActionThinking, "正在初始化会话..."},
	{"MemoryRecall_", ActionThinking, "正在回忆相关信息..."},
	{"OrchestrationRouter_", ActionThinking, "正在分析请求..."},
	{"Planner_", ActionPlanning, "正在制定计划..."},
	{"PlanStepExecutor_", ActionPlanning, "正在执行计划步骤..."},
	{"ContextAssembler_", ActionThinking, "正在整理上下文..."},
	{"LLM_", ActionGenerating, "正在生成回复..."},
	{"ToolCall_", ActionCallingTool, "正在调用工具..."},
	{"ObservationRecorder_", ActionGenerating, "正在记录观察结果..."},
	{"Verifier_", ActionVerifying, "正在验证结果..."},
	{"Finalizer_", ActionFinalizing, "正在整理最终回复..."},
	{"MemoryWrite_", ActionFinalizing, "正在保存记忆..."},
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

func TranslateEvent(event fruntime.Event) *ChatEvent {
	switch event.Type {
	case fruntime.EventNodeStarted:
		return translateNodeStarted(event)
	case fruntime.EventLLMContentChunk:
		if !hasPrefix(event.NodeID, streamableContentPrefixes) {
			return nil
		}
		return translateChunk(event, ActionGenerating)
	case fruntime.EventLLMReasoningChunk:
		if !hasPrefix(event.NodeID, streamableReasoningPrefixes) {
			return nil
		}
		return translateChunk(event, ActionThinking)
	case fruntime.EventToolCalled:
		return translateToolCalled(event)
	case fruntime.EventToolReturned:
		return &ChatEvent{
			Type:      ActionToolResult,
			Message:   "工具调用完成",
			NodeID:    event.NodeID,
			Timestamp: event.Timestamp,
		}
	case fruntime.EventToolFailed:
		return translateToolFailed(event)
	case fruntime.EventRunFinished:
		return &ChatEvent{
			Type:      ActionComplete,
			Message:   "完成",
			Timestamp: event.Timestamp,
		}
	case fruntime.EventRunFailed:
		return translateRunFailed(event)
	case fruntime.EventRunCanceled:
		return &ChatEvent{
			Type:      ActionComplete,
			Message:   "已取消",
			Timestamp: event.Timestamp,
		}
	default:
		return nil
	}
}

func translateNodeStarted(event fruntime.Event) *ChatEvent {
	for _, m := range nodeActionMap {
		if strings.HasPrefix(event.NodeID, m.prefix) {
			return &ChatEvent{
				Type:      m.action,
				Message:   m.message,
				NodeID:    event.NodeID,
				Timestamp: event.Timestamp,
			}
		}
	}
	return nil
}

func translateChunk(event fruntime.Event, action ActionType) *ChatEvent {
	text := extractPayloadString(event.Payload, "text")
	if text == "" {
		return nil
	}
	return &ChatEvent{
		Type:      action,
		Content:   text,
		NodeID:    event.NodeID,
		Timestamp: event.Timestamp,
	}
}

func translateToolCalled(event fruntime.Event) *ChatEvent {
	name := extractPayloadString(event.Payload, "name")
	msg := "正在调用工具..."
	if name != "" {
		msg = "正在调用工具: " + name
	}
	return &ChatEvent{
		Type:      ActionCallingTool,
		Message:   msg,
		NodeID:    event.NodeID,
		Timestamp: event.Timestamp,
	}
}

func translateToolFailed(event fruntime.Event) *ChatEvent {
	errMsg := extractPayloadString(event.Payload, "error")
	msg := "工具调用失败"
	if errMsg != "" {
		msg = "工具调用失败: " + errMsg
	}
	return &ChatEvent{
		Type:      ActionToolResult,
		Message:   msg,
		NodeID:    event.NodeID,
		Timestamp: event.Timestamp,
	}
}

func translateRunFailed(event fruntime.Event) *ChatEvent {
	errMsg := extractPayloadString(event.Payload, "error_message")
	msg := "执行失败"
	if errMsg != "" {
		msg = "执行失败: " + errMsg
	}
	return &ChatEvent{
		Type:      ActionError,
		Message:   msg,
		Timestamp: event.Timestamp,
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
