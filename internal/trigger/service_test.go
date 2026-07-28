package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

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

func TestServiceInvokeWebhookBuildsStateAndChecksSignature(t *testing.T) {
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
		Webhook: &WebhookSpec{
			Secret: "secret",
			StateMappings: []WebhookStateMapping{
				{Parameter: "message", StatePath: "shared.webhook.message"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"message":"hello"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	run, err := service.InvokeWebhook(context.Background(), "webhook-1", body, map[string]string{
		DefaultSignatureHeader: "sha256=" + hex.EncodeToString(mac.Sum(nil)),
		"Authorization":        "Bearer secret-token",
		"Cookie":               "session=secret",
		"X-Trace-ID":           "trace-1",
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
	for _, sensitive := range []string{DefaultSignatureHeader, "Authorization", "Cookie"} {
		if _, exists := metadata[sensitive]; exists {
			t.Fatalf("sensitive header %q leaked into metadata: %#v", sensitive, metadata)
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

func TestServiceRejectsInvalidWebhookSignatureAndPayload(t *testing.T) {
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
		Webhook: &WebhookSpec{Secret: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-1", []byte(`{}`), map[string]string{}); err != ErrInvalidSignature {
		t.Fatalf("invalid signature error = %v", err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-1", []byte(`not-json`), map[string]string{
		DefaultSignatureHeader: "sha256=" + signBody("secret", []byte(`not-json`)),
	}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("invalid payload error = %v", err)
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
		ID:       "schedule-1",
		Type:     TypeSchedule,
		Enabled:  true,
		Target:   Target{GraphID: "graph-1"},
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

	run, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != runtime.RunStatusRunning {
		t.Fatalf("run status = %q, want running", run.Status)
	}
	if _, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), nil); !errors.Is(err, ErrBusy) {
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
	if _, err := service.InvokeWebhook(context.Background(), "webhook-async", []byte(`{}`), nil); err != nil {
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

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
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

	run, err := service.InvokeWebhook(context.Background(), "webhook-recorded", []byte(`{}`), nil)
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
	if _, err := service.InvokeWebhook(context.Background(), "webhook-recorded", []byte(`{}`), nil); err == nil {
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
