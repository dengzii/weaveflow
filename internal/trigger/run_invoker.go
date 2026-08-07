package trigger

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/google/uuid"
)

type RunStarter interface {
	Start(context.Context, *state.State) (runtime.RunRecord, *state.State, error)
}

type InitialStateValidator interface {
	ValidateInitialState(*state.State) error
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
	}, string(body))
}

func (s *Service) InvokeWebhookInput(ctx context.Context, id string, input any, apiKey string, headers map[string]string) (runtime.RunRecord, error) {
	return s.invokeWebhook(ctx, id, apiKey, headers, func() (any, error) {
		return input, nil
	}, "")
}

func (s *Service) invokeWebhook(ctx context.Context, id string, apiKey string, headers map[string]string, input func() (any, error), rawBody string) (runtime.RunRecord, error) {
	trigger, err := s.triggerStore.Get(ctx, id)
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
	metadata := webhookMetadata(headers)
	return s.invoke(ctx, trigger, payload, metadata, "webhook", rawBody)
}

func (s *Service) InvokeSchedule(ctx context.Context, id string) (runtime.RunRecord, error) {
	trigger, err := s.triggerStore.Get(ctx, id)
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
	return s.invoke(ctx, trigger, input, map[string]any{"scheduled_at": s.now().UTC().Format(time.RFC3339Nano)}, "schedule", "")
}

func buildTriggerState(item Trigger, input any, metadata map[string]any, triggerType string, rawBody string) (*state.State, error) {
	initial := state.FromMap(item.InitialState)
	values := make([]triggerStateBindingValue, 0, 4)
	switch item.Type {
	case TypeWebhook:
		if item.Webhook != nil && item.Webhook.StateBindings != nil {
			bindings := item.Webhook.StateBindings
			values = requestTriggerStateBindingValues(bindings.Input, bindings.Metadata, bindings.TriggerID, bindings.TriggerType, bindings.RawBody, input, metadata, item.ID, triggerType, rawBody)
		}
	case TypeSchedule:
		if item.Schedule != nil && item.Schedule.StateBindings != nil {
			bindings := item.Schedule.StateBindings
			values = requestTriggerStateBindingValues(bindings.Input, bindings.Metadata, bindings.TriggerID, bindings.TriggerType, "", input, metadata, item.ID, triggerType, "")
		}
	}
	for _, binding := range values {
		if binding.path == "" {
			continue
		}
		if err := state.SetPath(initial, binding.path, binding.value); err != nil {
			return nil, fmt.Errorf("initialize trigger %s state at %q: %w", binding.name, binding.path, err)
		}
	}
	return initial, nil
}

type triggerStateBindingValue struct {
	name  string
	path  string
	value any
}

func requestTriggerStateBindingValues(
	inputPath string,
	metadataPath string,
	triggerIDPath string,
	triggerTypePath string,
	rawBodyPath string,
	input any,
	metadata map[string]any,
	triggerID string,
	triggerType string,
	rawBody string,
) []triggerStateBindingValue {
	return []triggerStateBindingValue{
		{name: "input", path: inputPath, value: input},
		{name: "metadata", path: metadataPath, value: metadata},
		{name: "trigger_id", path: triggerIDPath, value: triggerID},
		{name: "trigger_type", path: triggerTypePath, value: triggerType},
		{name: "raw_body", path: rawBodyPath, value: rawBody},
	}
}

func (s *Service) invoke(ctx context.Context, item Trigger, input any, metadata map[string]any, triggerType string, rawBody string) (runtime.RunRecord, error) {
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
	if err := s.invocationStore.CreateRecord(ctx, record); err != nil {
		return runtime.RunRecord{}, fmt.Errorf("create trigger record: %w", err)
	}

	run, runErr := s.invokeRun(ctx, item, input, metadata, triggerType, rawBody)
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
	_ = s.invocationStore.UpdateRecord(context.WithoutCancel(ctx), record)
	return run, runErr
}

func (s *Service) invokeRun(ctx context.Context, trigger Trigger, input any, metadata map[string]any, triggerType string, rawBody string) (runtime.RunRecord, error) {
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
	initial, err := buildTriggerState(trigger, input, metadata, triggerType, rawBody)
	if err != nil {
		return runtime.RunRecord{}, err
	}
	if trigger.Type == TypeWebhook && trigger.Webhook != nil {
		if err := applyWebhookStateMappings(initial, input, trigger.Webhook.StateMappings); err != nil {
			return runtime.RunRecord{}, err
		}
	}
	if err := validateRunnerInitialState(runner, initial); err != nil {
		return runtime.RunRecord{}, err
	}
	executionCtx := s.executionContext(ctx)
	executionCtx = runtime.WithRunOrigin(executionCtx, runtime.RunOrigin{
		Type:      triggerType,
		TriggerID: trigger.ID,
	})
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

func validateRunnerInitialState(runner RunStarter, initial *state.State) error {
	validator, ok := runner.(InitialStateValidator)
	if !ok {
		return nil
	}
	if err := validator.ValidateInitialState(initial); err != nil {
		return fmt.Errorf("validate trigger initial state: %w", err)
	}
	return nil
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

func verifyWebhookAPIKey(spec *WebhookSpec, provided string) error {
	if spec == nil {
		return nil
	}
	expected := spec.APIKey
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

func webhookMetadata(headers map[string]string) map[string]any {
	metadata := make(map[string]any, len(headers))
	for key, value := range headers {
		switch {
		case strings.EqualFold(key, "Authorization"),
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
