package chatchannel

import (
	"context"
	"errors"
	"testing"
)

type testFactory struct{}

func (testFactory) Definition() Definition {
	return Definition{
		ID:    "test",
		Title: "Test",
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

func (testFactory) ValidateConfig(config map[string]any) error {
	if config["name"] == "invalid" {
		return errors.New("invalid name")
	}
	return nil
}

func (testFactory) New(InstanceConfig) (Instance, error) { return testInstance{}, nil }

type testInstance struct{}

func (testInstance) Run(context.Context) error { return nil }

func TestRegistryExposesDefinitionsAndProtectsWriteOnlyConfig(t *testing.T) {
	registry := NewDefaultRegistry()
	if err := registry.Register(testFactory{}); err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].ID != HTTPChannelID || definitions[1].ID != "test" {
		t.Fatalf("definitions = %#v", definitions)
	}
	stored := map[string]any{"name": "bot", "secret": "stored"}
	redacted := registry.RedactConfig("test", stored)
	if _, exists := redacted["secret"]; exists || stored["secret"] != "stored" {
		t.Fatalf("redacted = %#v, stored = %#v", redacted, stored)
	}
	merged := registry.MergeWriteOnlyConfig("test", stored, map[string]any{"name": "updated"})
	if merged["secret"] != "stored" || merged["name"] != "updated" {
		t.Fatalf("merged = %#v", merged)
	}
}

func TestRegistryValidatesConfigAndHandler(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testFactory{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateConfig("missing", nil); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("missing channel error = %v", err)
	}
	if err := registry.ValidateConfig("test", map[string]any{"name": "invalid"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid config error = %v", err)
	}
	if _, err := registry.NewInstance("test", InstanceConfig{}); err == nil {
		t.Fatal("expected missing handler error")
	}
}
