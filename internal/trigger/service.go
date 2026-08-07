package trigger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/google/uuid"
)

var (
	ErrInvalidTrigger      = errors.New("invalid trigger")
	ErrDisabled            = errors.New("trigger is disabled")
	ErrBusy                = errors.New("trigger is already running")
	ErrInvalidAPIKey       = errors.New("webhook api_key is invalid")
	ErrInvalidPayload      = errors.New("invalid webhook payload")
	ErrInvalidStateMapping = errors.New("invalid webhook state mapping")
	ErrInvalidTarget       = errors.New("invalid trigger target")
	ErrTypeMismatch        = errors.New("trigger type mismatch")
	ErrChatReplyMissing    = errors.New("chat graph completed without a reply")
)

const APIKeyQueryParameter = "api_key"

const (
	DefaultRecordLimit      = 100
	MaxRecordLimit          = 500
	DefaultChatHistoryLimit = 100
)

type Service struct {
	triggerStore          TriggerStore
	invocationStore       InvocationStore
	chatHistoryStore      ChatHistoryStore
	chatConversationStore ChatConversationStore
	resolver              RunnerResolver
	chatRegistry          *chatchannel.Registry
	now                   func() time.Time

	operationMu    sync.Mutex
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	schedules      map[string]*scheduleEntry
	chatChannels   map[string]*chatChannelEntry
	activeRuns     map[string]int
	chatLocks      map[string]*chatHistoryLock
	chatRouteLocks map[string]*chatHistoryLock
	activeChats    map[string]map[*activeChatExecution]struct{}
}

type chatHistoryLock struct {
	mu   sync.Mutex
	refs int
}

type ServiceOption func(*Service) error

func WithChatChannels(registry *chatchannel.Registry) ServiceOption {
	return func(service *Service) error {
		if registry == nil {
			return fmt.Errorf("chat channel registry is nil")
		}
		service.chatRegistry = registry
		return nil
	}
}

func NewService(store Store, resolver RunnerResolver, options ...ServiceOption) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("trigger store is required")
	}
	if resolver == nil {
		return nil, fmt.Errorf("runner resolver is required")
	}
	service := &Service{
		triggerStore:          store,
		invocationStore:       store,
		chatHistoryStore:      store,
		chatConversationStore: store,
		resolver:              resolver,
		chatRegistry:          chatchannel.NewDefaultRegistry(),
		now:                   time.Now,
		schedules:             make(map[string]*scheduleEntry),
		chatChannels:          make(map[string]*chatChannelEntry),
		activeRuns:            make(map[string]int),
		chatLocks:             make(map[string]*chatHistoryLock),
		chatRouteLocks:        make(map[string]*chatHistoryLock),
		activeChats:           make(map[string]map[*activeChatExecution]struct{}),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) Create(ctx context.Context, trigger Trigger) (Trigger, error) {
	if s == nil {
		return Trigger{}, fmt.Errorf("trigger service is nil")
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	now := s.now()
	trigger = trigger.Normalize(now)
	if trigger.ID == "" {
		trigger.ID = uuid.NewString()
	}
	if err := trigger.Validate(); err != nil {
		return Trigger{}, err
	}
	if err := validateScheduleExpression(trigger); err != nil {
		return Trigger{}, err
	}
	schedule, err := s.buildSchedule(trigger)
	if err != nil {
		return Trigger{}, err
	}
	channel, err := s.buildChatChannel(trigger)
	if err != nil {
		return Trigger{}, err
	}
	if err := s.triggerStore.Create(ctx, trigger); err != nil {
		return Trigger{}, err
	}
	s.replaceSchedule(trigger.ID, schedule)
	s.replaceChatChannel(trigger.ID, trigger.Chat, channel)
	return trigger, nil
}

func (s *Service) Update(ctx context.Context, trigger Trigger) (Trigger, error) {
	if s == nil {
		return Trigger{}, fmt.Errorf("trigger service is nil")
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	previous, err := s.triggerStore.Get(ctx, trigger.ID)
	if err != nil {
		return Trigger{}, err
	}
	if trigger.Chat != nil && previous.Chat != nil {
		if strings.TrimSpace(trigger.Chat.Channel) == "" {
			trigger.Chat.Channel = previous.Chat.Channel
		}
		if strings.TrimSpace(trigger.Chat.Channel) == strings.TrimSpace(previous.Chat.Channel) {
			trigger.Chat.ChannelConfig = s.chatRegistry.MergeWriteOnlyConfig(
				trigger.Chat.Channel,
				previous.Chat.ChannelConfig,
				trigger.Chat.ChannelConfig,
			)
		}
	}
	trigger = trigger.Normalize(previous.CreatedAt)
	trigger.CreatedAt = previous.CreatedAt
	trigger.UpdatedAt = s.now()
	if err := trigger.Validate(); err != nil {
		return Trigger{}, err
	}
	if err := validateScheduleExpression(trigger); err != nil {
		return Trigger{}, err
	}
	schedule, err := s.buildSchedule(trigger)
	if err != nil {
		return Trigger{}, err
	}
	channel, err := s.buildChatChannel(trigger)
	if err != nil {
		return Trigger{}, err
	}
	if err := s.triggerStore.Update(ctx, trigger); err != nil {
		return Trigger{}, err
	}
	s.replaceSchedule(trigger.ID, schedule)
	s.replaceChatChannel(trigger.ID, trigger.Chat, channel)
	return trigger, nil
}

func (s *Service) Get(ctx context.Context, id string) (Trigger, error) {
	return s.triggerStore.Get(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Trigger, error) {
	return s.triggerStore.List(ctx)
}

func (s *Service) ReplaceGraph(ctx context.Context, graphID string, items []Trigger) ([]Trigger, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger service is nil")
	}
	graphID = strings.TrimSpace(graphID)
	if graphID == "" {
		return nil, fmt.Errorf("%w: graph_id is required", ErrInvalidTarget)
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	existing, err := s.triggerStore.List(ctx)
	if err != nil {
		return nil, err
	}
	existingByID := make(map[string]Trigger, len(existing))
	oldGraphIDs := make(map[string]struct{})
	for _, item := range existing {
		existingByID[item.ID] = item
		if item.Target.GraphID == graphID {
			oldGraphIDs[item.ID] = struct{}{}
		}
	}

	now := s.now()
	next := make([]Trigger, 0, len(items))
	schedules := make(map[string]*scheduleEntry, len(items))
	channels := make(map[string]chatchannel.Instance, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.Target = Target{GraphID: graphID}
		previous, exists := existingByID[item.ID]
		if exists && previous.Target.GraphID != graphID {
			return nil, ErrExists
		}
		if exists {
			if item.Webhook != nil && item.Webhook.APIKey == "" && previous.Webhook != nil {
				item.Webhook.APIKey = previous.Webhook.APIKey
			}
			if item.Chat != nil && previous.Chat != nil && strings.TrimSpace(item.Chat.Channel) == strings.TrimSpace(previous.Chat.Channel) {
				item.Chat.ChannelConfig = s.chatRegistry.MergeWriteOnlyConfig(
					item.Chat.Channel,
					previous.Chat.ChannelConfig,
					item.Chat.ChannelConfig,
				)
			}
			item = item.Normalize(previous.CreatedAt)
			item.CreatedAt = previous.CreatedAt
			item.UpdatedAt = now
		} else {
			item = item.Normalize(now)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, ErrExists
		}
		seen[item.ID] = struct{}{}
		if err := item.Validate(); err != nil {
			return nil, err
		}
		if err := validateScheduleExpression(item); err != nil {
			return nil, err
		}
		schedule, err := s.buildSchedule(item)
		if err != nil {
			return nil, err
		}
		channel, err := s.buildChatChannel(item)
		if err != nil {
			return nil, err
		}
		if schedule != nil {
			schedules[item.ID] = schedule
		}
		if channel != nil {
			channels[item.ID] = channel
		}
		next = append(next, item)
	}

	if err := s.triggerStore.ReplaceGraph(ctx, graphID, next); err != nil {
		return nil, err
	}
	for id := range oldGraphIDs {
		if _, keep := seen[id]; keep {
			continue
		}
		s.replaceSchedule(id, nil)
		s.replaceChatChannel(id, nil, nil)
	}
	for _, item := range next {
		s.replaceSchedule(item.ID, schedules[item.ID])
		s.replaceChatChannel(item.ID, item.Chat, channels[item.ID])
	}
	return next, nil
}

func (s *Service) ListRecords(ctx context.Context, triggerID string, limit int) ([]Record, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger service is nil")
	}
	triggerID = strings.TrimSpace(triggerID)
	if limit <= 0 {
		limit = DefaultRecordLimit
	}
	if limit > MaxRecordLimit {
		limit = MaxRecordLimit
	}
	return s.invocationStore.ListRecords(ctx, triggerID, limit)
}

func (s *Service) ListChatHistory(ctx context.Context, filter ChatHistoryFilter) ([]ChatHistory, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger service is nil")
	}
	filter.TriggerID = strings.TrimSpace(filter.TriggerID)
	filter.UserID = strings.TrimSpace(filter.UserID)
	filter.ChannelConversationID = strings.TrimSpace(filter.ChannelConversationID)
	filter.ConversationID = strings.TrimSpace(filter.ConversationID)
	if filter.Limit <= 0 {
		filter.Limit = DefaultChatHistoryLimit
	}
	if filter.Limit > MaxRecordLimit {
		filter.Limit = MaxRecordLimit
	}
	return s.chatHistoryStore.ListChatHistory(ctx, filter)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if s == nil {
		return fmt.Errorf("trigger service is nil")
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if err := s.triggerStore.Delete(ctx, id); err != nil {
		return err
	}
	s.replaceSchedule(id, nil)
	s.replaceChatChannel(id, nil, nil)
	return nil
}

func (s *Service) Start(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("trigger service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	items, err := s.triggerStore.List(ctx)
	if err != nil {
		return err
	}
	schedules := make(map[string]*scheduleEntry)
	channels := make(map[string]chatchannel.Instance)
	channelNames := make(map[string]string)
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("load trigger %q: %w", item.ID, err)
		}
		if err := validateScheduleExpression(item); err != nil {
			return fmt.Errorf("load trigger %q: %w", item.ID, err)
		}
		schedule, err := s.buildSchedule(item)
		if err != nil {
			return fmt.Errorf("load trigger %q: %w", item.ID, err)
		}
		if schedule != nil {
			schedules[item.ID] = schedule
		}
		channel, err := s.buildChatChannel(item)
		if err != nil {
			return fmt.Errorf("load trigger %q: %w", item.ID, err)
		}
		if channel != nil {
			channels[item.ID] = channel
			channelNames[item.ID] = item.Chat.Channel
		}
	}

	s.mu.Lock()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.schedules = schedules
	s.chatChannels = make(map[string]*chatChannelEntry, len(channels))
	pendingChannels := make(map[string]chatchannel.Instance, len(channels))
	for _, schedule := range schedules {
		schedule.cron.Start()
	}
	for id, channel := range channels {
		channelCtx, cancel := context.WithCancel(s.ctx)
		entry := &chatChannelEntry{channel: channelNames[id], ctx: channelCtx, cancel: cancel, done: make(chan struct{})}
		s.chatChannels[id] = entry
		pendingChannels[id] = channel
	}
	s.mu.Unlock()
	for id, channel := range pendingChannels {
		s.mu.Lock()
		entry := s.chatChannels[id]
		s.mu.Unlock()
		if entry != nil {
			go s.runChatChannel(id, channel, entry)
		}
	}
	return nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.ctx = nil
	entries := s.schedules
	s.schedules = make(map[string]*scheduleEntry)
	channels := s.chatChannels
	s.chatChannels = make(map[string]*chatChannelEntry)
	activeChats := make([]*activeChatExecution, 0)
	for _, executions := range s.activeChats {
		for execution := range executions {
			activeChats = append(activeChats, execution)
		}
	}
	s.activeChats = make(map[string]map[*activeChatExecution]struct{})
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, entry := range entries {
		entry.cron.Stop()
	}
	for _, entry := range channels {
		entry.cancel()
	}
	for _, execution := range activeChats {
		execution.cancel()
	}
	for _, entry := range channels {
		<-entry.done
	}
	return nil
}
