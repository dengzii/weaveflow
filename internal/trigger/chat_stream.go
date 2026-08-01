package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/runtime"
)

type chatInvocationSink struct {
	mu            sync.Mutex
	target        chatcap.ReplySink
	recordReply   func(context.Context, chatcap.Reply) error
	streamUpdates bool
	streamNodeIDs map[string]struct{}
	sequence      int64
	streamed      bool
	lastUpdate    string
	messageSent   bool
	lastMessage   string
}

type chatLLMStreamObserver struct {
	mu      sync.Mutex
	target  chatcap.ReplySink
	content map[string]*strings.Builder
}

type chatLLMEventPayload struct {
	CallID string `json:"call_id"`
	Text   string `json:"text"`
}

func newChatLLMStreamObserver(target chatcap.ReplySink) *chatLLMStreamObserver {
	return &chatLLMStreamObserver{
		target:  target,
		content: map[string]*strings.Builder{},
	}
}

func (o *chatLLMStreamObserver) Observe(ctx context.Context, event runtime.Event) error {
	if o == nil || o.target == nil {
		return nil
	}
	if event.Type != runtime.EventLLMContentChunk && event.Type != runtime.EventLLMContent && event.Type != runtime.EventLLMCall {
		return nil
	}
	var payload chatLLMEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode %s event payload: %w", event.Type, err)
	}
	key := chatLLMStreamKey(event, payload.CallID)

	o.mu.Lock()
	defer o.mu.Unlock()
	if event.Type != runtime.EventLLMContentChunk {
		delete(o.content, key)
		return nil
	}
	if payload.Text == "" {
		return nil
	}
	builder := o.content[key]
	if builder == nil {
		builder = &strings.Builder{}
		o.content[key] = builder
	}
	_, _ = builder.WriteString(payload.Text)
	if strings.TrimSpace(payload.Text) == "" {
		return nil
	}
	return o.target.Emit(ctx, chatcap.Reply{
		Kind:    chatcap.ReplyUpdate,
		Content: builder.String(),
		NodeID:  event.NodeID,
	})
}

func chatLLMStreamKey(event runtime.Event, callID string) string {
	if callID = strings.TrimSpace(callID); callID != "" {
		return callID
	}
	return event.StepID + "\x00" + event.NodeID
}

func newChatInvocationSink(spec *ChatSpec, target chatcap.ReplySink, recordReply func(context.Context, chatcap.Reply) error) *chatInvocationSink {
	sink := &chatInvocationSink{target: target, recordReply: recordReply}
	if spec == nil {
		return sink
	}
	sink.streamUpdates = spec.StreamUpdates
	if len(spec.StreamNodeIDs) > 0 {
		sink.streamNodeIDs = make(map[string]struct{}, len(spec.StreamNodeIDs))
		for _, nodeID := range spec.StreamNodeIDs {
			sink.streamNodeIDs[nodeID] = struct{}{}
		}
	}
	return sink
}

func (s *chatInvocationSink) Emit(ctx context.Context, reply chatcap.Reply) error {
	if s == nil || s.target == nil {
		return chatcap.ErrReplySinkUnavailable
	}
	if reply.Kind == chatcap.ReplyUpdate {
		if !s.streamUpdates {
			return nil
		}
		if len(s.streamNodeIDs) > 0 {
			if _, ok := s.streamNodeIDs[reply.NodeID]; !ok {
				return nil
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	reply.Sequence = s.sequence
	if s.recordReply != nil {
		if err := s.recordReply(ctx, reply); err != nil {
			return err
		}
	}
	if err := s.target.Emit(ctx, reply); err != nil {
		return err
	}
	switch reply.Kind {
	case chatcap.ReplyUpdate:
		s.streamed = true
		s.lastUpdate = reply.Content
	case chatcap.ReplyMessage:
		s.messageSent = true
		s.lastMessage = reply.Content
	}
	return nil
}

func (s *chatInvocationSink) finish(ctx context.Context, content string, runErr error) error {
	s.mu.Lock()
	if strings.TrimSpace(content) == "" && s.streamed {
		content = s.lastUpdate
	}
	if !s.streamed && s.messageSent && content == s.lastMessage {
		content = ""
	}
	s.mu.Unlock()
	reply := chatcap.Reply{Kind: chatcap.ReplyFinish, Content: content}
	if runErr != nil {
		reply.Error = runErr.Error()
	}
	return s.Emit(ctx, reply)
}
