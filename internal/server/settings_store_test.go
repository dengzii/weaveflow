package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"

	"github.com/gin-gonic/gin"
)

func TestRuntimeSettingsPersistAcrossServerRestart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("WEAVEFLOW_PERSISTED_SETTING", "")

	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	body := `{
		"environment": {"WEAVEFLOW_PERSISTED_SETTING": "saved"},
		"models": [
			{
				"id": "default",
				"enabled": true,
				"provider": "openai",
				"api_key": "persisted-key",
				"model": "gpt-persisted",
				"base_url": "http://127.0.0.1:9999/v1"
			},
			{
				"id": "fast",
				"enabled": true,
				"provider": "openai",
				"model": "gpt-fast",
				"base_url": "http://127.0.0.1:9999/v1"
			}
		],
		"memory": {"enabled": true}
	}`
	response := serveHTTP(engine, http.MethodPut, "/runtime/settings", body)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT /runtime/settings status = %d, body = %s", response.Code, response.Body.String())
	}

	response = serveHTTP(engine, http.MethodPut, "/runtime/settings", `{
		"models": [
			{"id":"default","enabled":true,"provider":"openai","model":"gpt-persisted","base_url":"http://127.0.0.1:9999/v1"},
			{"id":"fast","enabled":true,"provider":"openai","model":"gpt-fast","base_url":"http://127.0.0.1:9999/v1"}
		]
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("resave graph settings status = %d, body = %s", response.Code, response.Body.String())
	}

	if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("OPENAI_MODEL"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("OPENAI_BASE_URL"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("WEAVEFLOW_PERSISTED_SETTING"); err != nil {
		t.Fatal(err)
	}

	restored, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() after settings save error = %v", err)
	}
	restoredEngine := gin.New()
	restored.RegisterRoutes(restoredEngine.Group(""))
	response = serveHTTP(restoredEngine, http.MethodGet, "/runtime/settings", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /runtime/settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "persisted-key") {
		t.Fatalf("GET /runtime/settings leaked persisted API key: %s", response.Body.String())
	}
	var settingsResponse struct {
		Data graphRuntimeSettings `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &settingsResponse); err != nil {
		t.Fatalf("decode graph settings response: %v", err)
	}
	if len(settingsResponse.Data.Models) != 2 {
		t.Fatalf("restored model count = %d, want 2", len(settingsResponse.Data.Models))
	}
	if settingsResponse.Data.Models[0].ID != core.DefaultModelID || settingsResponse.Data.Models[0].Model != "gpt-persisted" {
		t.Fatalf("restored default model = %#v", settingsResponse.Data.Models[0])
	}
	if settingsResponse.Data.Models[1].ID != "fast" || settingsResponse.Data.Models[1].Model != "gpt-fast" {
		t.Fatalf("restored fast model = %#v", settingsResponse.Data.Models[1])
	}
	if !settingsResponse.Data.Models[0].APIKeyConfigured || !settingsResponse.Data.Models[1].APIKeyConfigured {
		t.Fatalf("restored API key flags = %#v", settingsResponse.Data.Models)
	}
	if settingsResponse.Data.Environment["WEAVEFLOW_PERSISTED_SETTING"] != "saved" {
		t.Fatalf("restored environment = %#v", settingsResponse.Data.Environment)
	}
	if !settingsResponse.Data.Memory.Enabled {
		t.Fatalf("restored memory settings = %#v", settingsResponse.Data.Memory)
	}
	runtimeContext := restored.runtime.runtimeContext()
	coreCtx := core.NewContext(runtimeContext)
	if coreCtx.Model() == nil || coreCtx.Model("fast") == nil {
		t.Fatalf("restored runtime models = %#v", core.ModelsFromContext(runtimeContext))
	}
}

func TestLoadGraphRuntimeSettingsRejectsModelWithoutID(t *testing.T) {
	baseDir := t.TempDir()
	data := []byte(`{"version":1,"environment":{},"models":[{"enabled":true,"provider":"openai"}],"memory":{"enabled":false}}`)
	if err := os.WriteFile(graphRuntimeSettingsPath(baseDir), data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadGraphRuntimeSettings(baseDir)
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("loadGraphRuntimeSettings() error = %v", err)
	}
}

func TestRuntimeSettingsRejectsModelWithoutID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	server.RegisterRoutes(engine.Group(""))

	response := serveHTTP(engine, http.MethodPut, "/runtime/settings", `{"models":[{"enabled":true,"provider":"openai"}]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "model id is required") {
		t.Fatalf("PUT /runtime/settings status = %d, body = %s", response.Code, response.Body.String())
	}
}
