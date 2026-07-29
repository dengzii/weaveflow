package trigger

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
)

type Type string

const (
	TypeWebhook  Type = "webhook"
	TypeSchedule Type = "schedule"
)

type ConcurrencyPolicy string

const (
	ConcurrencyParallel ConcurrencyPolicy = "parallel"
	ConcurrencySkip     ConcurrencyPolicy = "skip"
)

type Target struct {
	GraphID string `json:"graph_id"`
}

type WebhookSpec struct {
	APIKey          string                `json:"api_key,omitempty"`
	Secret          string                `json:"secret,omitempty"`
	SignatureHeader string                `json:"signature_header,omitempty"`
	StateMappings   []WebhookStateMapping `json:"state_mappings,omitempty"`
}

type WebhookStateMapping struct {
	Parameter string `json:"parameter"`
	StatePath string `json:"state_path"`
}

type ScheduleSpec struct {
	Cron     string         `json:"cron"`
	Timezone string         `json:"timezone,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
}

type Trigger struct {
	ID           string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Type         Type              `json:"type"`
	Enabled      bool              `json:"enabled"`
	Target       Target            `json:"target"`
	Concurrency  ConcurrencyPolicy `json:"concurrency,omitempty"`
	InitialState map[string]any    `json:"initial_state,omitempty"`
	Webhook      *WebhookSpec      `json:"webhook,omitempty"`
	Schedule     *ScheduleSpec     `json:"schedule,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type TriggerResult struct {
	Trigger Trigger           `json:"trigger"`
	Run     runtime.RunRecord `json:"run"`
}

type Record struct {
	ID           string             `json:"id"`
	TriggerID    string             `json:"trigger_id"`
	TriggerType  Type               `json:"trigger_type"`
	Target       Target             `json:"target"`
	Status       runtime.RunStatus  `json:"status"`
	Run          *runtime.RunRecord `json:"run,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty"`
	TriggeredAt  time.Time          `json:"triggered_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

func (t Trigger) Normalize(now time.Time) Trigger {
	t.ID = strings.TrimSpace(t.ID)
	t.Name = strings.TrimSpace(t.Name)
	t.Type = Type(strings.TrimSpace(string(t.Type)))
	t.Concurrency = ConcurrencyPolicy(strings.TrimSpace(string(t.Concurrency)))
	t.Target.GraphID = strings.TrimSpace(t.Target.GraphID)
	if t.Concurrency == "" {
		t.Concurrency = ConcurrencyParallel
	}
	if t.Webhook != nil {
		if t.Webhook.APIKey == "" {
			t.Webhook.APIKey = t.Webhook.Secret
		}
		t.Webhook.Secret = ""
		t.Webhook.SignatureHeader = ""
		for i := range t.Webhook.StateMappings {
			t.Webhook.StateMappings[i].Parameter = strings.TrimSpace(t.Webhook.StateMappings[i].Parameter)
			t.Webhook.StateMappings[i].StatePath = strings.TrimSpace(t.Webhook.StateMappings[i].StatePath)
			if path, err := state.ParsePath(t.Webhook.StateMappings[i].StatePath); err == nil {
				t.Webhook.StateMappings[i].StatePath = path.String()
			}
		}
	}
	if t.Schedule != nil {
		t.Schedule.Cron = strings.TrimSpace(t.Schedule.Cron)
		t.Schedule.Timezone = strings.TrimSpace(t.Schedule.Timezone)
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = now
	}
	return t
}

func (t Trigger) Validate() error {
	if err := validateTriggerID(t.ID); err != nil {
		return err
	}
	if t.Type != TypeWebhook && t.Type != TypeSchedule {
		return fmt.Errorf("%w: trigger type %q is invalid", ErrInvalidTrigger, t.Type)
	}
	if t.Concurrency != ConcurrencyParallel && t.Concurrency != ConcurrencySkip {
		return fmt.Errorf("%w: trigger concurrency %q is invalid", ErrInvalidTrigger, t.Concurrency)
	}
	if t.Target.GraphID == "" {
		return fmt.Errorf("%w: %w: graph_id is required", ErrInvalidTrigger, ErrInvalidTarget)
	}
	if err := validateInitialState(t.InitialState); err != nil {
		return fmt.Errorf("%w: initial_state: %v", ErrInvalidTrigger, err)
	}
	switch t.Type {
	case TypeWebhook:
		if t.Webhook == nil {
			return fmt.Errorf("%w: webhook spec is required", ErrInvalidTrigger)
		}
		if err := validateWebhookStateMappings(t.Webhook.StateMappings); err != nil {
			return fmt.Errorf("%w: %w: %v", ErrInvalidTrigger, ErrInvalidStateMapping, err)
		}
	case TypeSchedule:
		if t.Schedule == nil || t.Schedule.Cron == "" {
			return fmt.Errorf("%w: schedule cron is required", ErrInvalidTrigger)
		}
		if t.Schedule.Timezone != "" {
			if _, err := time.LoadLocation(t.Schedule.Timezone); err != nil {
				return fmt.Errorf("%w: schedule timezone %q is invalid: %v", ErrInvalidTrigger, t.Schedule.Timezone, err)
			}
		}
	}
	return nil
}

func validateInitialState(initial map[string]any) error {
	for section, value := range initial {
		if section != state.SectionShared && section != state.SectionScopes {
			return fmt.Errorf("state section %q is not allowed", section)
		}
		values, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("state section %q must be an object", section)
		}
		if section == state.SectionShared {
			for _, reserved := range []string{"request", "trigger"} {
				if _, exists := values[reserved]; exists {
					return fmt.Errorf("state path %q is reserved", section+"."+reserved)
				}
			}
		}
	}
	if _, err := json.Marshal(initial); err != nil {
		return fmt.Errorf("must contain JSON-compatible values: %w", err)
	}
	return nil
}

func validateTriggerID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: trigger id is required", ErrInvalidTrigger)
	}
	if len(id) > 128 {
		return fmt.Errorf("%w: trigger id is too long", ErrInvalidTrigger)
	}
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("%w: trigger id %q contains an invalid character", ErrInvalidTrigger, id)
	}
	if strings.HasPrefix(id, ".") || isReservedTriggerID(id) {
		return fmt.Errorf("%w: trigger id %q is reserved", ErrInvalidTrigger, id)
	}
	return nil
}

func isReservedTriggerID(id string) bool {
	base := strings.ToUpper(strings.SplitN(id, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func validateWebhookStateMappings(mappings []WebhookStateMapping) error {
	targets := make(map[string]struct{}, len(mappings))
	for index, mapping := range mappings {
		if err := validateWebhookParameter(mapping.Parameter); err != nil {
			return fmt.Errorf("webhook state mapping %d: %w", index+1, err)
		}
		path, err := state.ParsePath(mapping.StatePath)
		if err != nil {
			return fmt.Errorf("webhook state mapping %d: invalid state path: %w", index+1, err)
		}
		segments := path.Segments()
		if len(segments) == 0 {
			return fmt.Errorf("webhook state mapping %d: state path must include a field", index+1)
		}
		if path.Section() != state.SectionShared && path.Section() != state.SectionScopes {
			return fmt.Errorf("webhook state mapping %d: state section %q is reserved", index+1, path.Section())
		}
		if path.Section() == state.SectionShared && (segments[0] == "trigger" ||
			(segments[0] == "request" && (len(segments) == 1 || segments[1] == "metadata"))) {
			return fmt.Errorf("webhook state mapping %d: state path %q is reserved", index+1, path.String())
		}
		if _, exists := targets[path.String()]; exists {
			return fmt.Errorf("webhook state mapping %d: duplicate state path %q", index+1, path.String())
		}
		targets[path.String()] = struct{}{}
	}
	return nil
}

func validateWebhookParameter(parameter string) error {
	if parameter == "$" {
		return nil
	}
	if parameter == "" {
		return fmt.Errorf("parameter is required")
	}
	for _, segment := range strings.Split(parameter, ".") {
		if segment == "" || segment != strings.TrimSpace(segment) {
			return fmt.Errorf("parameter path %q is invalid", parameter)
		}
	}
	return nil
}
