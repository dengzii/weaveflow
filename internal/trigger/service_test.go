package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type chatRecordingStarter struct {
	initial *state.State
}

func (s *chatRecordingStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	s.initial = initial.Clone()
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContentChunk, "step-worker", "worker", "call-worker", "ignored"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContentChunk, "step-answer", "answer", "call-answer", "dra"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContentChunk, "step-answer", "answer", "call-answer", "ft"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := observeChatLLMEvent(ctx, runtime.EventLLMContent, "step-answer", "answer", "call-answer", "draft"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := chatcap.EmitReply(ctx, chatcap.Reply{Kind: chatcap.ReplyMessage, Content: "side", NodeID: "notify"}); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	if err := state.SetPath(initial, "shared.final.answer", "final"); err != nil {
		return runtime.RunRecord{}, initial, err
	}
	return runtime.RunRecord{RunID: "chat-run", Status: runtime.RunStatusCompleted}, initial, nil
}

func observeChatLLMEvent(ctx context.Context, eventType runtime.EventType, stepID, nodeID, callID, text string) error {
	payload, err := json.Marshal(map[string]string{"call_id": callID, "text": text})
	if err != nil {
		return err
	}
	observer := runtime.RunnerEventObserverFromContext(ctx)
	if observer == nil {
		return errors.New("runtime event observer is unavailable")
	}
	return observer.Observe(ctx, runtime.Event{
		RunID:   "chat-run",
		StepID:  stepID,
		NodeID:  nodeID,
		Type:    eventType,
		Payload: payload,
	})
}

func TestServiceInvokeChatStreamsAndSendsMultipleReplies(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &chatRecordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:      "chat",
		Type:    TypeChat,
		Enabled: true,
		Target:  Target{GraphID: "graph"},
		Chat: &ChatSpec{
			StreamUpdates: true,
			StreamNodeIDs: []string{"answer"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var replies []chatcap.Reply
	result, err := service.InvokeChat(context.Background(), "chat", chatcap.Message{
		ID:             "message-1",
		ConversationID: "conversation-1",
		Content:        "hello",
		Metadata:       map[string]any{"channel": "test"},
	}, chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.RunID != "chat-run" || result.FinalReply != "final" {
		t.Fatalf("result = %#v", result)
	}
	if len(replies) != 4 {
		t.Fatalf("replies = %#v", replies)
	}
	if replies[0].Kind != chatcap.ReplyUpdate || replies[0].Content != "dra" || replies[0].Sequence != 1 {
		t.Fatalf("update = %#v", replies[0])
	}
	if replies[1].Kind != chatcap.ReplyUpdate || replies[1].Content != "draft" || replies[1].Sequence != 2 {
		t.Fatalf("update = %#v", replies[1])
	}
	if replies[2].Kind != chatcap.ReplyMessage || replies[2].Content != "side" || replies[2].Sequence != 3 {
		t.Fatalf("message = %#v", replies[1])
	}
	if replies[3].Kind != chatcap.ReplyFinish || replies[3].Content != "final" || replies[3].Sequence != 4 {
		t.Fatalf("finish = %#v", replies[3])
	}
	input, _ := state.ReadPath(starter.initial, "shared.request.input")
	messageID, _ := state.ReadPath(starter.initial, "shared.request.metadata.message_id")
	conversationID, _ := state.ReadPath(starter.initial, "shared.request.metadata.conversation_id")
	channel, _ := state.ReadPath(starter.initial, "shared.request.metadata.channel")
	if input != "hello" || messageID != "message-1" || conversationID != "conversation-1" || channel != "test" {
		t.Fatalf("chat state = input:%#v message:%#v conversation:%#v channel:%#v", input, messageID, conversationID, channel)
	}
	records, err := service.ListRecords(context.Background(), "chat", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != runtime.RunStatusCompleted {
		t.Fatalf("records = %#v", records)
	}
}

func TestChatLLMStreamObserverAccumulatesContentPerCall(t *testing.T) {
	var replies []chatcap.Reply
	observer := newChatLLMStreamObserver(chatcap.ReplySinkFunc(func(_ context.Context, reply chatcap.Reply) error {
		replies = append(replies, reply)
		return nil
	}))
	ctx := runtime.WithRunnerEventObserver(context.Background(), observer)

	for _, item := range []struct {
		typ    runtime.EventType
		callID string
		text   string
	}{
		{typ: runtime.EventLLMContentChunk, callID: "call-1", text: "hello"},
		{typ: runtime.EventLLMContentChunk, callID: "call-1", text: " "},
		{typ: runtime.EventLLMContentChunk, callID: "call-1", text: "world"},
		{typ: runtime.EventLLMContent, callID: "call-1", text: "hello world"},
		{typ: runtime.EventLLMContentChunk, callID: "call-2", text: "replacement"},
	} {
		if err := observeChatLLMEvent(ctx, item.typ, "step", "answer", item.callID, item.text); err != nil {
			t.Fatal(err)
		}
	}

	if len(replies) != 3 {
		t.Fatalf("replies = %#v", replies)
	}
	if replies[0].Content != "hello" || replies[1].Content != "hello world" || replies[2].Content != "replacement" {
		t.Fatalf("reply contents = [%q %q %q]", replies[0].Content, replies[1].Content, replies[2].Content)
	}
}

type lifecycleChannelFactory struct {
	started chan map[string]any
	stopped chan struct{}
}

func (factory *lifecycleChannelFactory) Definition() chatchannel.Definition {
	return chatchannel.Definition{
		ID:    "lifecycle",
		Title: "Lifecycle",
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":   map[string]any{"type": "string"},
				"secret": map[string]any{"type": "string", "writeOnly": true},
			},
			"required": []any{"name", "secret"},
		},
	}
}

func (factory *lifecycleChannelFactory) ValidateConfig(config map[string]any) error {
	if config["name"] == "" || config["secret"] == "" || config["secret"] == nil {
		return errors.New("name and secret are required")
	}
	return nil
}

func (factory *lifecycleChannelFactory) New(config chatchannel.InstanceConfig) (chatchannel.Instance, error) {
	return &lifecycleChannel{config: config.Config, started: factory.started, stopped: factory.stopped}, nil
}

type lifecycleChannel struct {
	config  map[string]any
	started chan map[string]any
	stopped chan struct{}
}

func (channel *lifecycleChannel) Run(ctx context.Context) error {
	channel.started <- channel.config
	<-ctx.Done()
	channel.stopped <- struct{}{}
	return nil
}

func TestServiceManagesRegisteredChatChannelLifecycleAndSecrets(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	factory := &lifecycleChannelFactory{started: make(chan map[string]any, 2), stopped: make(chan struct{}, 2)}
	channels := chatchannel.NewDefaultRegistry()
	if err := channels.Register(factory); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		store,
		RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) { return &recordingStarter{}, nil }),
		WithChatChannels(channels),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), Trigger{
		ID: "managed-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{
			Channel:       "lifecycle",
			ChannelConfig: map[string]any{"name": "first", "secret": "stored-secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if redacted := service.RedactChatChannelConfig(created); redacted.Chat.ChannelConfig["secret"] != nil {
		t.Fatalf("redacted chat config = %#v", redacted.Chat.ChannelConfig)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	select {
	case config := <-factory.started:
		if config["name"] != "first" || config["secret"] != "stored-secret" {
			t.Fatalf("started config = %#v", config)
		}
	case <-time.After(time.Second):
		t.Fatal("chat channel did not start")
	}

	updated, err := service.Update(context.Background(), Trigger{
		ID: "managed-chat", Type: TypeChat, Enabled: true, Target: Target{GraphID: "graph"},
		Chat: &ChatSpec{Channel: "lifecycle", ChannelConfig: map[string]any{"name": "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Chat.ChannelConfig["secret"] != "stored-secret" {
		t.Fatalf("updated chat config = %#v", updated.Chat.ChannelConfig)
	}
	select {
	case <-factory.stopped:
	case <-time.After(time.Second):
		t.Fatal("previous chat channel did not stop")
	}
	select {
	case config := <-factory.started:
		if config["name"] != "second" || config["secret"] != "stored-secret" {
			t.Fatalf("restarted config = %#v", config)
		}
	case <-time.After(time.Second):
		t.Fatal("updated chat channel did not start")
	}
	if err := service.Delete(context.Background(), "managed-chat"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-factory.stopped:
	case <-time.After(time.Second):
		t.Fatal("deleted chat channel did not stop")
	}
}

type recordingStarter struct {
	initial *state.State
	calls   int
}

func (r *recordingStarter) Start(_ context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	r.initial = initial.Clone()
	r.calls++
	return runtime.RunRecord{RunID: "run-1", Status: runtime.RunStatusCompleted}, initial, nil
}

type asyncRecordingStarter struct {
	recordingStarter
	done <-chan struct{}
}

func (r *asyncRecordingStarter) StartAsync(_ context.Context, initial *state.State) (runtime.RunRecord, <-chan struct{}, error) {
	r.initial = initial.Clone()
	r.calls++
	return runtime.RunRecord{RunID: "run-async", Status: runtime.RunStatusRunning}, r.done, nil
}

func TestServiceInvokeWebhookBuildsStateAndChecksAPIKey(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:      "webhook-1",
		Type:    TypeWebhook,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		InitialState: map[string]any{
			"shared": map[string]any{"tenant": map[string]any{"id": "tenant-1"}},
			"scopes": map[string]any{"agent": map[string]any{"mode": "review"}},
		},
		Webhook: &WebhookSpec{
			APIKey: "secret",
			StateMappings: []WebhookStateMapping{
				{Parameter: "message", StatePath: "shared.webhook.message"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"message":"hello"}`)
	run, err := service.InvokeWebhook(context.Background(), "webhook-1", body, "secret", map[string]string{
		"Authorization": "Bearer secret-token",
		"Cookie":        "session=secret",
		"X-Trace-ID":    "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" || starter.calls != 1 {
		t.Fatalf("run = %#v, calls = %d", run, starter.calls)
	}
	input, ok := state.ReadPath(starter.initial, "shared.request.input")
	if !ok {
		t.Fatal("webhook input is missing")
	}
	values, ok := input.(map[string]any)
	if !ok || values["message"] != "hello" {
		t.Fatalf("webhook input = %#v", input)
	}
	mapped, ok := state.ReadPath(starter.initial, "shared.webhook.message")
	if !ok || mapped != "hello" {
		t.Fatalf("mapped webhook input = %#v", mapped)
	}
	tenantID, ok := state.ReadPath(starter.initial, "shared.tenant.id")
	if !ok || tenantID != "tenant-1" {
		t.Fatalf("trigger initial shared state = %#v", tenantID)
	}
	agentMode, ok := state.ReadPath(starter.initial, "scopes.agent.mode")
	if !ok || agentMode != "review" {
		t.Fatalf("trigger initial scoped state = %#v", agentMode)
	}
	triggerID, ok := state.ReadPath(starter.initial, "shared.trigger.id")
	if !ok || triggerID != "webhook-1" {
		t.Fatalf("trigger id = %#v", triggerID)
	}
	metadataValue, ok := state.ReadPath(starter.initial, "shared.request.metadata")
	if !ok {
		t.Fatal("webhook metadata is missing")
	}
	metadata, ok := metadataValue.(map[string]any)
	if !ok || metadata["X-Trace-ID"] != "trace-1" {
		t.Fatalf("webhook metadata = %#v", metadataValue)
	}
	for _, sensitive := range []string{"Authorization", "Cookie"} {
		if _, exists := metadata[sensitive]; exists {
			t.Fatalf("sensitive header %q leaked into metadata: %#v", sensitive, metadata)
		}
	}
}

func TestTriggerRejectsInvalidInitialState(t *testing.T) {
	tests := []map[string]any{
		{"runtime": map[string]any{"run_id": "spoofed"}},
		{"shared": "not-an-object"},
		{"shared": map[string]any{"request": map[string]any{"input": "spoofed"}}},
		{"shared": map[string]any{"trigger": map[string]any{"id": "spoofed"}}},
	}
	for _, initialState := range tests {
		item := Trigger{
			ID:           "webhook",
			Type:         TypeWebhook,
			Concurrency:  ConcurrencyParallel,
			Target:       Target{GraphID: "graph-1"},
			InitialState: initialState,
			Webhook:      &WebhookSpec{},
		}
		if err := item.Validate(); err == nil {
			t.Fatalf("initial state %#v was accepted", initialState)
		} else if !errors.Is(err, ErrInvalidTrigger) {
			t.Fatalf("initial state error = %v", err)
		}
	}
}

func TestTriggerRejectsInvalidWebhookStateMappings(t *testing.T) {
	tests := [][]WebhookStateMapping{
		{{Parameter: "", StatePath: "shared.input"}},
		{{Parameter: "input", StatePath: "runtime.input"}},
		{{Parameter: "input", StatePath: "shared.trigger.id"}},
		{{Parameter: "input", StatePath: "shared.request"}},
		{{Parameter: "input", StatePath: "shared.request.metadata.source"}},
		{
			{Parameter: "first", StatePath: "shared.input"},
			{Parameter: "second", StatePath: "shared.input"},
		},
	}
	for _, mappings := range tests {
		item := Trigger{
			ID:          "webhook",
			Type:        TypeWebhook,
			Concurrency: ConcurrencyParallel,
			Target:      Target{GraphID: "graph-1"},
			Webhook:     &WebhookSpec{StateMappings: mappings},
		}
		if err := item.Validate(); err == nil {
			t.Fatalf("mappings %#v were accepted", mappings)
		} else if !errors.Is(err, ErrInvalidStateMapping) {
			t.Fatalf("mapping error = %v", err)
		}
	}
}

func TestServiceRejectsInvalidWebhookAPIKeyAndPayload(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:      "webhook-1",
		Type:    TypeWebhook,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		Webhook: &WebhookSpec{APIKey: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-1", []byte(`{}`), "wrong", nil); err != ErrInvalidAPIKey {
		t.Fatalf("invalid api_key error = %v", err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-1", []byte(`not-json`), "secret", nil); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("invalid payload error = %v", err)
	}
}

func TestServiceUsesLegacyWebhookSecretAsAPIKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "legacy.json"),
		[]byte(`{"id":"legacy","type":"webhook","enabled":true,"target":{"graph_id":"graph-1"},"webhook":{"secret":"legacy-key","signature_header":"X-Legacy-Signature"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "legacy", []byte(`{}`), "legacy-key", nil); err != nil {
		t.Fatal(err)
	}
	if starter.calls != 1 {
		t.Fatalf("legacy webhook calls = %d, want 1", starter.calls)
	}
}

func TestServiceInvokeScheduleAndValidatesCron(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:       "bad-schedule",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
		Schedule: &ScheduleSpec{Cron: "not-a-cron"},
	}); err == nil {
		t.Fatal("invalid cron was accepted")
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:      "schedule-1",
		Type:    TypeSchedule,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		InitialState: map[string]any{
			"shared": map[string]any{"schedule": map[string]any{"attempt": float64(2)}},
		},
		Schedule: &ScheduleSpec{Cron: "0 12 * * *", Input: map[string]any{"source": "timer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.InvokeSchedule(context.Background(), "schedule-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("run id = %q", run.RunID)
	}
	input, ok := state.ReadPath(starter.initial, "shared.request.input")
	if !ok || input.(map[string]any)["source"] != "timer" {
		t.Fatalf("schedule input = %#v", input)
	}
	attempt, ok := state.ReadPath(starter.initial, "shared.schedule.attempt")
	if !ok || attempt != float64(2) {
		t.Fatalf("schedule initial state = %#v", attempt)
	}
	if _, err := service.Get(context.Background(), "schedule-1"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceAsyncRunKeepsSkipConcurrencyActiveUntilCompletion(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	starter := &asyncRecordingStarter{done: done}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), Trigger{
		ID:          "webhook-async",
		Type:        TypeWebhook,
		Enabled:     true,
		Target:      Target{GraphID: "graph-1"},
		Concurrency: ConcurrencySkip,
		Webhook:     &WebhookSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != runtime.RunStatusRunning {
		t.Fatalf("run status = %q, want running", run.Status)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), "", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second invocation error = %v, want ErrBusy", err)
	}

	close(done)
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.Lock()
		active := service.activeRuns["webhook-async"]
		service.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("async trigger remained active after completion")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), "", nil); err != nil {
		t.Fatalf("invocation after completion error = %v", err)
	}
}

func TestTriggerRequiresGraphIDTarget(t *testing.T) {
	item := Trigger{
		ID:          "webhook",
		Type:        TypeWebhook,
		Concurrency: ConcurrencyParallel,
		Webhook:     &WebhookSpec{},
	}
	if err := item.Validate(); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("Validate() error = %v, want ErrInvalidTarget", err)
	}
}

type failingStore struct {
	Store
	listErr   error
	deleteErr error
}

func (s *failingStore) List(ctx context.Context) ([]Trigger, error) {
	if s.listErr != nil {
		err := s.listErr
		s.listErr = nil
		return nil, err
	}
	return s.Store.List(ctx)
}

func (s *failingStore) Delete(ctx context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Store.Delete(ctx, id)
}

func TestServiceStartCanRetryAfterStoreFailure(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &failingStore{Store: fileStore, listErr: errors.New("temporary list failure")}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:       "schedule-1",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
		Schedule: &ScheduleSpec{Cron: "0 12 * * *"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.Start(context.Background()); err == nil {
		t.Fatal("first Start() unexpectedly succeeded")
	}
	service.mu.Lock()
	startedAfterFailure := service.cancel != nil
	schedulesAfterFailure := len(service.schedules)
	service.mu.Unlock()
	if startedAfterFailure || schedulesAfterFailure != 0 {
		t.Fatalf("failed Start() left started=%v schedules=%d", startedAfterFailure, schedulesAfterFailure)
	}

	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("retry Start() error = %v", err)
	}
	service.mu.Lock()
	startedAfterRetry := service.cancel != nil
	schedulesAfterRetry := len(service.schedules)
	service.mu.Unlock()
	if !startedAfterRetry || schedulesAfterRetry != 1 {
		t.Fatalf("retried Start() left started=%v schedules=%d", startedAfterRetry, schedulesAfterRetry)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceDeleteFailureKeepsPersistedScheduleActive(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &failingStore{Store: fileStore}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return &recordingStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), Trigger{
		ID:       "schedule-1",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
		Schedule: &ScheduleSpec{Cron: "0 12 * * *"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()

	store.deleteErr = errors.New("temporary delete failure")
	if err := service.Delete(context.Background(), "schedule-1"); err == nil {
		t.Fatal("Delete() unexpectedly succeeded")
	}
	service.mu.Lock()
	scheduleCount := len(service.schedules)
	service.mu.Unlock()
	if scheduleCount != 1 {
		t.Fatalf("failed Delete() left %d schedules, want 1", scheduleCount)
	}
	if _, err := fileStore.Get(context.Background(), "schedule-1"); err != nil {
		t.Fatalf("failed Delete() removed persisted trigger: %v", err)
	}
}

func TestServiceRecordsStartedAndFailedInvocations(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &recordingStarter{}
	service, err := NewService(store, RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	if _, err := service.Create(context.Background(), Trigger{
		ID:      "webhook-recorded",
		Type:    TypeWebhook,
		Enabled: true,
		Target:  Target{GraphID: "graph-1"},
		Webhook: &WebhookSpec{},
	}); err != nil {
		t.Fatal(err)
	}

	run, err := service.InvokeWebhook(context.Background(), "webhook-recorded", []byte(`{}`), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("run id = %q", run.RunID)
	}

	clock = clock.Add(time.Minute)
	service.resolver = RunnerResolverFunc(func(context.Context, Target) (RunStarter, error) {
		return nil, errors.New("graph unavailable")
	})
	if _, err := service.InvokeWebhook(context.Background(), "webhook-recorded", []byte(`{}`), "", nil); err == nil {
		t.Fatal("failed invocation unexpectedly succeeded")
	}

	records, err := service.ListRecords(context.Background(), "webhook-recorded", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if records[0].Status != runtime.RunStatusFailed || records[0].ErrorMessage != "graph unavailable" {
		t.Fatalf("failed record = %#v", records[0])
	}
	if records[1].Status != runtime.RunStatusCompleted || records[1].Run == nil || records[1].Run.RunID != "run-1" {
		t.Fatalf("started record = %#v", records[1])
	}
}
