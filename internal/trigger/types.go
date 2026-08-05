package trigger

import (
	"bytes"
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
	TypeChat     Type = "chat"
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
	APIKey        string                `json:"api_key,omitempty"`
	StateMappings []WebhookStateMapping `json:"state_mappings,omitempty"`
}

func (s *WebhookSpec) UnmarshalJSON(data []byte) error {
	type webhookSpec WebhookSpec
	var decoded webhookSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*s = WebhookSpec(decoded)
	return nil
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

type ChatSpec struct {
	Channel       string             `json:"channel"`
	ChannelConfig map[string]any     `json:"channel_config,omitempty"`
	StreamUpdates bool               `json:"stream_updates"`
	StreamNodeIDs []string           `json:"stream_node_ids,omitempty"`
	HistoryLimit  int                `json:"history_limit,omitempty"`
	StateBindings *ChatStateBindings `json:"state_bindings,omitempty"`
}

type ChatStateBindings struct {
	Conversation   string `json:"conversation,omitempty"`
	RawHistory     string `json:"raw_history,omitempty"`
	TriggerID      string `json:"trigger_id,omitempty"`
	Channel        string `json:"channel,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
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
	Chat         *ChatSpec         `json:"chat,omitempty"`
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
	if t.Chat != nil {
		t.Chat.Channel = strings.TrimSpace(t.Chat.Channel)
		if t.Chat.Channel == "" {
			t.Chat.Channel = "http"
		}
		seen := make(map[string]struct{}, len(t.Chat.StreamNodeIDs))
		nodeIDs := make([]string, 0, len(t.Chat.StreamNodeIDs))
		for _, nodeID := range t.Chat.StreamNodeIDs {
			nodeID = strings.TrimSpace(nodeID)
			if nodeID == "" {
				continue
			}
			if _, ok := seen[nodeID]; ok {
				continue
			}
			seen[nodeID] = struct{}{}
			nodeIDs = append(nodeIDs, nodeID)
		}
		t.Chat.StreamNodeIDs = nodeIDs
		t.Chat.StateBindings = normalizeChatStateBindings(t.Chat.StateBindings)
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
	if t.Type != TypeWebhook && t.Type != TypeSchedule && t.Type != TypeChat {
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
	case TypeChat:
		if t.Chat == nil {
			return fmt.Errorf("%w: chat spec is required", ErrInvalidTrigger)
		}
		if t.Chat.Channel == "" {
			return fmt.Errorf("%w: chat channel is required", ErrInvalidTrigger)
		}
		if _, err := json.Marshal(t.Chat.ChannelConfig); err != nil {
			return fmt.Errorf("%w: chat channel_config must contain JSON-compatible values: %v", ErrInvalidTrigger, err)
		}
		if t.Chat.HistoryLimit < 0 || t.Chat.HistoryLimit > MaxRecordLimit {
			return fmt.Errorf("%w: chat history_limit must be between 0 and %d", ErrInvalidTrigger, MaxRecordLimit)
		}
		if err := validateChatStateBindings(t.Chat.StateBindings); err != nil {
			return fmt.Errorf("%w: chat state_bindings: %v", ErrInvalidTrigger, err)
		}
	}
	return nil
}

func normalizeChatStateBindings(bindings *ChatStateBindings) *ChatStateBindings {
	if bindings == nil {
		return nil
	}
	normalized := *bindings
	paths := []*string{
		&normalized.Conversation,
		&normalized.RawHistory,
		&normalized.TriggerID,
		&normalized.Channel,
		&normalized.UserID,
		&normalized.ConversationID,
		&normalized.MessageID,
	}
	for _, value := range paths {
		*value = strings.TrimSpace(*value)
		if path, err := state.ParsePath(*value); err == nil {
			*value = path.String()
		}
	}
	if normalized == (ChatStateBindings{}) {
		return nil
	}
	return &normalized
}

func validateChatStateBindings(bindings *ChatStateBindings) error {
	if bindings == nil {
		return nil
	}
	values := []struct {
		name          string
		path          string
		effectivePath string
	}{
		{name: "conversation", path: bindings.Conversation, effectivePath: chatConversationMessagesPath(bindings.Conversation)},
		{name: "raw_history", path: bindings.RawHistory},
		{name: "trigger_id", path: bindings.TriggerID},
		{name: "channel", path: bindings.Channel},
		{name: "user_id", path: bindings.UserID},
		{name: "conversation_id", path: bindings.ConversationID},
		{name: "message_id", path: bindings.MessageID},
	}
	validated := make([]struct {
		name string
		path string
	}, 0, len(values))
	for _, value := range values {
		if value.path == "" {
			continue
		}
		path, err := state.ParsePath(value.path)
		if err != nil {
			return fmt.Errorf("%s path is invalid: %w", value.name, err)
		}
		if len(path.Segments()) == 0 {
			return fmt.Errorf("%s path must include a field", value.name)
		}
		if path.Section() != state.SectionShared && path.Section() != state.SectionScopes {
			return fmt.Errorf("%s path section %q is not allowed", value.name, path.Section())
		}
		canonical := path.String()
		effectivePath := canonical
		if value.effectivePath != "" {
			effectivePath = value.effectivePath
		}
		if statePathsOverlap(effectivePath, "shared.request.input") {
			return fmt.Errorf("%s path %q overlaps the chat input path", value.name, canonical)
		}
		for _, previous := range validated {
			if statePathsOverlap(effectivePath, previous.path) {
				return fmt.Errorf("%s path %q overlaps %s path %q", value.name, canonical, previous.name, previous.path)
			}
		}
		validated = append(validated, struct {
			name string
			path string
		}{name: value.name, path: effectivePath})
	}
	return nil
}

func chatConversationMessagesPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if path, err := state.ParsePath(root); err == nil {
		root = path.String()
	}
	return root + ".messages"
}

func statePathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
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
