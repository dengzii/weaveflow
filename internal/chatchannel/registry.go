package chatchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/dsl"
)

const HTTPChannelID = "http"

var (
	ErrChannelNotFound   = errors.New("chat channel is not registered")
	ErrInvalidConfig     = errors.New("invalid chat channel config")
	ErrSetupUnavailable  = errors.New("chat channel setup is unavailable")
	ErrInvalidSetupInput = errors.New("invalid chat channel setup input")
)

const SetupKindQRCode = "qr_code"

type SetupDefinition struct {
	Kind string `json:"kind"`
}

type Definition struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description,omitempty"`
	ConfigSchema map[string]any   `json:"config_schema"`
	Setup        *SetupDefinition `json:"setup,omitempty"`
}

type Handler interface {
	Handle(context.Context, chatcap.InboundMessage, chatcap.ReplySink) error
}

type HandlerFunc func(context.Context, chatcap.InboundMessage, chatcap.ReplySink) error

func (f HandlerFunc) Handle(ctx context.Context, message chatcap.InboundMessage, sink chatcap.ReplySink) error {
	return f(ctx, message, sink)
}

type Instance interface {
	Run(context.Context) error
}

type InstanceConfig struct {
	TriggerID string
	Config    map[string]any
	Handler   Handler
}

type Factory interface {
	Definition() Definition
	ValidateConfig(map[string]any) error
	New(InstanceConfig) (Instance, error)
}

type SetupStatus string

const (
	SetupStatusWaiting              SetupStatus = "waiting"
	SetupStatusScanned              SetupStatus = "scanned"
	SetupStatusVerificationRequired SetupStatus = "verification_required"
	SetupStatusConfirmed            SetupStatus = "confirmed"
	SetupStatusExpired              SetupStatus = "expired"
	SetupStatusFailed               SetupStatus = "failed"
)

type SetupStartConfig struct {
	ExistingConfig map[string]any
}

type SetupPollInput struct {
	VerificationCode string
}

type SetupAccount struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}

type SetupResult struct {
	Status           SetupStatus
	QRCodeContent    string
	ExpiresAt        time.Time
	Account          *SetupAccount
	Message          string
	CredentialConfig map[string]any
}

type SetupSession interface {
	Poll(context.Context, SetupPollInput) (SetupResult, error)
}

type SetupFactory interface {
	StartSetup(context.Context, SetupStartConfig) (SetupSession, SetupResult, error)
}

type CredentialIdentifier interface {
	CredentialID(map[string]any) string
}

type SecretResolver interface {
	Resolve(context.Context, dsl.SecretRef) (string, error)
}

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func NewDefaultRegistry() *Registry {
	registry := NewRegistry()
	if err := registry.Register(httpFactory{}); err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Register(factory Factory) error {
	if r == nil {
		return errors.New("chat channel registry is nil")
	}
	if factory == nil {
		return errors.New("chat channel factory is nil")
	}
	definition := normalizeDefinition(factory.Definition())
	if definition.ID == "" {
		return errors.New("chat channel ID is required")
	}
	if definition.Title == "" {
		return fmt.Errorf("chat channel %q title is required", definition.ID)
	}
	if definition.ConfigSchema == nil {
		return fmt.Errorf("chat channel %q config schema is required", definition.ID)
	}
	if definition.Setup != nil {
		if definition.Setup.Kind == "" {
			return fmt.Errorf("chat channel %q setup kind is required", definition.ID)
		}
		if _, ok := factory.(SetupFactory); !ok {
			return fmt.Errorf("chat channel %q declares setup but does not implement it", definition.ID)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[definition.ID]; exists {
		return fmt.Errorf("chat channel %q is already registered", definition.ID)
	}
	r.factories[definition.ID] = factory
	return nil
}

func (r *Registry) Definition(id string) (Definition, bool) {
	factory, ok := r.factory(id)
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(normalizeDefinition(factory.Definition())), true
}

func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	ids := make([]string, 0, len(r.factories))
	for id := range r.factories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	factories := make([]Factory, 0, len(ids))
	for _, id := range ids {
		factories = append(factories, r.factories[id])
	}
	r.mu.RUnlock()
	definitions := make([]Definition, 0, len(factories))
	for _, factory := range factories {
		definitions = append(definitions, cloneDefinition(normalizeDefinition(factory.Definition())))
	}
	return definitions
}

func (r *Registry) ValidateConfig(id string, config map[string]any) error {
	factory, ok := r.factory(id)
	if !ok {
		return fmt.Errorf("%w: %q", ErrChannelNotFound, strings.TrimSpace(id))
	}
	if err := validateJSONConfig(config); err != nil {
		return err
	}
	if err := factory.ValidateConfig(cloneConfig(config)); err != nil {
		return fmt.Errorf("%w: channel %q: %v", ErrInvalidConfig, strings.TrimSpace(id), err)
	}
	return nil
}

func (r *Registry) NewInstance(id string, config InstanceConfig) (Instance, error) {
	factory, ok := r.factory(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrChannelNotFound, strings.TrimSpace(id))
	}
	if config.Handler == nil {
		return nil, errors.New("chat channel handler is required")
	}
	if err := r.ValidateConfig(id, config.Config); err != nil {
		return nil, err
	}
	config.TriggerID = strings.TrimSpace(config.TriggerID)
	config.Config = cloneConfig(config.Config)
	instance, err := factory.New(config)
	if err != nil {
		return nil, fmt.Errorf("create chat channel %q: %w", strings.TrimSpace(id), err)
	}
	return instance, nil
}

func (r *Registry) StartSetup(ctx context.Context, id string, config SetupStartConfig) (SetupSession, SetupResult, error) {
	factory, ok := r.factory(id)
	if !ok {
		return nil, SetupResult{}, fmt.Errorf("%w: %q", ErrChannelNotFound, strings.TrimSpace(id))
	}
	setupFactory, ok := factory.(SetupFactory)
	if !ok {
		return nil, SetupResult{}, fmt.Errorf("%w: %q", ErrSetupUnavailable, strings.TrimSpace(id))
	}
	config.ExistingConfig = cloneConfig(config.ExistingConfig)
	session, result, err := setupFactory.StartSetup(ctx, config)
	if err != nil {
		return nil, SetupResult{}, fmt.Errorf("start chat channel %q setup: %w", strings.TrimSpace(id), err)
	}
	if session == nil {
		return nil, SetupResult{}, fmt.Errorf("start chat channel %q setup: session is nil", strings.TrimSpace(id))
	}
	result.CredentialConfig = cloneConfig(result.CredentialConfig)
	return session, result, nil
}

func (r *Registry) CredentialID(id string, config map[string]any) string {
	factory, ok := r.factory(id)
	if !ok {
		return ""
	}
	identifier, ok := factory.(CredentialIdentifier)
	if !ok {
		return ""
	}
	return strings.TrimSpace(identifier.CredentialID(cloneConfig(config)))
}

func (r *Registry) RedactConfig(id string, config map[string]any) map[string]any {
	definition, ok := r.Definition(id)
	if !ok {
		return cloneConfig(config)
	}
	result := cloneConfig(config)
	redactWriteOnly(definition.ConfigSchema, result)
	return result
}

func (r *Registry) MergeWriteOnlyConfig(id string, stored, incoming map[string]any) map[string]any {
	definition, ok := r.Definition(id)
	if !ok {
		return cloneConfig(incoming)
	}
	result := cloneConfig(incoming)
	preserveWriteOnly(definition.ConfigSchema, stored, result)
	return result
}

func (r *Registry) MapWriteOnlyConfig(id string, config map[string]any, mapper func(string, any) (any, error)) (map[string]any, error) {
	definition, ok := r.Definition(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrChannelNotFound, strings.TrimSpace(id))
	}
	if mapper == nil {
		return nil, errors.New("write-only config mapper is nil")
	}
	result := cloneConfig(config)
	if err := mapWriteOnlyConfig(definition.ConfigSchema, result, "", mapper); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Registry) ValidateSecretRefs(id string, config map[string]any) error {
	_, err := r.MapWriteOnlyConfig(id, config, func(path string, value any) (any, error) {
		ref, err := ParseSecretRef(value)
		if err != nil {
			return nil, fmt.Errorf("channel %q config %q: %w", strings.TrimSpace(id), path, err)
		}
		return ref, nil
	})
	return err
}

func (r *Registry) ResolveConfig(ctx context.Context, id string, config map[string]any, resolver SecretResolver) (map[string]any, error) {
	return r.MapWriteOnlyConfig(id, config, func(path string, value any) (any, error) {
		if resolver == nil {
			return nil, fmt.Errorf("channel %q config %q secret resolver is required", strings.TrimSpace(id), path)
		}
		ref, err := ParseSecretRef(value)
		if err != nil {
			return nil, fmt.Errorf("channel %q config %q: %w", strings.TrimSpace(id), path, err)
		}
		resolved, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("resolve channel %q config %q: %w", strings.TrimSpace(id), path, err)
		}
		if strings.TrimSpace(resolved) == "" {
			return nil, fmt.Errorf("resolve channel %q config %q: secret is empty", strings.TrimSpace(id), path)
		}
		return resolved, nil
	})
}

func ParseSecretRef(value any) (dsl.SecretRef, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return dsl.SecretRef{}, fmt.Errorf("invalid secret reference: %w", err)
	}
	var ref dsl.SecretRef
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil {
		return dsl.SecretRef{}, fmt.Errorf("invalid secret reference: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return dsl.SecretRef{}, fmt.Errorf("invalid secret reference: %w", err)
	}
	ref.Source = strings.ToLower(strings.TrimSpace(ref.Source))
	ref.Ref = strings.TrimSpace(ref.Ref)
	if err := ref.Validate(); err != nil {
		return dsl.SecretRef{}, fmt.Errorf("invalid secret reference: %w", err)
	}
	return ref, nil
}

func (r *Registry) factory(id string) (Factory, bool) {
	if r == nil {
		return nil, false
	}
	id = strings.TrimSpace(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	factory, ok := r.factories[id]
	return factory, ok
}

func normalizeDefinition(definition Definition) Definition {
	definition.ID = strings.TrimSpace(definition.ID)
	definition.Title = strings.TrimSpace(definition.Title)
	definition.Description = strings.TrimSpace(definition.Description)
	if definition.Setup != nil {
		setup := *definition.Setup
		setup.Kind = strings.TrimSpace(setup.Kind)
		definition.Setup = &setup
	}
	return definition
}

func cloneDefinition(definition Definition) Definition {
	definition.ConfigSchema = cloneConfig(definition.ConfigSchema)
	if definition.Setup != nil {
		setup := *definition.Setup
		definition.Setup = &setup
	}
	return definition
}

func cloneConfig(config map[string]any) map[string]any {
	if config == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return map[string]any{}
	}
	return result
}

func validateJSONConfig(config map[string]any) error {
	if _, err := json.Marshal(config); err != nil {
		return fmt.Errorf("%w: must contain JSON-compatible values: %v", ErrInvalidConfig, err)
	}
	return nil
}

func redactWriteOnly(schema, value map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	for key, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		if writeOnly, _ := property["writeOnly"].(bool); writeOnly {
			delete(value, key)
			continue
		}
		child, _ := value[key].(map[string]any)
		if child != nil {
			redactWriteOnly(property, child)
		}
	}
}

func preserveWriteOnly(schema, stored, incoming map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	for key, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		if writeOnly, _ := property["writeOnly"].(bool); writeOnly {
			if value, exists := stored[key]; exists && emptyWriteOnlyValue(incoming[key]) {
				incoming[key] = value
			}
			continue
		}
		storedChild, _ := stored[key].(map[string]any)
		incomingChild, _ := incoming[key].(map[string]any)
		if storedChild != nil && incomingChild != nil {
			preserveWriteOnly(property, storedChild, incomingChild)
		}
	}
}

func mapWriteOnlyConfig(schema, value map[string]any, prefix string, mapper func(string, any) (any, error)) error {
	properties, _ := schema["properties"].(map[string]any)
	for key, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if writeOnly, _ := property["writeOnly"].(bool); writeOnly {
			current, exists := value[key]
			if !exists {
				continue
			}
			mapped, err := mapper(path, current)
			if err != nil {
				return err
			}
			if mapped == nil {
				delete(value, key)
				continue
			}
			value[key] = mapped
			continue
		}
		child, _ := value[key].(map[string]any)
		if child != nil {
			if err := mapWriteOnlyConfig(property, child, path, mapper); err != nil {
				return err
			}
		}
	}
	return nil
}

func emptyWriteOnlyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

type httpFactory struct{}

func (httpFactory) Definition() Definition {
	return Definition{
		ID:          HTTPChannelID,
		Title:       "HTTP / SSE",
		Description: "Receive chat messages through the Chat Trigger HTTP endpoint.",
		ConfigSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (httpFactory) ValidateConfig(config map[string]any) error {
	if len(config) != 0 {
		return errors.New("http channel does not accept configuration")
	}
	return nil
}

func (httpFactory) New(InstanceConfig) (Instance, error) {
	return nil, nil
}
