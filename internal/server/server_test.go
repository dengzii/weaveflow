package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/chatchannel/wecom"
	"github.com/dengzii/weaveflow/internal/chatchannel/weixin"
	"github.com/dengzii/weaveflow/node"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/tmc/langchaingo/llms"
)

type contractTestNode struct {
	core.NodeBase
}

func TestRegistryResponseIncludesNodeGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := wfregistry.NewRegistry()
	if err := reg.RegisterNodeTypeInGroup("Models", wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{Type: "llm_turn", Title: "LLM Turn", ConfigSchema: dsl.JSONSchema{"type": "object"}},
		Build:          func(*wfregistry.BuildContext, wfregistry.ResolvedNodeSpec) (core.Node, error) { return nil, nil },
	}); err != nil {
		t.Fatalf("register grouped node type: %v", err)
	}

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.GET("/registry", srv.handleGetRegistry)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/registry", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /registry status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data registryResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode registry response: %v", err)
	}
	if len(response.Data.NodeGroups) != 1 || response.Data.NodeGroups[0].Name != "Models" {
		t.Fatalf("node groups = %#v", response.Data.NodeGroups)
	}
	if nodeTypes := response.Data.NodeGroups[0].NodeTypes; len(nodeTypes) != 1 || nodeTypes[0] != "llm_turn" {
		t.Fatalf("grouped node types = %#v", nodeTypes)
	}
	if len(response.Data.ChatChannels) != 3 || response.Data.ChatChannels[0].ID != chatchannel.HTTPChannelID || response.Data.ChatChannels[1].ID != wecom.ChannelID || response.Data.ChatChannels[2].ID != weixin.ChannelID {
		t.Fatalf("chat channels = %#v", response.Data.ChatChannels)
	}
	if response.Data.ChatChannels[1].Title != "WeCom Bot" || response.Data.ChatChannels[2].Title != "WeChat Bot" {
		t.Fatalf("chat channel titles = %#v", response.Data.ChatChannels)
	}
	properties, _ := response.Data.ChatChannels[1].ConfigSchema["properties"].(map[string]any)
	secret, _ := properties["secret"].(map[string]any)
	if secret["writeOnly"] != true {
		t.Fatalf("WeCom secret schema = %#v", secret)
	}
	properties, _ = response.Data.ChatChannels[2].ConfigSchema["properties"].(map[string]any)
	botToken, _ := properties["bot_token"].(map[string]any)
	if botToken["writeOnly"] != true {
		t.Fatalf("WeChat bot token schema = %#v", botToken)
	}
	if setup := response.Data.ChatChannels[2].Setup; setup == nil || setup.Kind != chatchannel.SetupKindQRCode {
		t.Fatalf("WeChat setup definition = %#v", setup)
	}
}

func newContractTestNode(spec dsl.GraphNodeSpec) *contractTestNode {
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	return &contractTestNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			ID:   spec.ID,
			Name: name,
		}),
	}
}

func (n *contractTestNode) Execute(core.Context, *state.Access) error {
	return nil
}

type interruptTestNode struct {
	core.NodeBase
	resumePath state.Path
}

func TestBindGraphUploadRejectsLegacyDefinitionFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(`{
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"nodes": [{"id":"node","type":"conversation_message","state":{}}],
			"state_schema": "legacy"
		}
	}`))
	if _, err := bindGraphUpload(ginContext); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("bindGraphUpload() error = %v, want unknown field", err)
	}
}

func TestBindGraphUploadRejectsLegacyEnvelopeForms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []string{
		`{"graph": {}}`,
		`{"id": "legacy", "definition": {}}`,
		`{"version":"2.0","state_modules":[],"nodes":[]}`,
	}
	for _, body := range tests {
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginContext.Request = httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(body))
		if _, err := bindGraphUpload(ginContext); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("bindGraphUpload(%s) error = %v, want unknown field", body, err)
		}
	}
}

func TestBindGraphUploadRejectsUnknownSettingsFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"definition":{},"settings":{"modle": {}}}`,
		`{"definition":{},"settings":{"model": {}}}`,
		`{"definition":{},"settings":{"memory": {"enabled": true, "path": "legacy"}}}`,
	} {
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginContext.Request = httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(body))
		if _, err := bindGraphUpload(ginContext); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("bindGraphUpload(%s) error = %v, want unknown field", body, err)
		}
	}
}

func TestDecodeRunRequestsRejectLegacyPayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		body   string
		decode func(*gin.Context) (*state.State, error)
	}{
		{name: "start state wrapper", body: `{"state": {}}`, decode: decodeStartRunRequest},
		{name: "start input wrapper", body: `{"input": {}}`, decode: decodeStartRunRequest},
		{name: "start direct state", body: `{"shared": {}}`, decode: decodeStartRunRequest},
		{name: "start non-object state", body: `{"initial_state": "legacy"}`, decode: decodeStartRunRequest},
		{name: "resume state wrapper", body: `{"state": {}}`, decode: decodeResumeRunRequest},
		{name: "resume initial state wrapper", body: `{"initial_state": {}}`, decode: decodeResumeRunRequest},
		{name: "resume direct state", body: `{"shared": {}}`, decode: decodeResumeRunRequest},
		{name: "resume non-object input", body: `{"input": []}`, decode: decodeResumeRunRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(test.body))
			if _, err := test.decode(ginContext); err == nil {
				t.Fatalf("decode(%s) error = nil, want invalid request", test.body)
			}
		})
	}
}

func newInterruptTestNode(spec dsl.GraphNodeSpec, resumePath state.Path) *interruptTestNode {
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	return &interruptTestNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			ID:   spec.ID,
			Name: name,
		}),
		resumePath: resumePath,
	}
}

func (n *interruptTestNode) Execute(_ core.Context, access *state.Access) error {
	if value, ok := access.ReadAny(n.resumePath); ok && value == "ok" {
		return nil
	}
	return &langgraph.NodeInterrupt{Node: n.ID(), Value: "waiting for resume input"}
}

func newRunControlTestGraph(t *testing.T, started chan<- struct{}, release <-chan struct{}, respectContext bool) *wfgraph.Graph {
	t.Helper()
	graph := wfgraph.NewGraph()
	var startOnce sync.Once
	err := graph.AddNode(node.NewFuncNode(node.Spec{ID: "work", Name: "work"}, func(ctx core.Context, access *state.Access) error {
		startOnce.Do(func() {
			close(started)
		})
		if respectContext {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			<-release
		}
		return access.SetAny(state.Shared("done"), true)
	}))
	if err != nil {
		t.Fatalf("add work node: %v", err)
	}
	if err := graph.SetEntryPoint("work"); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := graph.SetFinishPoint("work"); err != nil {
		t.Fatalf("set finish point: %v", err)
	}
	return graph
}

type recordingEventSink struct {
	mu     sync.Mutex
	events []runtime.Event
}

func (s *recordingEventSink) Publish(_ context.Context, event runtime.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingEventSink) PublishBatch(ctx context.Context, events []runtime.Event) error {
	for _, event := range events {
		if err := s.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *recordingEventSink) ListEvents(runID string) ([]runtime.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]runtime.Event, 0, len(s.events))
	for _, event := range s.events {
		if runID == "" || event.RunID == runID {
			out = append(out, event)
		}
	}
	return out, nil
}

func TestHandleRuntimeToolsReturnsDefinitions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx := core.WithTools(context.Background(), map[string]core.Tool{
		"zeta": {
			Function: &llms.FunctionDefinition{
				Name:        "zeta",
				Description: "Run zeta.",
				Parameters:  map[string]any{"type": "object"},
			},
		},
		"alpha": {
			Function: &llms.FunctionDefinition{
				Name:        "alpha_tool",
				Description: "Run alpha.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{"type": "string"},
					},
				},
				Strict: true,
			},
		},
	})
	srv, err := New(ctx, Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	w := serveHTTP(engine, http.MethodGet, "/runtime/tools", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runtime/tools status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Data  toolsResponse `json:"data"`
		Error *apiError     `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode tools response: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("response error = %#v", response.Error)
	}
	if len(response.Data.Tools) != 2 {
		t.Fatalf("tools length = %d, want 2", len(response.Data.Tools))
	}
	if response.Data.Tools[0].ID != "alpha" || response.Data.Tools[1].ID != "zeta" {
		t.Fatalf("tools order = %#v, want sorted by id", response.Data.Tools)
	}
	alpha := response.Data.Tools[0]
	if alpha.Name != "alpha_tool" || alpha.Description != "Run alpha." || !alpha.Strict {
		t.Fatalf("alpha tool definition = %#v", alpha)
	}
	parameters, ok := alpha.Parameters.(map[string]any)
	if !ok || parameters["type"] != "object" {
		t.Fatalf("alpha parameters = %#v, want object schema", alpha.Parameters)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "handler") {
		t.Fatalf("tools response leaked handler field: %s", w.Body.String())
	}
}

func TestGraphUploadUpdatesSessionRuntimeSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("WEAVEFLOW_TEST_FLAG", "")
	t.Setenv("WEAVEFLOW_TEST_TOKEN", "")

	ctx := core.WithTools(context.Background(), map[string]core.Tool{
		"alpha": {
			Function: &llms.FunctionDefinition{Name: "alpha_tool"},
		},
	})
	srv, err := New(ctx, Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	settings := `{
		"environment": {"TAVILY_API_KEY": "tavily-key", "WEAVEFLOW_TEST_FLAG": "enabled", "WEAVEFLOW_TEST_TOKEN": "secret-token"},
		"models": [
			{
				"id": "default",
				"enabled": true,
				"provider": "openai",
				"api_key": "test-key",
				"model": "gpt-test",
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
	uploaded := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("settings-graph", "v1", "settings", settings))

	if len(uploaded.Settings.Models) != 2 {
		t.Fatalf("models length = %d, want 2", len(uploaded.Settings.Models))
	}
	if uploaded.Settings.Models[0].ID != core.DefaultModelID || uploaded.Settings.Models[0].Model != "gpt-test" {
		t.Fatalf("default model settings = %#v", uploaded.Settings.Models[0])
	}
	if uploaded.Settings.Models[1].ID != "fast" || uploaded.Settings.Models[1].Model != "gpt-fast" {
		t.Fatalf("fast model settings = %#v", uploaded.Settings.Models[1])
	}
	if !uploaded.Settings.Models[0].APIKeyConfigured || !uploaded.Settings.Models[1].APIKeyConfigured {
		t.Fatalf("api key configured flags = %#v", uploaded.Settings.Models)
	}
	if !uploaded.Settings.Memory.Enabled || uploaded.Settings.Memory.Directory == "" {
		t.Fatalf("memory settings = %#v", uploaded.Settings.Memory)
	}
	if uploaded.Settings.Environment["WEAVEFLOW_TEST_FLAG"] != "enabled" {
		t.Fatalf("environment = %#v", uploaded.Settings.Environment)
	}
	if _, ok := uploaded.Settings.Environment["WEAVEFLOW_TEST_TOKEN"]; ok {
		t.Fatalf("settings response leaked secret environment name: %#v", uploaded.Settings.Environment)
	}
	if _, ok := uploaded.Settings.Environment["TAVILY_API_KEY"]; ok {
		t.Fatalf("settings response leaked TAVILY_API_KEY: %#v", uploaded.Settings.Environment)
	}
	responseData, err := json.Marshal(uploaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(responseData), "test-key") || strings.Contains(string(responseData), "secret-token") || strings.Contains(string(responseData), "tavily-key") {
		t.Fatalf("upload response leaked a secret: %s", responseData)
	}
	if got := os.Getenv("WEAVEFLOW_TEST_TOKEN"); got != "" {
		t.Fatalf("graph upload mutated process WEAVEFLOW_TEST_TOKEN = %q", got)
	}
	if got := os.Getenv("WEAVEFLOW_TEST_FLAG"); got != "" {
		t.Fatalf("graph upload mutated process WEAVEFLOW_TEST_FLAG = %q", got)
	}

	coreCtx := core.NewContext(srv.runtime.runtimeContext())
	if coreCtx.Model() == nil {
		t.Fatalf("runtime context model is nil")
	}
	if coreCtx.Model("fast") == nil {
		t.Fatalf("runtime context fast model is nil")
	}
	if coreCtx.Memory() == nil {
		t.Fatalf("runtime context memory is nil")
	}
	if _, ok := coreCtx.Tools()["alpha"]; !ok {
		t.Fatalf("runtime context tools = %#v, want alpha preserved", coreCtx.Tools())
	}
	if got := coreCtx.Environment()["TAVILY_API_KEY"]; got != "tavily-key" {
		t.Fatalf("runtime context TAVILY_API_KEY = %q, want tavily-key", got)
	}

	withoutVisibleEnvironment := `{
		"environment": {},
		"models": [
			{"id":"default","enabled":true,"provider":"openai","model":"gpt-test","base_url":"http://127.0.0.1:9999/v1"},
			{"id":"fast","enabled":true,"provider":"openai","model":"gpt-fast","base_url":"http://127.0.0.1:9999/v1"}
		],
		"memory": {"enabled": true}
	}`
	second := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("settings-graph", "v1", "settings", withoutVisibleEnvironment))
	if second.Graph.GraphSessionID == uploaded.Graph.GraphSessionID {
		t.Fatal("changed graph settings reused the previous session")
	}
	if got := core.EnvironmentVariableFromContext(srv.runtime.runtimeContext(), "WEAVEFLOW_TEST_FLAG"); got != "" {
		t.Fatalf("removed visible environment = %q, want empty", got)
	}
	if got := core.EnvironmentVariableFromContext(srv.runtime.runtimeContext(), "TAVILY_API_KEY"); got != "tavily-key" {
		t.Fatalf("preserved runtime context TAVILY_API_KEY = %q, want tavily-key", got)
	}
}

func TestHandleRuntimeToolsConcurrentWithGraphUploads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := core.WithTools(context.Background(), map[string]core.Tool{
		"alpha": {Function: &llms.FunctionDefinition{Name: "alpha_tool"}},
	})
	srv, err := New(ctx, Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploadBody := triggerGraphUploadBody("concurrent-settings", "v1", "settings")

	const requests = 32
	var wg sync.WaitGroup
	for range requests {
		wg.Add(2)
		go func() {
			defer wg.Done()
			response := serveHTTP(engine, http.MethodGet, "/runtime/tools", "")
			if response.Code != http.StatusOK {
				t.Errorf("GET /runtime/tools status = %d, body = %s", response.Code, response.Body.String())
			}
		}()
		go func() {
			defer wg.Done()
			response := serveHTTP(engine, http.MethodPut, "/graph", uploadBody)
			if response.Code != http.StatusOK {
				t.Errorf("PUT /graph status = %d, body = %s", response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
}

func TestRuntimeSettingsIncludesToolEnvironment(t *testing.T) {
	expected := map[string]string{
		"TAVILY_API_KEY":                      "test-tavily-key",
		"WEAVEFLOW_TOOL_WORKDIR":              t.TempDir(),
		"WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK": "false",
		"WEAVEFLOW_BASH_TIMEOUT":              "120000",
		"WEAVEFLOW_BASH_ALLOWLIST":            "go,git",
		"GIT_BASH":                            "C:/Program Files/Git/bin/bash.exe",
		"MSYS2_BASH":                          "C:/msys64/usr/bin/bash.exe",
		"MINGW_BASH":                          "C:/msys64/mingw64/bin/bash.exe",
	}
	for key, value := range expected {
		t.Setenv(key, value)
	}

	settings := graphRuntimeSettingsFromContext(context.Background(), "")
	for key, value := range expected {
		if settings.Environment[key] != value {
			t.Fatalf("%s = %q, want %q", key, settings.Environment[key], value)
		}
	}

	expectedPresets := map[string]graphEnvironmentPreset{
		"TAVILY_API_KEY":                      {Key: "TAVILY_API_KEY", Type: "string"},
		"WEAVEFLOW_TOOL_WORKDIR":              {Key: "WEAVEFLOW_TOOL_WORKDIR", Type: "string"},
		"WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK": {Key: "WEAVEFLOW_TOOL_SKIP_WORKSPACE_CHECK", DefaultValue: "false", Type: "boolean"},
		"WEAVEFLOW_BASH_TIMEOUT":              {Key: "WEAVEFLOW_BASH_TIMEOUT", DefaultValue: "120000", Type: "integer"},
		"WEAVEFLOW_BASH_ALLOWLIST":            {Key: "WEAVEFLOW_BASH_ALLOWLIST", Type: "string"},
		"GIT_BASH":                            {Key: "GIT_BASH", Type: "string"},
		"MSYS2_BASH":                          {Key: "MSYS2_BASH", Type: "string"},
		"MINGW_BASH":                          {Key: "MINGW_BASH", Type: "string"},
	}
	if len(settings.EnvironmentPresets) != len(expectedPresets) {
		t.Fatalf("environment presets = %#v", settings.EnvironmentPresets)
	}
	for _, preset := range settings.EnvironmentPresets {
		if expectedPresets[preset.Key] != preset {
			t.Fatalf("environment preset %q = %#v, want %#v", preset.Key, preset, expectedPresets[preset.Key])
		}
	}
}

func TestRuntimeSettingsContextEnvironmentOverridesProcess(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "process-key")
	ctx := core.WithEnvironment(context.Background(), map[string]string{"TAVILY_API_KEY": "context-key"})

	settings := graphRuntimeSettingsFromContext(ctx, "")
	if got := settings.Environment["TAVILY_API_KEY"]; got != "context-key" {
		t.Fatalf("TAVILY_API_KEY = %q, want context-key", got)
	}
}

func TestNewPreservesExistingSinkAndBroadcasts(t *testing.T) {
	sink := &recordingEventSink{}
	runner := &runtime.GraphRunner{EventSink: sink}
	srv, err := New(context.Background(), Config{Runner: runner, EventBuffer: 1})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, unsubscribe := srv.EventHub().Subscribe(eventFilter{
		RunID: "run-1",
		Types: map[runtime.EventType]struct{}{
			runtime.EventNodeStarted: {},
		},
	})
	defer unsubscribe()

	event := runtime.Event{
		ID:        "event-1",
		RunID:     "run-1",
		NodeID:    "node-1",
		Type:      runtime.EventNodeStarted,
		Timestamp: time.Now(),
	}
	if err := runner.EventSink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	select {
	case got := <-events:
		if got.ID != event.ID {
			t.Fatalf("broadcast event id = %q, want %q", got.ID, event.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}

	listed, err := runner.ListEvents("run-1")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != event.ID {
		t.Fatalf("listed events = %#v, want one %q event", listed, event.ID)
	}
}

func TestRegisterRoutesMountsOnRouterGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sink := &recordingEventSink{}
	runner := &runtime.GraphRunner{EventSink: sink}
	srv, err := New(context.Background(), Config{Runner: runner})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	event := runtime.Event{
		ID:        "event-1",
		RunID:     "run-1",
		Type:      runtime.EventRunStarted,
		Timestamp: time.Now(),
	}
	if err := runner.EventSink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	engine := gin.New()
	srv.RegisterRoutes(engine.Group("/debug"))

	req := httptest.NewRequest(http.MethodGet, "/debug/runs/run-1/events", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data  runtime.EventPage `json:"data"`
		Error *apiError         `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("response error = %#v", response.Error)
	}
	if len(response.Data.Items) != 1 || response.Data.Items[0].ID != event.ID {
		t.Fatalf("response data = %#v, want one %q event", response.Data, event.ID)
	}
}

func TestListEventsPaginatesNewestFirstAndValidatesParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sink := &recordingEventSink{}
	runner := &runtime.GraphRunner{EventSink: sink}
	srv, err := New(context.Background(), Config{Runner: runner})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for index := 0; index < 501; index++ {
		if err := sink.Publish(context.Background(), runtime.Event{
			ID:    fmt.Sprintf("event-%03d", index),
			RunID: "run-1",
			Type:  runtime.EventRunStarted,
		}); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}

	engine := gin.New()
	srv.RegisterRoutes(engine.Group("/debug"))

	defaultPage := decodeEventPageResponse(t, serveHTTP(engine, http.MethodGet, "/debug/runs/run-1/events", ""), http.StatusOK)
	if len(defaultPage.Items) != defaultEventPageLimit {
		t.Fatalf("default page item count = %d, want %d", len(defaultPage.Items), defaultEventPageLimit)
	}
	if defaultPage.Items[0].ID != "event-500" || defaultPage.Items[len(defaultPage.Items)-1].ID != "event-001" {
		t.Fatalf("default page bounds = %q..%q", defaultPage.Items[0].ID, defaultPage.Items[len(defaultPage.Items)-1].ID)
	}
	if defaultPage.NextCursor == "" {
		t.Fatal("default page next cursor is empty")
	}

	first := decodeEventPageResponse(t, serveHTTP(engine, http.MethodGet, "/debug/runs/run-1/events?limit=2", ""), http.StatusOK)
	if len(first.Items) != 2 || first.Items[0].ID != "event-500" || first.Items[1].ID != "event-499" {
		t.Fatalf("first page = %#v", first)
	}
	second := decodeEventPageResponse(
		t,
		serveHTTP(engine, http.MethodGet, "/debug/runs/run-1/events?limit=2&cursor="+first.NextCursor, ""),
		http.StatusOK,
	)
	if len(second.Items) != 2 || second.Items[0].ID != "event-498" || second.Items[1].ID != "event-497" {
		t.Fatalf("second page = %#v", second)
	}

	maximum := decodeEventPageResponse(t, serveHTTP(engine, http.MethodGet, "/debug/runs/run-1/events?limit=2000", ""), http.StatusOK)
	if len(maximum.Items) != 501 {
		t.Fatalf("maximum page item count = %d, want 501", len(maximum.Items))
	}
	for _, path := range []string{
		"/debug/runs/run-1/events?limit=0",
		"/debug/runs/run-1/events?limit=2001",
		"/debug/runs/run-1/events?cursor=invalid",
	} {
		response := serveHTTP(engine, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400; body = %s", path, response.Code, response.Body.String())
		}
	}
}

func TestInvalidRunStatusReturnsStructuredError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{
		BaseDir: t.TempDir(),
		Runner:  &runtime.GraphRunner{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	response := serveHTTP(engine, http.MethodGet, "/runs?status=unknown", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
	var decoded apiResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Data != nil {
		t.Fatalf("data = %#v, want nil", decoded.Data)
	}
	if decoded.Error == nil || decoded.Error.Code != "invalid_request" || decoded.Error.Message == "" {
		t.Fatalf("error = %#v, want structured invalid_request", decoded.Error)
	}
}

func TestPutGraphConfiguresRunnerForDebugRun(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "conversation_message",
					"config": {"content": "hello"},
					"state": {"conversation": {"path": "scopes.input.conversation"}}
				}
			]
		}
	}`

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}

	var graphResponse struct {
		Data graphLoadResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &graphResponse); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	if graphResponse.Data.Graph.GraphHash == "" {
		t.Fatal("graph response graph_hash is empty")
	}
	if graphResponse.Data.Graph.GraphSnapshotHash == "" {
		t.Fatal("graph response graph_snapshot_hash is empty")
	}
	if graphResponse.Data.Graph.GraphSessionID == "" {
		t.Fatal("graph response graph_session_id is empty")
	}
	if graphResponse.Data.RunnerBaseDir == "" {
		t.Fatal("graph response runner_base_dir is empty")
	}
	if graphResponse.Data.Graph.GraphSessionID != filepath.Base(graphResponse.Data.RunnerBaseDir) {
		t.Fatalf("graph session id = %q, want base dir %q", graphResponse.Data.Graph.GraphSessionID, filepath.Base(graphResponse.Data.RunnerBaseDir))
	}

	definitionPath := filepath.Join(graphResponse.Data.RunnerBaseDir, "definition.json")
	if _, err := os.Stat(definitionPath); err != nil {
		t.Fatalf("stat graph definition snapshot: %v", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(graphResponse.Data.RunnerBaseDir, "graph.json"))
	if err != nil {
		t.Fatalf("read graph session manifest: %v", err)
	}
	var manifest graphSessionManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode graph session manifest: %v", err)
	}
	if manifest.GraphID != "debug-graph" {
		t.Fatalf("manifest graph id = %q, want debug-graph", manifest.GraphID)
	}
	if manifest.GraphHash != graphResponse.Data.Graph.GraphHash {
		t.Fatalf("manifest graph hash = %q, want %q", manifest.GraphHash, graphResponse.Data.Graph.GraphHash)
	}
	if manifest.GraphSnapshotHash != graphResponse.Data.Graph.GraphSnapshotHash {
		t.Fatalf("manifest graph snapshot hash = %q, want %q", manifest.GraphSnapshotHash, graphResponse.Data.Graph.GraphSnapshotHash)
	}
	if manifest.GraphSessionID != graphResponse.Data.Graph.GraphSessionID {
		t.Fatalf("manifest graph session id = %q, want %q", manifest.GraphSessionID, graphResponse.Data.Graph.GraphSessionID)
	}
	if manifest.DefinitionPath != "definition.json" {
		t.Fatalf("manifest definition path = %q, want definition.json", manifest.DefinitionPath)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/graph", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	var currentGraphResponse struct {
		Data graphInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &currentGraphResponse); err != nil {
		t.Fatalf("decode current graph response: %v", err)
	}
	if currentGraphResponse.Data.GraphHash != graphResponse.Data.Graph.GraphHash {
		t.Fatalf("current graph hash = %q, want %q", currentGraphResponse.Data.GraphHash, graphResponse.Data.Graph.GraphHash)
	}
	if currentGraphResponse.Data.GraphSnapshotHash != graphResponse.Data.Graph.GraphSnapshotHash {
		t.Fatalf("current graph snapshot hash = %q, want %q", currentGraphResponse.Data.GraphSnapshotHash, graphResponse.Data.Graph.GraphSnapshotHash)
	}
	if currentGraphResponse.Data.GraphSessionID != graphResponse.Data.Graph.GraphSessionID {
		t.Fatalf("current graph session id = %q, want %q", currentGraphResponse.Data.GraphSessionID, graphResponse.Data.Graph.GraphSessionID)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /runs status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data runResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if response.Data.Run.GraphID != "debug-graph" {
		t.Fatalf("run graph id = %q, want debug-graph", response.Data.Run.GraphID)
	}
	if response.Data.Run.GraphHash != graphResponse.Data.Graph.GraphHash {
		t.Fatalf("run graph hash = %q, want %q", response.Data.Run.GraphHash, graphResponse.Data.Graph.GraphHash)
	}
	if response.Data.Run.GraphSnapshotHash != graphResponse.Data.Graph.GraphSnapshotHash {
		t.Fatalf("run graph snapshot hash = %q, want %q", response.Data.Run.GraphSnapshotHash, graphResponse.Data.Graph.GraphSnapshotHash)
	}
	if response.Data.Run.GraphSessionID != graphResponse.Data.Graph.GraphSessionID {
		t.Fatalf("run graph session id = %q, want %q", response.Data.Run.GraphSessionID, graphResponse.Data.Graph.GraphSessionID)
	}
	if response.Data.Run.Status != runtime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", response.Data.Run.Status, runtime.RunStatusCompleted)
	}
}

func TestPutGraphMetadataOnlyChangeKeepsSemanticHash(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	first := putGraphForHashTest(t, engine, `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{"id": "input", "type": "conversation_message", "config": {"content": "hello"}, "state": {"conversation": {"path": "scopes.input.conversation"}}}
			],
			"metadata": {"web": {
				"positions": {"input": {"x": 10, "y": 20}},
				"trigger_nodes": {"incoming": {"x": -320, "y": 80}}
			}}
		}
	}`)
	time.Sleep(time.Millisecond)
	second := putGraphForHashTest(t, engine, `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{"id": "input", "type": "conversation_message", "config": {"content": "hello"}, "state": {"conversation": {"path": "scopes.input.conversation"}}}
			],
			"metadata": {"web": {
				"positions": {"input": {"x": 30, "y": 40}},
				"trigger_nodes": {"incoming": {"x": -360, "y": 120}}
			}}
		}
	}`)

	if first.Graph.GraphHash != second.Graph.GraphHash {
		t.Fatalf("semantic graph hash changed after metadata-only change: %q != %q", first.Graph.GraphHash, second.Graph.GraphHash)
	}
	if first.Graph.GraphSnapshotHash == second.Graph.GraphSnapshotHash {
		t.Fatalf("snapshot graph hash did not change after metadata-only change: %q", first.Graph.GraphSnapshotHash)
	}
	if first.Graph.GraphSessionID == second.Graph.GraphSessionID {
		t.Fatalf("graph session id did not change between uploads: %q", first.Graph.GraphSessionID)
	}
}

func TestConfiguredGraphExposesComputedHashes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	g := wfgraph.NewGraph()
	if err := g.AddNode(node.NewFuncNode(node.Spec{ID: "input", Name: "input"}, func(core.Context, *state.Access) error {
		return nil
	})); err != nil {
		t.Fatalf("add node: %v", err)
	}
	g.SetNodeSpec(dsl.GraphNodeSpec{ID: "input", Type: "test", Name: "input"})
	if err := g.SetEntryPoint("input"); err != nil {
		t.Fatalf("set entry point: %v", err)
	}
	if err := g.SetFinishPoint("input"); err != nil {
		t.Fatalf("set finish point: %v", err)
	}

	srv, err := New(context.Background(), Config{
		BaseDir: t.TempDir(),
		Graph:   g,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graph", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Data graphInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	if response.Data.GraphHash == "" {
		t.Fatal("configured graph hash is empty")
	}
	if response.Data.GraphSnapshotHash == "" {
		t.Fatal("configured graph snapshot hash is empty")
	}
}

func TestDeleteRunRemovesDebugRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{"id": "input", "type": "conversation_message", "config": {"content": "hello"}, "state": {"conversation": {"path": "scopes.input.conversation"}}}
			]
		}
	}`

	w := serveHTTP(engine, http.MethodPut, "/graph", graphBody)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	start := serveHTTP(engine, http.MethodPost, "/runs", `{}`)
	result := decodeRunResultResponse(t, start, http.StatusOK)

	deleted := serveHTTP(engine, http.MethodDelete, "/runs/"+result.Run.RunID+"?graph_id=debug-graph", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE /runs/:run_id status = %d, body = %s", deleted.Code, deleted.Body.String())
	}

	w = serveHTTP(engine, http.MethodGet, "/runs/"+result.Run.RunID+"?graph_id=debug-graph", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET deleted run status = %d, body = %s", w.Code, w.Body.String())
	}

	w = serveHTTP(engine, http.MethodGet, "/runs?graph_id=debug-graph", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs status = %d, body = %s", w.Code, w.Body.String())
	}
	var listResponse struct {
		Data []runtime.RunRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(listResponse.Data) != 0 {
		t.Fatalf("runs length = %d, want 0; runs = %#v", len(listResponse.Data), listResponse.Data)
	}
}

func TestDeleteCachedRunWithoutConfiguredGraph(t *testing.T) {
	gin.SetMode(gin.TestMode)

	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{"id": "input", "type": "conversation_message", "config": {"content": "hello"}, "state": {"conversation": {"path": "scopes.input.conversation"}}}
			]
		}
	}`

	w := serveHTTP(engine, http.MethodPut, "/graph", graphBody)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	start := serveHTTP(engine, http.MethodPost, "/runs", `{}`)
	result := decodeRunResultResponse(t, start, http.StatusOK)

	cacheOnlyServer, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New cache-only server error = %v", err)
	}
	cacheOnlyEngine := gin.New()
	cacheOnlyServer.RegisterRoutes(cacheOnlyEngine.Group(""))

	deleted := serveHTTP(cacheOnlyEngine, http.MethodDelete, "/runs/"+result.Run.RunID+"?graph_id=debug-graph", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE cached /runs/:run_id status = %d, body = %s", deleted.Code, deleted.Body.String())
	}

	w = serveHTTP(cacheOnlyEngine, http.MethodGet, "/runs?graph_id=debug-graph", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs status = %d, body = %s", w.Code, w.Body.String())
	}
	var listResponse struct {
		Data []runtime.RunRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(listResponse.Data) != 0 {
		t.Fatalf("runs length = %d, want 0; runs = %#v", len(listResponse.Data), listResponse.Data)
	}
}

func TestListRunsWithGraphIDAggregatesGraphSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "conversation_message",
					"config": {"content": "hello"},
					"state": {"conversation": {"path": "scopes.input.conversation"}}
				}
			]
		}
	}`

	runIDs := make(map[string]struct{})
	for i := 0; i < 2; i++ {
		if i > 0 {
			time.Sleep(time.Millisecond)
		}

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(graphBody))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
		}

		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST /runs status = %d, body = %s", w.Code, w.Body.String())
		}

		var response struct {
			Data runResult `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode run response: %v", err)
		}
		runIDs[response.Data.Run.RunID] = struct{}{}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs?graph_id=debug-graph", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data []runtime.RunRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(response.Data) != 2 {
		t.Fatalf("runs length = %d, want 2; runs = %#v", len(response.Data), response.Data)
	}
	for _, run := range response.Data {
		delete(runIDs, run.RunID)
	}
	if len(runIDs) != 0 {
		t.Fatalf("missing graph runs: %#v", runIDs)
	}
}

func TestRunInspectionResourcesExposeDebugRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "debug-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "conversation_message",
					"config": {"content": "hello"},
					"state": {"conversation": {"path": "scopes.input.conversation"}}
				}
			]
		}
	}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /runs status = %d, body = %s", w.Code, w.Body.String())
	}

	var runResponse struct {
		Data runResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &runResponse); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	runID := runResponse.Data.Run.RunID
	if runID == "" {
		t.Fatal("run id is empty")
	}

	resources := getRunResourcesForTest(t, engine, runID, "")
	if resources.Run.RunID != runID {
		t.Fatalf("run id = %q, want %q", resources.Run.RunID, runID)
	}
	if len(resources.Steps) == 0 {
		t.Fatal("steps are empty")
	}
	if len(resources.Checkpoints) == 0 {
		t.Fatal("checkpoints are empty")
	}
	if len(resources.Events) == 0 {
		t.Fatal("events are empty")
	}
	if resources.Artifacts == nil {
		t.Fatal("artifacts is nil")
	}

	invalidFormat := serveHTTP(
		engine,
		http.MethodGet,
		"/runs/"+runID+"/artifacts/missing?format=xml",
		"",
	)
	if invalidFormat.Code != http.StatusBadRequest {
		t.Fatalf("invalid artifact format status = %d, body = %s", invalidFormat.Code, invalidFormat.Body.String())
	}
}

func TestRunInterruptResponseAndResume(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := wfregistry.NewRegistry()
	if err := reg.RegisterStateModule(builtin.ProtocolsStateModuleDefinition()); err != nil {
		t.Fatalf("register state module: %v", err)
	}
	if err := reg.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:  "interrupt_once",
			Title: "Interrupt Once",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "resume", Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: func(_ *wfregistry.BuildContext, resolved wfregistry.ResolvedNodeSpec) (core.Node, error) {
			return newInterruptTestNode(resolved.Spec, resolved.State["resume"].Path), nil
		},
	}); err != nil {
		t.Fatalf("register node type: %v", err)
	}

	srv, err := New(context.Background(), Config{
		BaseDir:  t.TempDir(),
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "interrupt-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "interrupt-graph",
			"entry_point": "wait",
			"finish_point": "wait",
			"nodes": [
				{"id": "wait", "type": "interrupt_once", "state": {"resume": {"path": "shared.resume"}}}
			]
		}
	}`

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /runs status = %d, body = %s", w.Code, w.Body.String())
	}

	var startResponse struct {
		Data  runResult `json:"data"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startResponse); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResponse.Error != nil {
		t.Fatalf("start response error = %#v", startResponse.Error)
	}
	if startResponse.Data.Run.Status != runtime.RunStatusPaused {
		t.Fatalf("start status = %q, want %q", startResponse.Data.Run.Status, runtime.RunStatusPaused)
	}
	if startResponse.Data.Run.LastCheckpointID == "" {
		t.Fatal("paused run last checkpoint id is empty")
	}
	if startResponse.Data.Interrupt == nil {
		t.Fatal("paused run interrupt is nil")
	}
	if startResponse.Data.Interrupt.RunID != startResponse.Data.Run.RunID {
		t.Fatalf("interrupt run id = %q, want %q", startResponse.Data.Interrupt.RunID, startResponse.Data.Run.RunID)
	}
	if startResponse.Data.Interrupt.CheckpointID != startResponse.Data.Run.LastCheckpointID {
		t.Fatalf("interrupt checkpoint id = %q, want %q", startResponse.Data.Interrupt.CheckpointID, startResponse.Data.Run.LastCheckpointID)
	}
	if startResponse.Data.Interrupt.NodeID != "wait" {
		t.Fatalf("interrupt node id = %q, want wait", startResponse.Data.Interrupt.NodeID)
	}
	if startResponse.Data.Interrupt.Message != "waiting for resume input" {
		t.Fatalf("interrupt message = %q, want waiting for resume input", startResponse.Data.Interrupt.Message)
	}

	resources := getRunResourcesForTest(t, engine, startResponse.Data.Run.RunID, "")
	if resources.Interrupt == nil {
		t.Fatal("interrupt is nil")
	}
	if resources.Interrupt.Message != "waiting for resume input" {
		t.Fatalf("interrupt message = %q, want waiting for resume input", resources.Interrupt.Message)
	}
	var pausedPayload map[string]any
	for _, event := range resources.Events {
		if event.Type != runtime.EventRunPaused {
			continue
		}
		if err := json.Unmarshal(event.Payload, &pausedPayload); err != nil {
			t.Fatalf("decode run.paused payload: %v", err)
		}
		break
	}
	if pausedPayload == nil {
		t.Fatal("run.paused event not found")
	}
	if pausedPayload["checkpoint_id"] != startResponse.Data.Run.LastCheckpointID {
		t.Fatalf("paused checkpoint payload = %#v, want %q", pausedPayload["checkpoint_id"], startResponse.Data.Run.LastCheckpointID)
	}
	if pausedPayload["node_id"] != "wait" {
		t.Fatalf("paused node payload = %#v, want wait", pausedPayload["node_id"])
	}
	if pausedPayload["message"] != "waiting for resume input" {
		t.Fatalf("paused message payload = %#v, want waiting for resume input", pausedPayload["message"])
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runs/"+startResponse.Data.Run.RunID+"/resume", strings.NewReader(`{
		"input": {"shared": {"resume": "ok"}}
	}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /runs/:run_id/resume status = %d, body = %s", w.Code, w.Body.String())
	}

	var resumeResponse struct {
		Data  runResult `json:"data"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resumeResponse); err != nil {
		t.Fatalf("decode resume response: %v", err)
	}
	if resumeResponse.Error != nil {
		t.Fatalf("resume response error = %#v", resumeResponse.Error)
	}
	if resumeResponse.Data.Run.Status != runtime.RunStatusCompleted {
		t.Fatalf("resume status = %q, want %q", resumeResponse.Data.Run.Status, runtime.RunStatusCompleted)
	}
	if resumeResponse.Data.Interrupt != nil {
		t.Fatalf("completed run interrupt = %#v, want nil", resumeResponse.Data.Interrupt)
	}
}

func TestCancelPausedCachedRunWithoutConfiguredGraph(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := wfregistry.NewRegistry()
	if err := reg.RegisterStateModule(builtin.ProtocolsStateModuleDefinition()); err != nil {
		t.Fatalf("register state module: %v", err)
	}
	if err := reg.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:  "interrupt_once",
			Title: "Interrupt Once",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "resume", Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: func(_ *wfregistry.BuildContext, resolved wfregistry.ResolvedNodeSpec) (core.Node, error) {
			return newInterruptTestNode(resolved.Spec, resolved.State["resume"].Path), nil
		},
	}); err != nil {
		t.Fatalf("register node type: %v", err)
	}

	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{
		BaseDir:  baseDir,
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "interrupt-graph",
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "interrupt-graph",
			"entry_point": "wait",
			"finish_point": "wait",
			"nodes": [
				{"id": "wait", "type": "interrupt_once", "state": {"resume": {"path": "shared.resume"}}}
			]
		}
	}`

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /runs status = %d, body = %s", w.Code, w.Body.String())
	}

	var startResponse struct {
		Data runResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startResponse); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResponse.Data.Run.Status != runtime.RunStatusPaused {
		t.Fatalf("start status = %q, want %q", startResponse.Data.Run.Status, runtime.RunStatusPaused)
	}

	cacheOnlyServer, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New cache-only server error = %v", err)
	}
	cacheOnlyEngine := gin.New()
	cacheOnlyServer.RegisterRoutes(cacheOnlyEngine.Group(""))

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runs/"+startResponse.Data.Run.RunID+"/cancel?graph_id=interrupt-graph", nil)
	cacheOnlyEngine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /runs/:run_id/cancel status = %d, body = %s", w.Code, w.Body.String())
	}

	var cancelResponse struct {
		Data runtime.RunRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cancelResponse); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelResponse.Data.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", cancelResponse.Data.Status, runtime.RunStatusCanceled)
	}
	if cancelResponse.Data.FinishedAt == nil {
		t.Fatal("canceled cached run finished_at is nil")
	}

	resources := getRunResourcesForTest(t, cacheOnlyEngine, startResponse.Data.Run.RunID, "interrupt-graph")
	if resources.Run.Status != runtime.RunStatusCanceled {
		t.Fatalf("run status = %q, want %q", resources.Run.Status, runtime.RunStatusCanceled)
	}
	if !hasRuntimeEvent(resources.Events, runtime.EventRunCanceled) {
		t.Fatal("events missing run.canceled")
	}
}

func TestPauseRunBlocksUntilPausedStatusAndPausedRunCanBeCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	graph := newRunControlTestGraph(t, started, release, false)
	srv, err := New(context.Background(), Config{
		Graph:   graph,
		BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	startDone := serveHTTPAsync(engine, http.MethodPost, "/runs", `{}`)
	waitForSignal(t, started, "run node start")
	runID := waitForServerRunID(t, srv.Runner())

	pauseDone := serveHTTPAsync(engine, http.MethodPost, "/runs/"+runID+"/pause", "")
	assertNoHTTPResponse(t, pauseDone, "pause")
	close(release)

	pauseResponse := waitForHTTPResponse(t, pauseDone, "pause")
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}

	startResponse := waitForHTTPResponse(t, startDone, "start")
	startResult := decodeRunResultResponse(t, startResponse, http.StatusOK)
	if startResult.Run.Status != runtime.RunStatusPaused {
		t.Fatalf("start response status = %q, want %q", startResult.Run.Status, runtime.RunStatusPaused)
	}

	cancelResponse := serveHTTP(engine, http.MethodPost, "/runs/"+runID+"/cancel", "")
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel paused response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}
}

func TestPauseRunCancelsActiveContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	graph := newRunControlTestGraph(t, started, release, true)
	srv, err := New(context.Background(), Config{
		Graph:   graph,
		BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	startDone := serveHTTPAsync(engine, http.MethodPost, "/runs", `{}`)
	waitForSignal(t, started, "run node start")
	runID := waitForServerRunID(t, srv.Runner())

	pauseResponse := serveHTTP(engine, http.MethodPost, "/runs/"+runID+"/pause", "")
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}

	startResponse := waitForHTTPResponse(t, startDone, "start")
	startResult := decodeRunResultResponse(t, startResponse, http.StatusOK)
	if startResult.Run.Status != runtime.RunStatusPaused {
		t.Fatalf("start response status = %q, want %q", startResult.Run.Status, runtime.RunStatusPaused)
	}
}

func TestResumeRunAfterActivePauseCompletes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	graph := newRunControlTestGraph(t, started, release, true)
	srv, err := New(context.Background(), Config{
		Graph:   graph,
		BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	startDone := serveHTTPAsync(engine, http.MethodPost, "/runs", `{}`)
	waitForSignal(t, started, "run node start")
	runID := waitForServerRunID(t, srv.Runner())

	pauseResponse := serveHTTP(engine, http.MethodPost, "/runs/"+runID+"/pause", "")
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}
	waitForHTTPResponse(t, startDone, "start")

	resumeDone := serveHTTPAsync(engine, http.MethodPost, "/runs/"+runID+"/resume", `{}`)
	assertNoHTTPResponse(t, resumeDone, "resume")
	close(release)

	resumeResponse := waitForHTTPResponse(t, resumeDone, "resume")
	resumeResult := decodeRunResultResponse(t, resumeResponse, http.StatusOK)
	if resumeResult.Run.Status != runtime.RunStatusCompleted {
		t.Fatalf("resume response status = %q, want %q", resumeResult.Run.Status, runtime.RunStatusCompleted)
	}
}

func TestCancelRunBlocksUntilCanceledStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	graph := newRunControlTestGraph(t, started, release, false)
	srv, err := New(context.Background(), Config{
		Graph:   graph,
		BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	startDone := serveHTTPAsync(engine, http.MethodPost, "/runs", `{}`)
	waitForSignal(t, started, "run node start")
	runID := waitForServerRunID(t, srv.Runner())

	cancelDone := serveHTTPAsync(engine, http.MethodPost, "/runs/"+runID+"/cancel", "")
	assertNoHTTPResponse(t, cancelDone, "cancel")
	close(release)

	cancelResponse := waitForHTTPResponse(t, cancelDone, "cancel")
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}

	startResponse := waitForHTTPResponse(t, startDone, "start")
	startResult := decodeRunResultResponse(t, startResponse, http.StatusOK)
	if startResult.Run.Status != runtime.RunStatusCanceled {
		t.Fatalf("start response status = %q, want %q", startResult.Run.Status, runtime.RunStatusCanceled)
	}
}

func TestCancelRunCancelsActiveContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	graph := newRunControlTestGraph(t, started, release, true)
	srv, err := New(context.Background(), Config{
		Graph:   graph,
		BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	startDone := serveHTTPAsync(engine, http.MethodPost, "/runs", `{}`)
	waitForSignal(t, started, "run node start")
	runID := waitForServerRunID(t, srv.Runner())

	cancelResponse := serveHTTP(engine, http.MethodPost, "/runs/"+runID+"/cancel", "")
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}

	startResponse := waitForHTTPResponse(t, startDone, "start")
	startResult := decodeRunResultResponse(t, startResponse, http.StatusOK)
	if startResult.Run.Status != runtime.RunStatusCanceled {
		t.Fatalf("start response status = %q, want %q", startResult.Run.Status, runtime.RunStatusCanceled)
	}
}

func TestCancelRunUsesTriggerSessionRunner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	graph := newRunControlTestGraph(t, started, release, true)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runner := newDefaultRunner(graph, Config{
		GraphID:        "trigger-graph",
		GraphVersion:   "2.0",
		GraphSessionID: "trigger-session",
	}, t.TempDir())
	srv.runtime.cacheTriggerSession("trigger-graph", graphRuntimeSession{
		graph:       graph,
		runner:      runner,
		baseContext: context.Background(),
	})
	if srv.currentRunner() != nil {
		t.Fatal("current runner is configured")
	}

	run, done, err := runner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "trigger run node start")

	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	cancelResponse := serveHTTP(
		engine,
		http.MethodPost,
		"/runs/"+run.RunID+"/cancel?graph_id=trigger-graph",
		"",
	)
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}
	waitForSignal(t, done, "trigger run completion")
}

func TestGraphInitialStateRequirementsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := wfregistry.NewRegistry()
	if err := reg.RegisterStateModule(builtin.ProtocolsStateModuleDefinition()); err != nil {
		t.Fatalf("register state module: %v", err)
	}
	if err := reg.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:       "requires_input",
			Title:      "Requires Input",
			StatePorts: []dsl.StatePortDefinition{{Name: "input", Description: "User request input.", Required: true, Schema: dsl.JSONSchema{"type": "string"}, Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace}},
		},
		Build: func(_ *wfregistry.BuildContext, resolved wfregistry.ResolvedNodeSpec) (core.Node, error) {
			return newContractTestNode(resolved.Spec), nil
		},
	}); err != nil {
		t.Fatalf("register node type: %v", err)
	}

	srv, err := New(context.Background(), Config{
		BaseDir:  t.TempDir(),
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"definition": {
			"version": "2.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": "requires-input",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "requires_input",
					"state": {"input": {"path": "shared.request.input"}}
				}
			]
		}
	}`

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graph/initial-state-requirements", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph/initial-state-requirements status = %d, body = %s", w.Code, w.Body.String())
	}
	assertInitialStateRequirementResponse(t, w.Body.Bytes())

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph status = %d, body = %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/graph/initial-state-requirements", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /graph/initial-state-requirements status = %d, body = %s", w.Code, w.Body.String())
	}
	assertInitialStateRequirementResponse(t, w.Body.Bytes())
}

func assertInitialStateRequirementResponse(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Data  core.InitialStateRequirements `json:"data"`
		Error *apiError                     `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode requirements response: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("response error = %#v", response.Error)
	}
	if len(response.Data.Required) != 1 {
		t.Fatalf("required = %#v, want one item", response.Data.Required)
	}
	required := response.Data.Required[0]
	if required.Path != "shared.request.input" {
		t.Fatalf("required path = %q, want shared.request.input", required.Path)
	}
	if len(required.Nodes) != 1 || required.Nodes[0] != "input" {
		t.Fatalf("required nodes = %#v, want [input]", required.Nodes)
	}
	if required.Type != "string" {
		t.Fatalf("required type = %q, want string", required.Type)
	}
	if len(response.Data.ProvidedByUpstream) != 0 {
		t.Fatalf("provided_by_upstream = %#v, want empty", response.Data.ProvidedByUpstream)
	}
	if len(response.Data.Unresolved) != 0 {
		t.Fatalf("unresolved = %#v, want empty", response.Data.Unresolved)
	}
}

func serveHTTPAsync(engine *gin.Engine, method string, path string, body string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	go func() {
		defer cancel()
		done <- serveHTTPWithContext(ctx, engine, method, path, body)
	}()
	return done
}

func serveHTTP(engine *gin.Engine, method string, path string, body string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return serveHTTPWithContext(ctx, engine, method, path, body)
}

func serveHTTPWithContext(ctx context.Context, engine *gin.Engine, method string, path string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func putGraphForHashTest(t *testing.T, engine *gin.Engine, body string) graphLoadResponse {
	t.Helper()
	return requestGraphForHashTest(t, engine, http.MethodPut, "/graph", body)
}

func requestGraphForHashTest(t *testing.T, engine *gin.Engine, method, path string, body string) graphLoadResponse {
	t.Helper()
	w := serveHTTP(engine, method, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, body = %s", method, path, w.Code, w.Body.String())
	}
	var decoded struct {
		Data  graphLoadResponse `json:"data"`
		Error *apiError         `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %#v", decoded.Error)
	}
	return decoded.Data
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func assertNoHTTPResponse(t *testing.T, done <-chan *httptest.ResponseRecorder, name string) {
	t.Helper()
	select {
	case response := <-done:
		t.Fatalf("%s returned before run reached target status: status=%d body=%s", name, response.Code, response.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
}

func waitForHTTPResponse(t *testing.T, done <-chan *httptest.ResponseRecorder, name string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-done:
		return response
	case <-time.After(4 * time.Second):
		t.Fatalf("timed out waiting for %s response", name)
		return nil
	}
}

func waitForServerRunID(t *testing.T, runner *runtime.GraphRunner) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		runs, err := runner.ListRuns(context.Background(), runtime.RunFilter{})
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) > 0 && runs[0].RunID != "" {
			return runs[0].RunID
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for run id")
		case <-ticker.C:
		}
	}
}

func hasRuntimeEvent(events []runtime.Event, eventType runtime.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

type runResourceSet struct {
	Run         runtime.RunRecord
	Steps       []runtime.StepRecord
	Checkpoints []runtime.CheckpointRecord
	Events      []runtime.Event
	Artifacts   []state.ArtifactRef
	Interrupt   *runInterrupt
}

func decodeEventPageResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) runtime.EventPage {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, wantStatus, response.Body.String())
	}
	var envelope struct {
		Data runtime.EventPage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode event page response: %v", err)
	}
	return envelope.Data
}

func getRunResourcesForTest(t *testing.T, engine *gin.Engine, runID, graphID string) runResourceSet {
	t.Helper()
	query := ""
	if graphID != "" {
		query = "?graph_id=" + graphID
	}
	basePath := "/runs/" + runID
	resources := runResourceSet{}
	eventPage := runtime.EventPage{}
	requests := []struct {
		path   string
		target any
	}{
		{path: basePath + query, target: &resources.Run},
		{path: basePath + "/steps" + query, target: &resources.Steps},
		{path: basePath + "/checkpoints" + query, target: &resources.Checkpoints},
		{path: basePath + "/events" + query, target: &eventPage},
		{path: basePath + "/artifacts" + query, target: &resources.Artifacts},
		{path: basePath + "/interrupt" + query, target: &resources.Interrupt},
	}
	for _, request := range requests {
		response := serveHTTP(engine, http.MethodGet, request.path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", request.path, response.Code, response.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode GET %s response: %v", request.path, err)
		}
		if err := json.Unmarshal(envelope.Data, request.target); err != nil {
			t.Fatalf("decode GET %s data: %v", request.path, err)
		}
	}
	resources.Events = eventPage.Items
	return resources
}

func decodeRunRecordResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) runtime.RunRecord {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data  runtime.RunRecord `json:"data"`
		Error *apiError         `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode run record response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %#v", decoded.Error)
	}
	return decoded.Data
}

func decodeRunResultResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) runResult {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data  runResult `json:"data"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode run result response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %#v", decoded.Error)
	}
	return decoded.Data
}
