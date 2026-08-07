package trigger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
)

const (
	chatCommandNew  = "/new"
	chatCommandStop = "/stop"
	chatCommandHelp = "/help"
)

type activeChatExecution struct {
	cancel context.CancelFunc
}

func (s *Service) handleChatCommand(ctx context.Context, item Trigger, message chatchannel.InboundMessage, sink chatcap.ReplySink) (ChatResult, bool, error) {
	command := parseChatCommand(message.Content)
	if command == "" {
		return ChatResult{}, false, nil
	}
	identity := ChatConversationIdentity{
		TriggerID:             item.ID,
		UserID:                message.UserID,
		ChannelConversationID: message.ConversationID,
	}
	result := ChatResult{Command: command}
	var reply string
	switch command {
	case chatCommandNew:
		unlock := s.lockChatRoute(identity)
		s.cancelChatExecutions(chatRouteKey(identity))
		conversation, err := s.chatConversationStore.CreateChatConversation(ctx, ChatConversation{
			TriggerID:             identity.TriggerID,
			UserID:                identity.UserID,
			ChannelConversationID: identity.ChannelConversationID,
			CreatedAt:             s.now().UTC(),
		})
		unlock()
		if err != nil {
			return ChatResult{}, true, fmt.Errorf("create chat conversation: %w", err)
		}
		result.ConversationID = conversation.ID
		reply = fmt.Sprintf("New conversation started: %s", conversation.ID)
	case chatCommandStop:
		unlock := s.lockChatRoute(identity)
		conversation, err := s.chatConversationStore.CurrentChatConversation(ctx, identity)
		if err == nil {
			result.ConversationID = conversation.ID
		} else if !errors.Is(err, ErrNotFound) {
			unlock()
			return ChatResult{}, true, fmt.Errorf("load current chat conversation: %w", err)
		}
		canceled := s.cancelChatExecutions(chatRouteKey(identity))
		unlock()
		if canceled > 0 {
			reply = "Response stopped."
		} else {
			reply = "No response is currently running."
		}
	case chatCommandHelp:
		reply = "/new - start a new conversation\n/stop - stop the current response\n/help - show available commands"
	}
	if err := sink.Emit(ctx, chatcap.Reply{Kind: chatcap.ReplyFinish, Content: reply, Sequence: 1}); err != nil {
		return result, true, err
	}
	result.FinalReply = reply
	return result, true, nil
}

func parseChatCommand(content string) string {
	command := strings.ToLower(strings.TrimSpace(content))
	switch command {
	case chatCommandNew, chatCommandStop, chatCommandHelp:
		return command
	default:
		return ""
	}
}

func (s *Service) prepareChatExecution(ctx context.Context, item Trigger, message chatchannel.InboundMessage) (context.Context, chatchannel.InboundMessage, func(), error) {
	identity := ChatConversationIdentity{
		TriggerID:             item.ID,
		UserID:                message.UserID,
		ChannelConversationID: message.ConversationID,
	}
	unlockRoute := s.lockChatRoute(identity)
	conversation, err := s.chatConversationStore.CurrentChatConversation(ctx, identity)
	if errors.Is(err, ErrNotFound) {
		conversation, err = s.chatConversationStore.CreateChatConversation(ctx, ChatConversation{
			TriggerID:             identity.TriggerID,
			UserID:                identity.UserID,
			ChannelConversationID: identity.ChannelConversationID,
			CreatedAt:             s.now().UTC(),
		})
	}
	if err != nil {
		unlockRoute()
		return nil, chatchannel.InboundMessage{}, nil, fmt.Errorf("resolve chat conversation: %w", err)
	}

	executionCtx, cancel := context.WithCancel(ctx)
	execution := &activeChatExecution{cancel: cancel}
	key := chatRouteKey(identity)
	s.mu.Lock()
	if s.activeChats[key] == nil {
		s.activeChats[key] = make(map[*activeChatExecution]struct{})
	}
	s.activeChats[key][execution] = struct{}{}
	s.mu.Unlock()
	unlockRoute()

	message.ChannelConversationID = identity.ChannelConversationID
	message.ConversationID = conversation.ID
	message = normalizeChatMessageMetadata(message, chatChannelID(item))
	cleanup := func() {
		cancel()
		s.mu.Lock()
		delete(s.activeChats[key], execution)
		if len(s.activeChats[key]) == 0 {
			delete(s.activeChats, key)
		}
		s.mu.Unlock()
	}
	return executionCtx, message, cleanup, nil
}

func (s *Service) cancelChatExecutions(key string) int {
	s.mu.Lock()
	executions := make([]*activeChatExecution, 0, len(s.activeChats[key]))
	for execution := range s.activeChats[key] {
		executions = append(executions, execution)
	}
	s.mu.Unlock()
	for _, execution := range executions {
		execution.cancel()
	}
	return len(executions)
}

func (s *Service) lockChatRoute(identity ChatConversationIdentity) func() {
	key := chatRouteKey(identity)
	s.mu.Lock()
	lock := s.chatRouteLocks[key]
	if lock == nil {
		lock = &chatHistoryLock{}
		s.chatRouteLocks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.chatRouteLocks, key)
		}
		s.mu.Unlock()
	}
}

func chatRouteKey(identity ChatConversationIdentity) string {
	return strings.TrimSpace(identity.TriggerID) + "\x00" + strings.TrimSpace(identity.UserID) + "\x00" + strings.TrimSpace(identity.ChannelConversationID)
}
