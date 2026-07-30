package trigger

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
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
)

const (
	APIKeyQueryParameter  = "api_key"
	legacySignatureHeader = "X-Webhook-Signature"
)

const (
	DefaultRecordLimit = 100
	MaxRecordLimit     = 500
)

type RunStarter interface {
	Start(context.Context, *state.State) (runtime.RunRecord, *state.State, error)
}

// AsyncRunStarter returns after run.started has been published. done is closed
// only after the background execution has stopped.
type AsyncRunStarter interface {
	StartAsync(context.Context, *state.State) (runtime.RunRecord, <-chan struct{}, error)
}

type RunnerResolver interface {
	Resolve(context.Context, Target) (RunStarter, error)
}

type RunnerResolverFunc func(context.Context, Target) (RunStarter, error)

func (f RunnerResolverFunc) Resolve(ctx context.Context, target Target) (RunStarter, error) {
	return f(ctx, target)
}

type Service struct {
	store        Store
	resolver     RunnerResolver
	chatRegistry *chatchannel.Registry
	now          func() time.Time

	operationMu  sync.Mutex
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	schedules    map[string]*scheduleEntry
	chatChannels map[string]*chatChannelEntry
	activeRuns   map[string]int
}

type scheduleEntry struct {
	cron *cron.Cron
	id   cron.EntryID
}

type chatChannelEntry struct {
	channel string
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
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
		store:        store,
		resolver:     resolver,
		chatRegistry: chatchannel.NewDefaultRegistry(),
		now:          time.Now,
		schedules:    make(map[string]*scheduleEntry),
		chatChannels: make(map[string]*chatChannelEntry),
		activeRuns:   make(map[string]int),
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
	if err := s.store.Create(ctx, trigger); err != nil {
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

	previous, err := s.store.Get(ctx, trigger.ID)
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
	if err := s.store.Update(ctx, trigger); err != nil {
		return Trigger{}, err
	}
	s.replaceSchedule(trigger.ID, schedule)
	s.replaceChatChannel(trigger.ID, trigger.Chat, channel)
	return trigger, nil
}

func (s *Service) Get(ctx context.Context, id string) (Trigger, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Trigger, error) {
	return s.store.List(ctx)
}

func (s *Service) ListRecords(ctx context.Context, triggerID string, limit int) ([]Record, error) {
	if s == nil {
		return nil, fmt.Errorf("trigger service is nil")
	}
	if limit <= 0 {
		limit = DefaultRecordLimit
	}
	if limit > MaxRecordLimit {
		limit = MaxRecordLimit
	}
	return s.store.ListRecords(ctx, strings.TrimSpace(triggerID), limit)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if s == nil {
		return fmt.Errorf("trigger service is nil")
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if err := s.store.Delete(ctx, id); err != nil {
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

	items, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	schedules := make(map[string]*scheduleEntry)
	channels := make(map[string]chatchannel.Instance)
	channelNames := make(map[string]string)
	for _, item := range items {
		item = item.Normalize(s.now())
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
	for _, entry := range channels {
		<-entry.done
	}
	return nil
}

func (s *Service) InvokeWebhook(ctx context.Context, id string, body []byte, apiKey string, headers map[string]string) (runtime.RunRecord, error) {
	return s.invokeWebhook(ctx, id, apiKey, headers, func() (any, error) {
		if len(strings.TrimSpace(string(body))) == 0 {
			return map[string]any{}, nil
		}
		var payload any
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
		}
		return payload, nil
	})
}

func (s *Service) InvokeWebhookInput(ctx context.Context, id string, input any, apiKey string, headers map[string]string) (runtime.RunRecord, error) {
	return s.invokeWebhook(ctx, id, apiKey, headers, func() (any, error) {
		return input, nil
	})
}

func (s *Service) invokeWebhook(ctx context.Context, id string, apiKey string, headers map[string]string, input func() (any, error)) (runtime.RunRecord, error) {
	trigger, err := s.store.Get(ctx, id)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	if trigger.Type != TypeWebhook {
		return runtime.RunRecord{}, fmt.Errorf("%w: trigger %q is not a webhook", ErrTypeMismatch, id)
	}
	if !trigger.Enabled {
		return runtime.RunRecord{}, ErrDisabled
	}
	if err := verifyWebhookAPIKey(trigger.Webhook, apiKey); err != nil {
		return runtime.RunRecord{}, err
	}
	payload, err := input()
	if err != nil {
		return runtime.RunRecord{}, err
	}
	metadata := webhookMetadata(headers, trigger.Webhook)
	return s.invoke(ctx, trigger, payload, metadata, "webhook")
}

func (s *Service) InvokeSchedule(ctx context.Context, id string) (runtime.RunRecord, error) {
	trigger, err := s.store.Get(ctx, id)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	if trigger.Type != TypeSchedule {
		return runtime.RunRecord{}, fmt.Errorf("%w: trigger %q is not a schedule", ErrTypeMismatch, id)
	}
	if !trigger.Enabled {
		return runtime.RunRecord{}, ErrDisabled
	}
	input := map[string]any{}
	if trigger.Schedule != nil && trigger.Schedule.Input != nil {
		input = trigger.Schedule.Input
	}
	return s.invoke(ctx, trigger, input, map[string]any{"scheduled_at": s.now().UTC().Format(time.RFC3339Nano)}, "schedule")
}

type ChatResult struct {
	Run        runtime.RunRecord `json:"run"`
	FinalReply string            `json:"final_reply,omitempty"`
}

func (s *Service) InvokeChat(ctx context.Context, id string, message chatcap.Message, sink chatcap.ReplySink) (ChatResult, error) {
	if s == nil {
		return ChatResult{}, fmt.Errorf("trigger service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return ChatResult{}, chatcap.ErrReplySinkUnavailable
	}
	message = message.Normalize()
	if err := message.Validate(); err != nil {
		return ChatResult{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	item, err := s.store.Get(ctx, id)
	if err != nil {
		return ChatResult{}, err
	}
	if item.Type != TypeChat {
		return ChatResult{}, fmt.Errorf("%w: trigger %q is not a chat trigger", ErrTypeMismatch, id)
	}
	if !item.Enabled {
		return ChatResult{}, ErrDisabled
	}

	now := s.now().UTC()
	record := Record{
		ID:          uuid.NewString(),
		TriggerID:   item.ID,
		TriggerType: item.Type,
		Target:      item.Target,
		Status:      runtime.RunStatusPending,
		TriggeredAt: now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateRecord(ctx, record); err != nil {
		return ChatResult{}, fmt.Errorf("create trigger record: %w", err)
	}

	result, runErr := s.invokeChatRun(ctx, item, message, sink)
	record.UpdatedAt = s.now().UTC()
	if result.Run.RunID != "" {
		runCopy := result.Run
		record.Run = &runCopy
		record.Status = result.Run.Status
	}
	if runErr != nil {
		if record.Status == "" || record.Status == runtime.RunStatusPending {
			record.Status = runtime.RunStatusFailed
		}
		record.ErrorMessage = runErr.Error()
	} else if record.Status == "" {
		record.Status = runtime.RunStatusCompleted
	}
	_ = s.store.UpdateRecord(context.WithoutCancel(ctx), record)
	return result, runErr
}

func (s *Service) invokeChatRun(ctx context.Context, item Trigger, message chatcap.Message, sink chatcap.ReplySink) (ChatResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	trackActive := false
	if item.Concurrency == ConcurrencySkip {
		s.mu.Lock()
		if s.activeRuns[item.ID] > 0 {
			s.mu.Unlock()
			return ChatResult{}, ErrBusy
		}
		s.activeRuns[item.ID]++
		s.mu.Unlock()
		trackActive = true
	}
	if trackActive {
		defer s.finishActive(item.ID)
	}

	runner, err := s.resolver.Resolve(ctx, item.Target)
	if err != nil {
		return ChatResult{}, err
	}
	if runner == nil {
		return ChatResult{}, fmt.Errorf("runner resolver returned nil")
	}
	metadata := make(map[string]any, len(message.Metadata)+2)
	for key, value := range message.Metadata {
		metadata[key] = value
	}
	if message.ID != "" {
		metadata["message_id"] = message.ID
	}
	if message.ConversationID != "" {
		metadata["conversation_id"] = message.ConversationID
	}
	initial, err := buildTriggerState(item, message.Content, metadata, string(TypeChat))
	if err != nil {
		return ChatResult{}, err
	}
	configuredSink := newChatInvocationSink(item.Chat, sink)
	executionCtx := runtime.WithRunnerEventObserver(ctx, newChatLLMStreamObserver(configuredSink))
	executionCtx = chatcap.WithReplySink(executionCtx, configuredSink)
	run, finalState, runErr := runner.Start(executionCtx, initial)
	finalReply, replyErr := chatFinalReply(finalState, item.Chat)
	if runErr == nil && replyErr != nil {
		runErr = replyErr
	}
	finishErr := configuredSink.finish(context.WithoutCancel(ctx), finalReply, runErr)
	if runErr == nil && finishErr != nil {
		runErr = finishErr
	}
	return ChatResult{Run: run, FinalReply: finalReply}, runErr
}

func buildTriggerState(item Trigger, input any, metadata map[string]any, triggerType string) (*state.State, error) {
	initial := state.FromMap(item.InitialState)
	if err := state.SetPath(initial, "shared.request", map[string]any{
		"input":    input,
		"metadata": metadata,
	}); err != nil {
		return nil, fmt.Errorf("initialize trigger request state: %w", err)
	}
	if err := state.SetPath(initial, "shared.trigger", map[string]any{
		"id":   item.ID,
		"type": triggerType,
	}); err != nil {
		return nil, fmt.Errorf("initialize trigger identity state: %w", err)
	}
	return initial, nil
}

func chatFinalReply(finalState *state.State, spec *ChatSpec) (string, error) {
	path := "shared.final.answer"
	if spec != nil && strings.TrimSpace(spec.ReplyPath) != "" {
		path = spec.ReplyPath
	}
	value, ok := state.ReadPath(finalState, path)
	if !ok || value == nil {
		return "", nil
	}
	reply, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("chat reply path %q must contain a string, got %T", path, value)
	}
	return strings.TrimSpace(reply), nil
}

type chatInvocationSink struct {
	mu            sync.Mutex
	target        chatcap.ReplySink
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

func newChatInvocationSink(spec *ChatSpec, target chatcap.ReplySink) *chatInvocationSink {
	sink := &chatInvocationSink{target: target}
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
	switch reply.Kind {
	case chatcap.ReplyUpdate:
		s.streamed = true
		s.lastUpdate = reply.Content
	case chatcap.ReplyMessage:
		s.messageSent = true
		s.lastMessage = reply.Content
	}
	return s.target.Emit(ctx, reply)
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

func (s *Service) invoke(ctx context.Context, item Trigger, input any, metadata map[string]any, triggerType string) (runtime.RunRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now().UTC()
	record := Record{
		ID:          uuid.NewString(),
		TriggerID:   item.ID,
		TriggerType: item.Type,
		Target:      item.Target,
		Status:      runtime.RunStatusPending,
		TriggeredAt: now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateRecord(ctx, record); err != nil {
		return runtime.RunRecord{}, fmt.Errorf("create trigger record: %w", err)
	}

	run, runErr := s.invokeRun(ctx, item, input, metadata, triggerType)
	record.UpdatedAt = s.now().UTC()
	if runErr != nil {
		record.Status = runtime.RunStatusFailed
		record.ErrorMessage = runErr.Error()
	} else {
		runCopy := run
		record.Run = &runCopy
		record.Status = run.Status
		if record.Status == "" {
			record.Status = runtime.RunStatusRunning
		}
	}
	_ = s.store.UpdateRecord(context.WithoutCancel(ctx), record)
	return run, runErr
}
func (s *Service) invokeRun(ctx context.Context, trigger Trigger, input any, metadata map[string]any, triggerType string) (runtime.RunRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	trackActive := false
	if trigger.Concurrency == ConcurrencySkip {
		s.mu.Lock()
		if s.activeRuns[trigger.ID] > 0 {
			s.mu.Unlock()
			return runtime.RunRecord{}, ErrBusy
		}
		s.activeRuns[trigger.ID]++
		s.mu.Unlock()
		trackActive = true
	}
	defer func() {
		if trackActive {
			s.finishActive(trigger.ID)
		}
	}()
	runner, err := s.resolver.Resolve(ctx, trigger.Target)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	if runner == nil {
		return runtime.RunRecord{}, fmt.Errorf("runner resolver returned nil")
	}
	initial, err := buildTriggerState(trigger, input, metadata, triggerType)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	if trigger.Type == TypeWebhook && trigger.Webhook != nil {
		if err := applyWebhookStateMappings(initial, input, trigger.Webhook.StateMappings); err != nil {
			return runtime.RunRecord{}, err
		}
	}
	executionCtx := s.executionContext(ctx)
	if asyncRunner, ok := runner.(AsyncRunStarter); ok {
		run, done, err := asyncRunner.StartAsync(executionCtx, initial)
		if err != nil {
			return runtime.RunRecord{}, err
		}
		if trackActive && done != nil {
			trackActive = false
			go func() {
				<-done
				s.finishActive(trigger.ID)
			}()
		}
		return run, nil
	}
	run, _, err := runner.Start(executionCtx, initial)
	return run, err
}

func (s *Service) executionContext(fallback context.Context) context.Context {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx != nil {
		return ctx
	}
	if fallback == nil {
		return context.Background()
	}
	return context.WithoutCancel(fallback)
}

func applyWebhookStateMappings(initial *state.State, input any, mappings []WebhookStateMapping) error {
	if err := validateWebhookStateMappings(mappings); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidStateMapping, err)
	}
	for _, mapping := range mappings {
		value, ok := webhookParameterValue(input, mapping.Parameter)
		if !ok {
			continue
		}
		if err := state.SetPath(initial, mapping.StatePath, value); err != nil {
			return fmt.Errorf("%w: map parameter %q to %q: %v", ErrInvalidStateMapping, mapping.Parameter, mapping.StatePath, err)
		}
	}
	return nil
}

func webhookParameterValue(input any, parameter string) (any, bool) {
	if parameter == "$" {
		return input, true
	}
	if value, ok := state.ResolveStateValue(input, strings.Split(parameter, ".")); ok {
		return value, true
	}
	// Flat inputs may store a dotted key such as "user.id" instead of nesting it.
	return state.ResolveStateValue(input, []string{parameter})
}

func (s *Service) finishActive(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeRuns[id] <= 1 {
		delete(s.activeRuns, id)
		return
	}
	s.activeRuns[id]--
}

func (s *Service) buildSchedule(trigger Trigger) (*scheduleEntry, error) {
	if !trigger.Enabled || trigger.Type != TypeSchedule {
		return nil, nil
	}
	location := time.UTC
	if trigger.Schedule.Timezone != "" {
		loaded, err := time.LoadLocation(trigger.Schedule.Timezone)
		if err != nil {
			return nil, err
		}
		location = loaded
	}
	scheduler := cron.New(cron.WithLocation(location))
	entryID, err := scheduler.AddFunc(trigger.Schedule.Cron, func() {
		ctx := s.scheduleContext()
		if ctx == nil {
			return
		}
		_, _ = s.InvokeSchedule(ctx, trigger.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: parse schedule %q: %v", ErrInvalidTrigger, trigger.Schedule.Cron, err)
	}
	return &scheduleEntry{cron: scheduler, id: entryID}, nil
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
		Handler: chatchannel.HandlerFunc(func(ctx context.Context, message chatcap.Message, sink chatcap.ReplySink) error {
			_, err := s.InvokeChat(ctx, item.ID, message, sink)
			return err
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTrigger, err)
	}
	return instance, nil
}

func (s *Service) replaceSchedule(id string, schedule *scheduleEntry) {
	s.mu.Lock()
	previous := s.schedules[id]
	if schedule != nil && s.cancel != nil {
		s.schedules[id] = schedule
		schedule.cron.Start()
	} else {
		delete(s.schedules, id)
	}
	s.mu.Unlock()
	if previous != nil {
		previous.cron.Remove(previous.id)
		previous.cron.Stop()
	}
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

func (s *Service) scheduleContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

func verifyWebhookAPIKey(spec *WebhookSpec, provided string) error {
	if spec == nil {
		return nil
	}
	expected := spec.APIKey
	if expected == "" {
		expected = spec.Secret
	}
	if expected == "" {
		return nil
	}
	expectedHash := sha256.Sum256([]byte(expected))
	providedHash := sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
		return ErrInvalidAPIKey
	}
	return nil
}

func webhookMetadata(headers map[string]string, spec *WebhookSpec) map[string]any {
	signatureHeader := legacySignatureHeader
	if spec != nil && strings.TrimSpace(spec.SignatureHeader) != "" {
		signatureHeader = spec.SignatureHeader
	}
	metadata := make(map[string]any, len(headers))
	for key, value := range headers {
		switch {
		case strings.EqualFold(key, signatureHeader),
			strings.EqualFold(key, "Authorization"),
			strings.EqualFold(key, "Proxy-Authorization"),
			strings.EqualFold(key, "Cookie"),
			strings.EqualFold(key, "Set-Cookie"):
			continue
		default:
			metadata[key] = value
		}
	}
	return metadata
}

func validateScheduleExpression(trigger Trigger) error {
	if trigger.Type != TypeSchedule || trigger.Schedule == nil {
		return nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(trigger.Schedule.Cron); err != nil {
		return fmt.Errorf("%w: parse schedule %q: %v", ErrInvalidTrigger, trigger.Schedule.Cron, err)
	}
	return nil
}
