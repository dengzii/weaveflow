package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/llms"

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
				"provider": "mistral",
				"api_format": "chat_completions",
				"model": "gpt-fast",
				"base_url": "http://127.0.0.1:9999/v1",
				"extra_body": {"safe_prompt": true}
			}
		]
	}`
	uploaded := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("persisted-graph", "v1", "persisted", settings))
	repeatedSettings := `{
		"environment": {"WEAVEFLOW_PERSISTED_SETTING": "saved"},
		"models": [
			{"id":"default","enabled":true,"provider":"openai","model":"gpt-persisted","base_url":"http://127.0.0.1:9999/v1"},
			{"id":"fast","enabled":true,"provider":"mistral","api_format":"chat_completions","model":"gpt-fast","base_url":"http://127.0.0.1:9999/v1","extra_body":{"safe_prompt":true}}
		]
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
	if settingsResponse.Models[1].Provider != "mistral" || settingsResponse.Models[1].APIFormat != "chat_completions" || settingsResponse.Models[1].ExtraBody["safe_prompt"] != true {
		t.Fatalf("restored fast provider settings = %#v", settingsResponse.Models[1])
	}
	if !settingsResponse.Models[0].APIKeyConfigured || !settingsResponse.Models[1].APIKeyConfigured {
		t.Fatalf("restored API key flags = %#v", settingsResponse.Models)
	}
	if settingsResponse.Environment["WEAVEFLOW_PERSISTED_SETTING"] != "saved" {
		t.Fatalf("restored environment = %#v", settingsResponse.Environment)
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

func TestGraphRuntimeSettingsRebuildsModelPricingAndToolGovernance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl-settings",
			"object":"chat.completion",
			"model":"priced-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}
		}`))
	}))
	defer providerServer.Close()

	handled := 0
	tool := core.Tool{
		Function:    &llms.FunctionDefinition{Name: "write_file"},
		Permissions: []string{"filesystem.write"},
		Approval:    core.ToolApprovalRequired,
		Handler: func(_ context.Context, call llms.ToolCall) (llms.ToolResult, error) {
			handled++
			return llms.ToolResult{ToolCallID: call.ID, Content: "written"}, nil
		},
	}
	srv, err := New(context.Background(), Config{
		BaseDir: t.TempDir(),
		RuntimeContextDecorators: []RuntimeContextDecorator{
			func(ctx context.Context) context.Context {
				return core.WithTools(ctx, map[string]core.Tool{"write_file": tool})
			},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	wantSettings := graphRuntimeSettings{
		Environment: map[string]string{"WEAVEFLOW_SETTINGS_TEST": "persisted"},
		Models: []graphModelSettings{{
			ID:       core.DefaultModelID,
			Enabled:  true,
			Provider: "openai",
			Model:    "priced-model",
			BaseURL:  providerServer.URL + "/v1",
			Pricing: llms.ModelPricing{
				Currency:         "usd",
				InputPerMillion:  2,
				OutputPerMillion: 4,
			},
		}},
		ToolPermissions: []string{"filesystem.write"},
		ToolApprovals:   map[string]bool{"write_file": true},
	}
	if err := persistGraphRuntimeSettings(srv.baseDir, wantSettings); err != nil {
		t.Fatalf("persistGraphRuntimeSettings() error = %v", err)
	}
	gotSettings, found, err := loadGraphRuntimeSettings(srv.baseDir)
	if err != nil {
		t.Fatalf("loadGraphRuntimeSettings() error = %v", err)
	}
	if !found {
		t.Fatal("loadGraphRuntimeSettings() did not find persisted settings")
	}
	if gotSettings.Models[0].Pricing != (llms.ModelPricing{
		Currency:         "USD",
		InputPerMillion:  2,
		OutputPerMillion: 4,
	}) {
		t.Fatalf("persisted pricing = %#v", gotSettings.Models[0].Pricing)
	}
	if strings.Join(gotSettings.ToolPermissions, ",") != "filesystem.write" || !gotSettings.ToolApprovals["write_file"] {
		t.Fatalf("persisted tool governance = permissions=%#v approvals=%#v", gotSettings.ToolPermissions, gotSettings.ToolApprovals)
	}

	runtimeContext, err := srv.buildRuntimeContext(gotSettings, "test-key")
	if err != nil {
		t.Fatalf("buildRuntimeContext() error = %v", err)
	}
	permissions, configured := core.ToolPermissionsFromContext(runtimeContext)
	if !configured || strings.Join(permissions, ",") != "filesystem.write" {
		t.Fatalf("rebuilt tool permissions = %#v, configured=%v", permissions, configured)
	}
	model := core.NewContext(runtimeContext).Model()
	if model == nil {
		t.Fatal("rebuilt model is nil")
	}
	response, err := core.GenerateModel(runtimeContext, model, llms.ModelRequest{
		Mode:     llms.ModelModeChat,
		Messages: []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
	})
	if err != nil {
		t.Fatalf("GenerateModel() error = %v", err)
	}
	if response.Cost == nil || response.Cost.Currency != "USD" || math.Abs(response.Cost.Total-0.0004) > 1e-12 {
		t.Fatalf("rebuilt model cost = %#v", response.Cost)
	}

	rebuiltTool, ok := core.ToolsFromContext(runtimeContext)["write_file"]
	if !ok {
		t.Fatal("rebuilt tool is missing")
	}
	result, err := core.ExecuteTool(runtimeContext, rebuiltTool, llms.ToolCall{
		ID:   "settings-call",
		Type: "function",
		FunctionCall: &llms.FunctionCall{
			Name:      "write_file",
			Arguments: json.RawMessage(`{}`),
		},
	})
	if err != nil || result.Content != "written" || handled != 1 {
		t.Fatalf("rebuilt tool execution = result=%#v err=%v handled=%d", result, err, handled)
	}
}

func TestBuildRuntimeContextWiresProviderAndExtraBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var captured map[string]any
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"model":"deepseek-chat",
			"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
		}`))
	}))
	defer providerServer.Close()

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	runtimeContext, err := srv.buildRuntimeContext(graphRuntimeSettings{
		Models: []graphModelSettings{{
			ID:        core.DefaultModelID,
			Enabled:   true,
			Provider:  "deepseek",
			APIFormat: "chat_completions",
			Model:     "deepseek-chat",
			BaseURL:   providerServer.URL + "/v1",
			ExtraBody: map[string]any{"custom_option": "enabled"},
		}},
	}, "test-key")
	if err != nil {
		t.Fatalf("build runtime context: %v", err)
	}
	model := core.NewContext(runtimeContext).Model()
	if model == nil {
		t.Fatal("runtime model is nil")
	}
	_, err = core.GenerateModel(runtimeContext, model, llms.ModelRequest{
		Mode:      llms.ModelModeChat,
		Messages:  []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "question")},
		MaxTokens: 64,
		Thinking:  llms.ThinkingModeHigh,
	})
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}
	if captured["max_tokens"] != float64(64) || captured["custom_option"] != "enabled" {
		t.Fatalf("provider request = %#v", captured)
	}
	thinking, _ := captured["thinking"].(map[string]any)
	if thinking["type"] != "enabled" {
		t.Fatalf("provider thinking = %#v", thinking)
	}
}

func TestLoadGraphRuntimeSettingsRejectsModelWithoutID(t *testing.T) {
	baseDir := t.TempDir()
	data := []byte(`{"version":3,"environment":{},"models":[{"enabled":true,"provider":"openai"}],"tool_permissions":[],"tool_approvals":{}}`)
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
	testServer, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	testServer.RegisterRoutes(engine.Group(""))

	_, requestBody := graphSessionRequestBodyForTest(t, graphUploadBodyWithSettings(
		"invalid-settings",
		"v1",
		"invalid",
		`{"environment":{},"models":[{"enabled":true,"provider":"openai"}]}`,
	))
	response := serveHTTP(engine, http.MethodPost, "/graphs/invalid-settings/sessions", requestBody)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "model id is required") {
		t.Fatalf("POST graph session status = %d, body = %s", response.Code, response.Body.String())
	}
}
