package trigger

import (
	"context"
	"fmt"
	"log"
	"strings"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
)

type chatChannelEntry struct {
	channel string
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

func (s *Service) ChatChannelDefinitions() []chatchannel.Definition {
	if s == nil || s.chatRegistry == nil {
		return nil
	}
	return s.chatRegistry.Definitions()
}

func (s *Service) ChatChannels() *chatchannel.Registry {
	if s == nil {
		return nil
	}
	return s.chatRegistry
}

func (s *Service) RedactChatChannelConfig(item Trigger) Trigger {
	if s == nil || s.chatRegistry == nil || item.Chat == nil {
		return item
	}
	chat := *item.Chat
	chat.ChannelConfig = s.chatRegistry.RedactConfig(chat.Channel, chat.ChannelConfig)
	item.Chat = &chat
	return item
}

func (s *Service) buildChatChannel(item Trigger) (chatchannel.Instance, error) {
	if item.Type != TypeChat || item.Chat == nil {
		return nil, nil
	}
	if s.chatRegistry == nil {
		return nil, fmt.Errorf("%w: chat channel registry is unavailable", ErrInvalidTrigger)
	}
	channelID := strings.TrimSpace(item.Chat.Channel)
	if err := s.chatRegistry.ValidateConfig(channelID, item.Chat.ChannelConfig); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTrigger, err)
	}
	if !item.Enabled {
		return nil, nil
	}
	instance, err := s.chatRegistry.NewInstance(channelID, chatchannel.InstanceConfig{
		TriggerID: item.ID,
		Config:    item.Chat.ChannelConfig,
		Handler: chatchannel.HandlerFunc(func(ctx context.Context, message chatchannel.InboundMessage, sink chatcap.ReplySink) error {
			_, err := s.InvokeChat(ctx, item.ID, message, sink)
			return err
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTrigger, err)
	}
	return instance, nil
}

func (s *Service) replaceChatChannel(id string, spec *ChatSpec, channel chatchannel.Instance) {
	s.mu.Lock()
	previous := s.chatChannels[id]
	delete(s.chatChannels, id)
	runtimeCtx := s.ctx
	s.mu.Unlock()
	if previous != nil {
		previous.cancel()
		<-previous.done
	}
	if channel == nil || runtimeCtx == nil {
		return
	}
	channelCtx, cancel := context.WithCancel(runtimeCtx)
	entry := &chatChannelEntry{ctx: channelCtx, cancel: cancel, done: make(chan struct{})}
	if spec != nil {
		entry.channel = spec.Channel
	}
	s.mu.Lock()
	if s.ctx != runtimeCtx {
		s.mu.Unlock()
		cancel()
		close(entry.done)
		return
	}
	s.chatChannels[id] = entry
	s.mu.Unlock()
	go s.runChatChannel(id, channel, entry)
}

func (s *Service) runChatChannel(id string, channel chatchannel.Instance, entry *chatChannelEntry) {
	err := channel.Run(entry.ctx)
	if err != nil && entry.ctx.Err() == nil {
		log.Printf("chat channel %q for trigger %q stopped: %v", entry.channel, id, err)
	}
	close(entry.done)
	s.mu.Lock()
	if s.chatChannels[id] == entry {
		delete(s.chatChannels, id)
	}
	s.mu.Unlock()
}
