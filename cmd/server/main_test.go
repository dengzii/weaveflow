package main

import (
	"strings"
	"testing"
)

func TestAssistantConfigFromEnvironment(t *testing.T) {
	t.Run("disabled when unset", func(t *testing.T) {
		t.Setenv("WEAVEFLOW_ASSISTANT_MODEL", "")
		t.Setenv("WEAVEFLOW_ASSISTANT_API_KEY", "")
		config, err := assistantConfigFromEnvironment()
		if err != nil || config != nil {
			t.Fatalf("config = %#v, err = %v", config, err)
		}
	})

	t.Run("requires model and key together", func(t *testing.T) {
		t.Setenv("WEAVEFLOW_ASSISTANT_MODEL", "assistant-model")
		t.Setenv("WEAVEFLOW_ASSISTANT_API_KEY", "")
		if _, err := assistantConfigFromEnvironment(); err == nil || !strings.Contains(err.Error(), "must be configured together") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("enables from model connection environment", func(t *testing.T) {
		t.Setenv("WEAVEFLOW_ASSISTANT_MODEL", "assistant-model")
		t.Setenv("WEAVEFLOW_ASSISTANT_API_KEY", "assistant-key")
		t.Setenv("WEAVEFLOW_ASSISTANT_BASE_URL", "https://example.com/v1")
		t.Setenv("WEAVEFLOW_ASSISTANT_PROVIDER", "openai")
		t.Setenv("WEAVEFLOW_ASSISTANT_API_FORMAT", "responses")
		config, err := assistantConfigFromEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		if config == nil || config.Model == nil || config.ModelID != "assistant-model" {
			t.Fatalf("config = %#v", config)
		}
	})
}
