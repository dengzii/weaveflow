package trigger

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	store    Store
	resolver RunnerResolver
	now      func() time.Time

	operationMu sync.Mutex
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	schedules   map[string]*scheduleEntry
	activeRuns  map[string]int
}

type scheduleEntry struct {
	cron *cron.Cron
	id   cron.EntryID
}

func NewService(store Store, resolver RunnerResolver) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("trigger store is required")
	}
	if resolver == nil {
		return nil, fmt.Errorf("runner resolver is required")
	}
	return &Service{
		store:      store,
		resolver:   resolver,
		now:        time.Now,
		schedules:  make(map[string]*scheduleEntry),
		activeRuns: make(map[string]int),
	}, nil
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
	if err := s.store.Create(ctx, trigger); err != nil {
		return Trigger{}, err
	}
	s.replaceSchedule(trigger.ID, schedule)
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
	if err := s.store.Update(ctx, trigger); err != nil {
		return Trigger{}, err
	}
	s.replaceSchedule(trigger.ID, schedule)
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
	}

	s.mu.Lock()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.schedules = schedules
	for _, schedule := range schedules {
		schedule.cron.Start()
	}
	s.mu.Unlock()
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
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, entry := range entries {
		entry.cron.Stop()
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
	initial := state.FromMap(trigger.InitialState)
	if err := state.SetPath(initial, "shared.request", map[string]any{
		"input":    input,
		"metadata": metadata,
	}); err != nil {
		return runtime.RunRecord{}, fmt.Errorf("initialize trigger request state: %w", err)
	}
	if err := state.SetPath(initial, "shared.trigger", map[string]any{
		"id":   trigger.ID,
		"type": triggerType,
	}); err != nil {
		return runtime.RunRecord{}, fmt.Errorf("initialize trigger identity state: %w", err)
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
