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

func TestGraphSessionSettingsPersistAcrossServerRestart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("WEAVEFLOW_PERSISTED_SETTING", "")

	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	settings := `{
		"environment": {"TAVILY_API_KEY": "persisted-tavily-key", "WEAVEFLOW_PERSISTED_SETTING": "saved"},
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
	uploaded := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("persisted-graph", "v1", "persisted", settings))
	repeatedSettings := `{
		"environment": {"WEAVEFLOW_PERSISTED_SETTING": "saved"},
		"models": [
			{"id":"default","enabled":true,"provider":"openai","model":"gpt-persisted","base_url":"http://127.0.0.1:9999/v1"},
			{"id":"fast","enabled":true,"provider":"openai","model":"gpt-fast","base_url":"http://127.0.0.1:9999/v1"}
		],
		"memory": {"enabled": true}
	}`
	repeated := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("persisted-graph", "v1", "persisted", repeatedSettings))
	if repeated.Graph.GraphSessionID != uploaded.Graph.GraphSessionID {
		t.Fatalf("upload without masked secrets created session %q, want %q", repeated.Graph.GraphSessionID, uploaded.Graph.GraphSessionID)
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
	if err := os.Unsetenv("TAVILY_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("WEAVEFLOW_PERSISTED_SETTING"); err != nil {
		t.Fatal(err)
	}

	restored, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() after settings save error = %v", err)
	}
	graphs, err := restored.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 {
		t.Fatalf("restored graph list = %#v, want one graph", graphs)
	}
	responseData, err := json.Marshal(graphs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(responseData), "persisted-key") || strings.Contains(string(responseData), "persisted-tavily-key") {
		t.Fatalf("graph list leaked persisted API key: %s", responseData)
	}
	restoredEngine := gin.New()
	restored.RegisterRoutes(restoredEngine.Group(""))
	settingsResponse := decodeGraphDetailResponse(
		t,
		serveHTTP(restoredEngine, http.MethodGet, "/graphs/persisted-graph", ""),
		http.StatusOK,
	).Settings
	if len(settingsResponse.Models) != 2 {
		t.Fatalf("restored model count = %d, want 2", len(settingsResponse.Models))
	}
	if settingsResponse.Models[0].ID != core.DefaultModelID || settingsResponse.Models[0].Model != "gpt-persisted" {
		t.Fatalf("restored default model = %#v", settingsResponse.Models[0])
	}
	if settingsResponse.Models[1].ID != "fast" || settingsResponse.Models[1].Model != "gpt-fast" {
		t.Fatalf("restored fast model = %#v", settingsResponse.Models[1])
	}
	if !settingsResponse.Models[0].APIKeyConfigured || !settingsResponse.Models[1].APIKeyConfigured {
		t.Fatalf("restored API key flags = %#v", settingsResponse.Models)
	}
	if settingsResponse.Environment["WEAVEFLOW_PERSISTED_SETTING"] != "saved" {
		t.Fatalf("restored environment = %#v", settingsResponse.Environment)
	}
	if !settingsResponse.Memory.Enabled {
		t.Fatalf("restored memory settings = %#v", settingsResponse.Memory)
	}
	restoredSession, err := restored.loadTriggerSession("persisted-graph")
	if err != nil {
		t.Fatal(err)
	}
	runtimeContext := restoredSession.baseContext
	coreCtx := core.NewContext(runtimeContext)
	if coreCtx.Model() == nil || coreCtx.Model("fast") == nil {
		t.Fatalf("restored runtime models = %#v", core.ModelsFromContext(runtimeContext))
	}
	if got := coreCtx.Environment()["TAVILY_API_KEY"]; got != "persisted-tavily-key" {
		t.Fatalf("restored TAVILY_API_KEY = %q, want persisted-tavily-key", got)
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

func TestGraphUploadRejectsModelWithoutID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	server.RegisterRoutes(engine.Group(""))

	_, requestBody := graphSessionRequestBodyForTest(t, graphUploadBodyWithSettings(
		"invalid-settings",
		"v1",
		"invalid",
		`{"environment":{},"models":[{"enabled":true,"provider":"openai"}],"memory":{"enabled":false}}`,
	))
	response := serveHTTP(engine, http.MethodPost, "/graphs/invalid-settings/sessions", requestBody)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "model id is required") {
		t.Fatalf("POST graph session status = %d, body = %s", response.Code, response.Body.String())
	}
}
