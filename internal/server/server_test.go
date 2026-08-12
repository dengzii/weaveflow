package server

import (
	"context"
	"encoding/json"
	"errors"
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
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/graphs/graph/sessions", strings.NewReader(`{
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
		`{"graph_id": "legacy", "definition": {}}`,
		`{"version":"2.0","state_modules":[],"nodes":[]}`,
	}
	for _, body := range tests {
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginContext.Request = httptest.NewRequest(http.MethodPost, "/graphs/graph/sessions", strings.NewReader(body))
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
		ginContext.Request = httptest.NewRequest(http.MethodPost, "/graphs/graph/sessions", strings.NewReader(body))
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
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/graphs/graph/sessions/session/runs", strings.NewReader(test.body))
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
	return &core.NodeInterrupt{NodeID: n.ID(), Value: "waiting for resume input"}
}

func newRunControlTestGraph(t *testing.T, started chan<- struct{}, release <-chan struct{}, respectContext bool) *wfgraph.Graph {
	t.Helper()
	graph := wfgraph.NewGraph(nil)
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
	if err := graph.SetNodeSpec(dsl.GraphNodeSpec{ID: "work", Type: "test", Name: "work"}); err != nil {
		t.Fatalf("set work node spec: %v", err)
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
		]
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
		]
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
	_, uploadBody = graphSessionRequestBodyForTest(t, uploadBody)

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
			response := serveHTTP(engine, http.MethodPost, "/graphs/concurrent-settings/sessions", uploadBody)
			if response.Code != http.StatusOK {
				t.Errorf("POST graph session status = %d, body = %s", response.Code, response.Body.String())
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

	settings := graphRuntimeSettingsFromContext(context.Background())
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

	settings := graphRuntimeSettingsFromContext(ctx)
	if got := settings.Environment["TAVILY_API_KEY"]; got != "context-key" {
		t.Fatalf("TAVILY_API_KEY = %q, want context-key", got)
	}
}

func TestNewPreservesExistingSinkAndBroadcasts(t *testing.T) {
	sink := &recordingEventSink{}
	srv, runner := mustNewEventTestServer(t, sink, 1)

	subscription := srv.EventHub().Subscribe(eventFilter{
		RunID: "run-1",
		Types: map[runtime.EventType]struct{}{
			runtime.EventNodeStarted: {},
		},
	}, "")
	defer subscription.Unsubscribe()

	event := runtime.Event{
		ID:        "event-1",
		RunID:     "run-1",
		NodeID:    "node-1",
		Type:      runtime.EventNodeStarted,
		Timestamp: time.Now(),
	}
	if err := runner.EventSink().Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	select {
	case got := <-subscription.Events:
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
	srv, runner := mustNewEventTestServer(t, sink, 0)

	event := runtime.Event{
		ID:        "event-1",
		RunID:     "run-1",
		Type:      runtime.EventRunStarted,
		Timestamp: time.Now(),
	}
	if err := runner.EventSink().Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	engine := gin.New()
	srv.RegisterRoutes(engine.Group("/debug"))

	req := httptest.NewRequest(http.MethodGet, "/debug/graphs/graph/runs/run-1/events", nil)
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
	srv, _ := mustNewEventTestServer(t, sink, 0)
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

	basePath := "/debug/graphs/graph/runs/run-1/events"
	defaultPage := decodeEventPageResponse(t, serveHTTP(engine, http.MethodGet, basePath, ""), http.StatusOK)
	if len(defaultPage.Items) != defaultEventPageLimit {
		t.Fatalf("default page item count = %d, want %d", len(defaultPage.Items), defaultEventPageLimit)
	}
	if defaultPage.Items[0].ID != "event-500" || defaultPage.Items[len(defaultPage.Items)-1].ID != "event-001" {
		t.Fatalf("default page bounds = %q..%q", defaultPage.Items[0].ID, defaultPage.Items[len(defaultPage.Items)-1].ID)
	}
	if defaultPage.NextCursor == "" {
		t.Fatal("default page next cursor is empty")
	}

	first := decodeEventPageResponse(t, serveHTTP(engine, http.MethodGet, basePath+"?limit=2", ""), http.StatusOK)
	if len(first.Items) != 2 || first.Items[0].ID != "event-500" || first.Items[1].ID != "event-499" {
		t.Fatalf("first page = %#v", first)
	}
	second := decodeEventPageResponse(
		t,
		serveHTTP(engine, http.MethodGet, basePath+"?limit=2&cursor="+first.NextCursor, ""),
		http.StatusOK,
	)
	if len(second.Items) != 2 || second.Items[0].ID != "event-498" || second.Items[1].ID != "event-497" {
		t.Fatalf("second page = %#v", second)
	}

	maximum := decodeEventPageResponse(t, serveHTTP(engine, http.MethodGet, basePath+"?limit=2000", ""), http.StatusOK)
	if len(maximum.Items) != 501 {
		t.Fatalf("maximum page item count = %d, want 501", len(maximum.Items))
	}
	for _, path := range []string{
		basePath + "?limit=0",
		basePath + "?limit=2001",
		basePath + "?cursor=invalid",
	} {
		response := serveHTTP(engine, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400; body = %s", path, response.Code, response.Body.String())
		}
	}
}

func TestInvalidRunStatusReturnsStructuredError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	response := serveHTTP(engine, http.MethodGet, "/graphs/graph/runs?status=unknown", "")
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

func TestListQueriesRejectEmptyValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	paths := []string{
		"/graphs/graph/runs?status=",
		"/graphs/graph/runs?status=running,,paused",
		"/graphs/graph/events/stream?type=",
		"/graphs/graph/events/stream?type=run.started,,run.finished",
	}
	for _, path := range paths {
		response := serveHTTP(engine, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400; body = %s", path, response.Code, response.Body.String())
		}
	}
}

func TestRunRequestsValidateBodyBeforeRunnerAvailability(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	requests := []string{
		"/graphs/graph/sessions/safe-session/runs",
		"/graphs/graph/runs/safe-run/resume",
	}
	for _, path := range requests {
		response := serveHTTP(engine, http.MethodPost, path, `{"unknown":true}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST %s status = %d, want 400; body = %s", path, response.Code, response.Body.String())
		}
	}
}

func TestRunRecordPathParametersRejectUnsafeIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	paths := []string{
		"/graphs/graph/runs/%2e%2e%5coutside/inspection",
		"/graphs/graph/runs/%20safe-run/inspection",
		"/graphs/graph/runs/safe-run%20/inspection",
		"/graphs/graph/runs/safe-run/checkpoints/%2e%2e%5coutside",
		"/graphs/graph/runs/safe-run/artifacts/%2e%2e%5coutside",
	}
	for _, path := range paths {
		response := serveHTTP(engine, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400; body = %s", path, response.Code, response.Body.String())
		}
	}
}

func TestCreateGraphSessionConfiguresRunnerForDebugRun(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	graphBody := `{
		"graph_id": "debug-graph",
		"settings": {"environment": {}, "models": []},
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

	graphResponse := putGraphForHashTest(t, engine, graphBody)
	if graphResponse.Graph.GraphHash == "" {
		t.Fatal("graph response graph_hash is empty")
	}
	if graphResponse.Graph.GraphSnapshotHash == "" {
		t.Fatal("graph response graph_snapshot_hash is empty")
	}
	if graphResponse.Graph.GraphSessionID == "" {
		t.Fatal("graph response graph_session_id is empty")
	}
	if graphResponse.RunnerBaseDir == "" {
		t.Fatal("graph response runner_base_dir is empty")
	}
	if graphResponse.Graph.GraphSessionID != filepath.Base(graphResponse.RunnerBaseDir) {
		t.Fatalf("graph session id = %q, want base dir %q", graphResponse.Graph.GraphSessionID, filepath.Base(graphResponse.RunnerBaseDir))
	}

	definitionPath := filepath.Join(graphResponse.RunnerBaseDir, "definition.json")
	if _, err := os.Stat(definitionPath); err != nil {
		t.Fatalf("stat graph definition snapshot: %v", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(graphResponse.RunnerBaseDir, "graph.json"))
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
	if manifest.GraphHash != graphResponse.Graph.GraphHash {
		t.Fatalf("manifest graph hash = %q, want %q", manifest.GraphHash, graphResponse.Graph.GraphHash)
	}
	if manifest.GraphSnapshotHash != graphResponse.Graph.GraphSnapshotHash {
		t.Fatalf("manifest graph snapshot hash = %q, want %q", manifest.GraphSnapshotHash, graphResponse.Graph.GraphSnapshotHash)
	}
	if manifest.GraphSessionID != graphResponse.Graph.GraphSessionID {
		t.Fatalf("manifest graph session id = %q, want %q", manifest.GraphSessionID, graphResponse.Graph.GraphSessionID)
	}
	if manifest.DefinitionPath != "definition.json" {
		t.Fatalf("manifest definition path = %q, want definition.json", manifest.DefinitionPath)
	}

	currentGraphResponse := decodeGraphDetailResponse(t, serveHTTP(engine, http.MethodGet, "/graphs/debug-graph", ""), http.StatusOK)
	if currentGraphResponse.Graph.GraphHash != graphResponse.Graph.GraphHash {
		t.Fatalf("current graph hash = %q, want %q", currentGraphResponse.Graph.GraphHash, graphResponse.Graph.GraphHash)
	}
	if currentGraphResponse.Graph.GraphSnapshotHash != graphResponse.Graph.GraphSnapshotHash {
		t.Fatalf("current graph snapshot hash = %q, want %q", currentGraphResponse.Graph.GraphSnapshotHash, graphResponse.Graph.GraphSnapshotHash)
	}
	if currentGraphResponse.Graph.GraphSessionID != graphResponse.Graph.GraphSessionID {
		t.Fatalf("current graph session id = %q, want %q", currentGraphResponse.Graph.GraphSessionID, graphResponse.Graph.GraphSessionID)
	}

	run := decodeRunRecordResponse(t, serveHTTP(
		engine,
		http.MethodPost,
		"/graphs/debug-graph/sessions/"+graphResponse.Graph.GraphSessionID+"/runs",
		`{}`,
	), http.StatusAccepted)
	if run.GraphID != "debug-graph" {
		t.Fatalf("run graph id = %q, want debug-graph", run.GraphID)
	}
	if run.GraphHash != graphResponse.Graph.GraphHash {
		t.Fatalf("run graph hash = %q, want %q", run.GraphHash, graphResponse.Graph.GraphHash)
	}
	if run.GraphSnapshotHash != graphResponse.Graph.GraphSnapshotHash {
		t.Fatalf("run graph snapshot hash = %q, want %q", run.GraphSnapshotHash, graphResponse.Graph.GraphSnapshotHash)
	}
	if run.GraphSessionID != graphResponse.Graph.GraphSessionID {
		t.Fatalf("run graph session id = %q, want %q", run.GraphSessionID, graphResponse.Graph.GraphSessionID)
	}
	completed := waitForRunTerminalStatus(t, srv.runtime.session("debug-graph", graphResponse.Graph.GraphSessionID).runner, run.RunID)
	if completed.Status != runtime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", completed.Status, runtime.RunStatusCompleted)
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
		"settings": {"environment": {}, "models": []},
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
		"settings": {"environment": {}, "models": []},
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

	g := wfgraph.NewGraph(nil)
	if err := g.AddNode(node.NewFuncNode(node.Spec{ID: "input", Name: "input"}, func(core.Context, *state.Access) error {
		return nil
	})); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetNodeSpec(dsl.GraphNodeSpec{ID: "input", Type: "test", Name: "input"}); err != nil {
		t.Fatalf("set node spec: %v", err)
	}
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
	if srv.Runner().GraphHash() == "" {
		t.Fatal("configured graph hash is empty")
	}
	if srv.Runner().GraphSnapshotHash() == "" {
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
		"settings": {"environment": {}, "models": []},
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

	uploaded := putGraphForHashTest(t, engine, graphBody)
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	waitForRunTerminalStatus(t, srv.runtime.session("debug-graph", uploaded.Graph.GraphSessionID).runner, started.RunID)

	deleted := serveHTTP(engine, http.MethodDelete, "/graphs/debug-graph/runs/"+started.RunID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE /runs/:run_id status = %d, body = %s", deleted.Code, deleted.Body.String())
	}

	w := serveHTTP(engine, http.MethodGet, "/graphs/debug-graph/runs/"+started.RunID+"/inspection", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET deleted run status = %d, body = %s", w.Code, w.Body.String())
	}

	w = serveHTTP(engine, http.MethodGet, "/graphs/debug-graph/runs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs status = %d, body = %s", w.Code, w.Body.String())
	}
	var listResponse struct {
		Data runListPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(listResponse.Data.Items) != 0 {
		t.Fatalf("runs length = %d, want 0; runs = %#v", len(listResponse.Data.Items), listResponse.Data.Items)
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
		"settings": {"environment": {}, "models": []},
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

	uploaded := putGraphForHashTest(t, engine, graphBody)
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	waitForRunTerminalStatus(t, srv.runtime.session("debug-graph", uploaded.Graph.GraphSessionID).runner, started.RunID)

	cacheOnlyServer, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New cache-only server error = %v", err)
	}
	cacheOnlyEngine := gin.New()
	cacheOnlyServer.RegisterRoutes(cacheOnlyEngine.Group(""))

	deleted := serveHTTP(cacheOnlyEngine, http.MethodDelete, "/graphs/debug-graph/runs/"+started.RunID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE cached /runs/:run_id status = %d, body = %s", deleted.Code, deleted.Body.String())
	}

	w := serveHTTP(cacheOnlyEngine, http.MethodGet, "/graphs/debug-graph/runs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs status = %d, body = %s", w.Code, w.Body.String())
	}
	var listResponse struct {
		Data runListPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(listResponse.Data.Items) != 0 {
		t.Fatalf("runs length = %d, want 0; runs = %#v", len(listResponse.Data.Items), listResponse.Data.Items)
	}
}

func TestDeleteCachedActiveRunWithoutConfiguredGraphIsRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)

	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("debug-graph", "v1", "hello"))
	active := startGraphRunForTest(t, engine, uploaded, `{}`)
	active = waitForRunTerminalStatus(t, srv.runtime.session("debug-graph", uploaded.Graph.GraphSessionID).runner, active.RunID)
	active.Status = runtime.RunStatusRunning
	active.FinishedAt = nil
	if err := runtime.NewFileExecutionStore(filepath.Join(srv.graphHistoryBaseDir(uploaded.Graph.ID), "execution")).UpdateRun(context.Background(), active); err != nil {
		t.Fatalf("mark cached run active: %v", err)
	}

	cacheOnlyServer, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New cache-only server error = %v", err)
	}
	cacheOnlyEngine := gin.New()
	cacheOnlyServer.RegisterRoutes(cacheOnlyEngine.Group(""))

	deleted := serveHTTP(cacheOnlyEngine, http.MethodDelete, "/graphs/debug-graph/runs/"+active.RunID, "")
	if deleted.Code != http.StatusConflict {
		t.Fatalf("DELETE cached active run status = %d, want 409; body = %s", deleted.Code, deleted.Body.String())
	}
	remaining := serveHTTP(cacheOnlyEngine, http.MethodGet, "/graphs/debug-graph/runs/"+active.RunID+"/inspection", "")
	if remaining.Code != http.StatusOK {
		t.Fatalf("GET cached active run status = %d, want 200; body = %s", remaining.Code, remaining.Body.String())
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
		"settings": {"environment": {}, "models": []},
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

		uploaded := putGraphForHashTest(t, engine, graphBody)
		run := startGraphRunForTest(t, engine, uploaded, `{}`)
		waitForRunTerminalStatus(t, srv.runtime.session("debug-graph", uploaded.Graph.GraphSessionID).runner, run.RunID)
		runIDs[run.RunID] = struct{}{}
	}

	w := serveHTTP(engine, http.MethodGet, "/graphs/debug-graph/runs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data runListPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(response.Data.Items) != 2 {
		t.Fatalf("runs length = %d, want 2; runs = %#v", len(response.Data.Items), response.Data.Items)
	}
	for _, run := range response.Data.Items {
		delete(runIDs, run.RunID)
	}
	if len(runIDs) != 0 {
		t.Fatalf("missing graph runs: %#v", runIDs)
	}
}

func TestRunResourceReadersKeepSessionOwnership(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	runID := "shared-run"
	artifactID := "second-artifact"

	newSessionReader := func(name string) *graphCacheReader {
		sessionDir := filepath.Join(baseDir, name)
		return &graphCacheReader{
			executionStores:  []*runtime.FileExecutionStore{runtime.NewFileExecutionStore(filepath.Join(sessionDir, "execution"))},
			checkpointStores: []*runtime.FileCheckpointStore{runtime.NewFileCheckpointStore(filepath.Join(sessionDir, "checkpoints"))},
			artifactStores:   []*runtime.FileArtifactStore{runtime.NewFileArtifactStore(filepath.Join(sessionDir, "artifacts"))},
			eventSinks:       []*runtime.FileEventSink{runtime.NewFileEventSink(filepath.Join(sessionDir, "events"))},
			codec:            state.NewJSONStateCodec(""),
		}
	}
	first := newSessionReader("first")
	second := newSessionReader("second")
	for _, reader := range []*graphCacheReader{first, second} {
		if err := reader.executionStores[0].CreateRun(ctx, runtime.RunRecord{RunID: runID}); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	if err := second.executionStores[0].AppendStep(ctx, runtime.StepRecord{RunID: runID, StepID: "second-step"}); err != nil {
		t.Fatalf("append second-session step: %v", err)
	}
	if err := second.checkpointStores[0].Save(ctx, runtime.CheckpointRecord{
		RunID: runID, CheckpointID: "second-checkpoint",
	}, nil); err != nil {
		t.Fatalf("save second-session checkpoint: %v", err)
	}
	if err := second.eventSinks[0].Publish(ctx, runtime.Event{
		RunID: runID, ID: "second-event", Type: runtime.EventRunCreated,
	}); err != nil {
		t.Fatalf("publish second-session event: %v", err)
	}
	if _, err := second.artifactStores[0].Save(ctx, runtime.Artifact{
		RunID: runID, ID: artifactID, Data: []byte("second"),
	}); err != nil {
		t.Fatalf("save second-session artifact: %v", err)
	}

	aggregated := &graphCacheReader{
		executionStores:  []*runtime.FileExecutionStore{first.executionStores[0], second.executionStores[0]},
		checkpointStores: []*runtime.FileCheckpointStore{first.checkpointStores[0], second.checkpointStores[0]},
		artifactStores:   []*runtime.FileArtifactStore{first.artifactStores[0], second.artifactStores[0]},
		eventSinks:       []*runtime.FileEventSink{first.eventSinks[0], second.eventSinks[0]},
		codec:            state.NewJSONStateCodec(""),
	}
	readers := map[string]runReader{
		"graph cache": aggregated,
		"combined":    &combinedRunReader{readers: []runReader{first, second}},
	}
	for name, reader := range readers {
		t.Run(name, func(t *testing.T) {
			steps, err := reader.ListSteps(ctx, runID)
			if err != nil || len(steps) != 0 {
				t.Fatalf("ListSteps() = %#v, %v; want empty owner-session result", steps, err)
			}
			checkpoints, err := reader.ListCheckpoints(ctx, runID)
			if err != nil || len(checkpoints) != 0 {
				t.Fatalf("ListCheckpoints() = %#v, %v; want empty owner-session result", checkpoints, err)
			}
			events, err := reader.ListEvents(runID)
			if err != nil || len(events) != 0 {
				t.Fatalf("ListEvents() = %#v, %v; want empty owner-session result", events, err)
			}
			artifacts, err := reader.ListArtifacts(ctx, runID)
			if err != nil || len(artifacts) != 0 {
				t.Fatalf("ListArtifacts() = %#v, %v; want empty owner-session result", artifacts, err)
			}
			if _, err := reader.LoadArtifact(ctx, state.ArtifactRef{RunID: runID, ID: artifactID}); !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
				t.Fatalf("LoadArtifact() error = %v, want record not found in owner session", err)
			}
		})
	}
}

func TestRunReadersUseRunIDAsTimestampTieBreaker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	newStore := func(runID string) *runtime.FileExecutionStore {
		store := runtime.NewFileExecutionStore(filepath.Join(t.TempDir(), "execution"))
		if err := store.CreateRun(ctx, runtime.RunRecord{RunID: runID}); err != nil {
			t.Fatalf("create run %q: %v", runID, err)
		}
		return store
	}

	first := &graphCacheReader{executionStores: []*runtime.FileExecutionStore{newStore("run-b")}}
	second := &graphCacheReader{executionStores: []*runtime.FileExecutionStore{newStore("run-a")}}
	readers := map[string]runReader{
		"graph cache": &graphCacheReader{executionStores: []*runtime.FileExecutionStore{
			first.executionStores[0], second.executionStores[0],
		}},
		"combined": &combinedRunReader{readers: []runReader{first, second}},
	}
	for name, reader := range readers {
		t.Run(name, func(t *testing.T) {
			runs, err := reader.ListRuns(ctx, runtime.RunFilter{})
			if err != nil {
				t.Fatalf("ListRuns() error = %v", err)
			}
			if len(runs) != 2 || runs[0].RunID != "run-a" || runs[1].RunID != "run-b" {
				t.Fatalf("ListRuns() = %#v, want run ID order for equal timestamps", runs)
			}
		})
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
		"settings": {"environment": {}, "models": []},
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
	uploaded := putGraphForHashTest(t, engine, graphBody)
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	waitForRunTerminalStatus(t, srv.runtime.session("debug-graph", uploaded.Graph.GraphSessionID).runner, started.RunID)
	runID := started.RunID
	if runID == "" {
		t.Fatal("run id is empty")
	}

	resources := getRunResourcesForTest(t, engine, runID, "debug-graph")
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
		"/graphs/debug-graph/runs/"+runID+"/artifacts/missing?format=xml",
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
		"settings": {"environment": {}, "models": []},
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

	uploaded := putGraphForHashTest(t, engine, graphBody)
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	pausedRun := waitForRunTerminalStatus(t, srv.runtime.session("interrupt-graph", uploaded.Graph.GraphSessionID).runner, started.RunID)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("start status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}
	if pausedRun.LastCheckpointID == "" {
		t.Fatal("paused run last checkpoint id is empty")
	}

	resources := getRunResourcesForTest(t, engine, pausedRun.RunID, "interrupt-graph")
	if resources.Interrupt == nil {
		t.Fatal("paused run interrupt is nil")
	}
	if resources.Interrupt.RunID != pausedRun.RunID {
		t.Fatalf("interrupt run id = %q, want %q", resources.Interrupt.RunID, pausedRun.RunID)
	}
	if resources.Interrupt.CheckpointID != pausedRun.LastCheckpointID {
		t.Fatalf("interrupt checkpoint id = %q, want %q", resources.Interrupt.CheckpointID, pausedRun.LastCheckpointID)
	}
	if resources.Interrupt.NodeID != "wait" {
		t.Fatalf("interrupt node id = %q, want wait", resources.Interrupt.NodeID)
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
	if pausedPayload["checkpoint_id"] != pausedRun.LastCheckpointID {
		t.Fatalf("paused checkpoint payload = %#v, want %q", pausedPayload["checkpoint_id"], pausedRun.LastCheckpointID)
	}
	if pausedPayload["node_id"] != "wait" {
		t.Fatalf("paused node payload = %#v, want wait", pausedPayload["node_id"])
	}
	if pausedPayload["message"] != "waiting for resume input" {
		t.Fatalf("paused message payload = %#v, want waiting for resume input", pausedPayload["message"])
	}

	w := serveHTTP(engine, http.MethodPost, "/graphs/interrupt-graph/runs/"+pausedRun.RunID+"/resume", `{
		"input": {"shared": {"resume": "ok"}}
	}`)
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
		"settings": {"environment": {}, "models": []},
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

	uploaded := putGraphForHashTest(t, engine, graphBody)
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	pausedRun := waitForRunTerminalStatus(t, srv.runtime.session("interrupt-graph", uploaded.Graph.GraphSessionID).runner, started.RunID)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("start status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}

	cacheOnlyServer, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New cache-only server error = %v", err)
	}
	cacheOnlyEngine := gin.New()
	cacheOnlyServer.RegisterRoutes(cacheOnlyEngine.Group(""))

	w := serveHTTP(cacheOnlyEngine, http.MethodPost, "/graphs/interrupt-graph/runs/"+pausedRun.RunID+"/cancel", "")
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

	resources := getRunResourcesForTest(t, cacheOnlyEngine, pausedRun.RunID, "interrupt-graph")
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

	run, runDone, err := srv.Runner().StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "run node start")
	runID := run.RunID

	pauseDone := serveHTTPAsync(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/pause", "")
	assertNoHTTPResponse(t, pauseDone, "pause")
	close(release)

	pauseResponse := waitForHTTPResponse(t, pauseDone, "pause")
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}

	waitForSignal(t, runDone, "run pause")
	storedRun, err := srv.Runner().GetRun(context.Background(), runID)
	if err != nil || storedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("stored run = %#v, error = %v; want paused", storedRun, err)
	}

	cancelResponse := serveHTTP(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/cancel", "")
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

	run, runDone, err := srv.Runner().StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "run node start")
	runID := run.RunID

	pauseResponse := serveHTTP(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/pause", "")
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}

	waitForSignal(t, runDone, "run pause")
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

	run, runDone, err := srv.Runner().StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "run node start")
	runID := run.RunID

	pauseResponse := serveHTTP(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/pause", "")
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}
	waitForSignal(t, runDone, "run pause")

	resumeDone := serveHTTPAsync(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/resume", `{}`)
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

	run, runDone, err := srv.Runner().StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "run node start")
	runID := run.RunID

	cancelDone := serveHTTPAsync(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/cancel", "")
	assertNoHTTPResponse(t, cancelDone, "cancel")
	close(release)

	cancelResponse := waitForHTTPResponse(t, cancelDone, "cancel")
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}

	waitForSignal(t, runDone, "run cancel")
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

	run, runDone, err := srv.Runner().StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "run node start")
	runID := run.RunID

	cancelResponse := serveHTTP(engine, http.MethodPost, "/graphs/graph/runs/"+runID+"/cancel", "")
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}

	waitForSignal(t, runDone, "run cancel")
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
	runner := mustNewDefaultRunner(t, graph, Config{
		GraphID:        "trigger-graph",
		GraphVersion:   "2.0",
		GraphSessionID: "trigger-session",
	}, t.TempDir(), nil)
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
		"/graphs/trigger-graph/runs/"+run.RunID+"/cancel",
		"",
	)
	canceledRun := decodeRunRecordResponse(t, cancelResponse, http.StatusOK)
	if canceledRun.Status != runtime.RunStatusCanceled {
		t.Fatalf("cancel response status = %q, want %q", canceledRun.Status, runtime.RunStatusCanceled)
	}
	waitForSignal(t, done, "trigger run completion")
}

func TestDeleteActiveRunUsesTriggerSessionRunner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRun := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseRun()

	graph := newRunControlTestGraph(t, started, release, true)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runner := mustNewDefaultRunner(t, graph, Config{
		GraphID:        "trigger-graph",
		GraphVersion:   "2.0",
		GraphSessionID: "trigger-session",
	}, t.TempDir(), nil)
	srv.runtime.cacheTriggerSession("trigger-graph", graphRuntimeSession{
		graph:       graph,
		runner:      runner,
		baseContext: context.Background(),
	})

	run, done, err := runner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "trigger run node start")

	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	deleted := serveHTTP(
		engine,
		http.MethodDelete,
		"/graphs/trigger-graph/runs/"+run.RunID,
		"",
	)
	if deleted.Code != http.StatusConflict {
		t.Fatalf("DELETE active trigger run status = %d, want 409; body = %s", deleted.Code, deleted.Body.String())
	}
	if _, err := runner.GetRun(context.Background(), run.RunID); err != nil {
		t.Fatalf("active trigger run was deleted: %v", err)
	}

	releaseRun()
	waitForSignal(t, done, "trigger run completion")
}

func TestPauseAndResumeRunUseTriggerSessionRunner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRun := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseRun()

	graph := newRunControlTestGraph(t, started, release, true)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runner := mustNewDefaultRunner(t, graph, Config{
		GraphID:        "trigger-graph",
		GraphVersion:   "2.0",
		GraphSessionID: "trigger-session",
	}, t.TempDir(), nil)
	srv.runtime.cacheTriggerSession("trigger-graph", graphRuntimeSession{
		graph:       graph,
		runner:      runner,
		baseContext: context.Background(),
	})

	run, done, err := runner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "trigger run node start")

	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	pauseResponse := serveHTTP(
		engine,
		http.MethodPost,
		"/graphs/trigger-graph/runs/"+run.RunID+"/pause",
		"",
	)
	pausedRun := decodeRunRecordResponse(t, pauseResponse, http.StatusOK)
	if pausedRun.Status != runtime.RunStatusPaused {
		t.Fatalf("pause response status = %q, want %q", pausedRun.Status, runtime.RunStatusPaused)
	}
	waitForSignal(t, done, "paused trigger run completion")

	resumeDone := serveHTTPAsync(
		engine,
		http.MethodPost,
		"/graphs/trigger-graph/runs/"+run.RunID+"/resume",
		`{}`,
	)
	assertNoHTTPResponse(t, resumeDone, "resume trigger run")
	releaseRun()
	resumeResponse := waitForHTTPResponse(t, resumeDone, "resume trigger run")
	resumeResult := decodeRunResultResponse(t, resumeResponse, http.StatusOK)
	if resumeResult.Run.Status != runtime.RunStatusCompleted {
		t.Fatalf("resume response status = %q, want %q", resumeResult.Run.Status, runtime.RunStatusCompleted)
	}
}

func TestPauseRunMarksLostExecutionFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	graph := newRunControlTestGraph(t, make(chan struct{}), make(chan struct{}), true)
	srv, err := New(context.Background(), Config{
		Graph:   graph,
		BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runner := srv.Runner()
	startedAt := time.Now().Add(-time.Minute)
	run := runtime.RunRecord{
		RunID:           "lost-run",
		GraphID:         effectiveRunnerGraphID(runner),
		Status:          runtime.RunStatusRunning,
		CurrentNodeID:   "work",
		PauseRequested:  true,
		CancelRequested: true,
		StartedAt:       startedAt,
		UpdatedAt:       startedAt,
	}
	if err := runner.ExecutionStore().CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	subscription := srv.EventHub().Subscribe(eventFilter{RunID: run.RunID}, "")
	defer subscription.Unsubscribe()

	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	response := serveHTTP(engine, http.MethodPost, "/graphs/graph/runs/"+run.RunID+"/pause", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("pause lost run status = %d, want 409; body = %s", response.Code, response.Body.String())
	}

	failed, err := runner.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if failed.Status != runtime.RunStatusFailed || failed.ErrorCode != "run_execution_lost" {
		t.Fatalf("lost run = %#v, want failed run_execution_lost", failed)
	}
	if failed.PauseRequested || failed.CancelRequested || failed.FinishedAt == nil {
		t.Fatalf("lost run retained non-terminal state: %#v", failed)
	}
	select {
	case event := <-subscription.Events:
		if event.Type != runtime.EventRunFailed {
			t.Fatalf("event type = %q, want %q", event.Type, runtime.EventRunFailed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run.failed event")
	}
}

func TestPauseRunUsesOwningHistoricalSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	firstGraph := newRunControlTestGraph(t, started, release, true)
	firstRunner := mustNewDefaultRunner(t, firstGraph, Config{
		GraphID:        "historical-graph",
		GraphVersion:   "v1",
		GraphSessionID: "session-1",
	}, t.TempDir(), srv.EventHub())
	srv.runtime.installSession(graphRuntimeSession{
		graph:       firstGraph,
		runner:      firstRunner,
		baseContext: context.Background(),
	})

	run, done, err := firstRunner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("StartAsync() error = %v", err)
	}
	waitForSignal(t, started, "historical run node start")

	secondGraph := newRunControlTestGraph(t, make(chan struct{}), make(chan struct{}), true)
	secondRunner := mustNewDefaultRunner(t, secondGraph, Config{
		GraphID:        "historical-graph",
		GraphVersion:   "v2",
		GraphSessionID: "session-2",
	}, t.TempDir(), nil)
	srv.runtime.installSession(graphRuntimeSession{
		graph:       secondGraph,
		runner:      secondRunner,
		baseContext: context.Background(),
	})

	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	response := serveHTTP(
		engine,
		http.MethodPost,
		"/graphs/historical-graph/runs/"+run.RunID+"/pause",
		"",
	)
	paused := decodeRunRecordResponse(t, response, http.StatusOK)
	if paused.Status != runtime.RunStatusPaused || paused.GraphSessionID != "session-1" {
		t.Fatalf("paused historical run = %#v", paused)
	}
	waitForSignal(t, done, "historical run pause")
	if _, err := secondRunner.GetRun(context.Background(), run.RunID); !errors.Is(err, runtime.ErrRunnerRecordNotFound) {
		t.Fatalf("new session runner GetRun() error = %v, want not found", err)
	}
}

func TestListRunsReconcilesOrphanedCachedExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("orphan-graph", "v1", "hello"))
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	started = waitForRunTerminalStatus(t, srv.runtime.session("orphan-graph", uploaded.Graph.GraphSessionID).runner, started.RunID)

	executionStore := runtime.NewFileExecutionStore(filepath.Join(srv.graphHistoryBaseDir(uploaded.Graph.ID), "execution"))
	started.Status = runtime.RunStatusRunning
	started.PauseRequested = true
	started.CancelRequested = true
	started.ErrorCode = ""
	started.ErrorMessage = ""
	started.StartedAt = time.Now().Add(-time.Minute)
	started.UpdatedAt = started.StartedAt
	started.FinishedAt = nil
	if err := executionStore.UpdateRun(context.Background(), started); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}

	restarted, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New() after restart error = %v", err)
	}
	subscription := restarted.EventHub().Subscribe(eventFilter{RunID: started.RunID}, "")
	defer subscription.Unsubscribe()
	restartedEngine := gin.New()
	restarted.RegisterRoutes(restartedEngine.Group(""))
	response := serveHTTP(restartedEngine, http.MethodGet, "/graphs/orphan-graph/runs", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET runs status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data runListPage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("runs = %#v, want one", envelope.Data)
	}
	failed := envelope.Data.Items[0]
	if failed.Status != runtime.RunStatusFailed || failed.ErrorCode != "run_execution_lost" {
		t.Fatalf("reconciled run = %#v", failed)
	}
	if failed.PauseRequested || failed.CancelRequested || failed.FinishedAt == nil {
		t.Fatalf("reconciled run retained non-terminal state: %#v", failed)
	}
	select {
	case event := <-subscription.Events:
		if event.Type != runtime.EventRunFailed {
			t.Fatalf("event type = %q, want %q", event.Type, runtime.EventRunFailed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconciled run.failed event")
	}
	persistedEvents, err := runtime.NewFileEventSink(filepath.Join(srv.graphHistoryBaseDir(uploaded.Graph.ID), "events")).ListEvents(started.RunID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if !hasRuntimeEvent(persistedEvents, runtime.EventRunFailed) {
		t.Fatal("persisted events missing run.failed")
	}
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
		"settings": {"environment": {}, "models": []},
		"triggers": [
			{"id":"hook","type":"webhook","enabled":true,"webhook":{"state_bindings":{"input":"shared.request.input"}}},
			{"id":"empty","type":"webhook","enabled":true,"webhook":{}}
		],
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
	req := httptest.NewRequest(http.MethodPost, "/graphs/requires-input/analysis/initial-state-requirements", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph/initial-state-requirements status = %d, body = %s", w.Code, w.Body.String())
	}
	assertInitialStateRequirementAnalysisResponse(t, w.Body.Bytes())

	putGraphForHashTest(t, engine, graphBody)
	detail := decodeGraphDetailResponse(t, serveHTTP(engine, http.MethodGet, "/graphs/requires-input", ""), http.StatusOK)
	assertInitialStateRequirements(t, detail.InitialStateRequirements)
}

func assertInitialStateRequirementAnalysisResponse(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Data  graphInitialStateAnalysis `json:"data"`
		Error *apiError                 `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode requirements response: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("response error = %#v", response.Error)
	}
	assertInitialStateRequirements(t, response.Data.Direct)
	if len(response.Data.Triggers) != 2 {
		t.Fatalf("trigger requirements = %#v, want two independent analyses", response.Data.Triggers)
	}
	if hook := response.Data.Triggers[0]; hook.TriggerID != "hook" || len(hook.Requirements.ProvidedByEntry) != 1 || hook.Requirements.ProvidedByEntry[0].Path != "shared.request.input" || len(hook.Requirements.Required) != 0 {
		t.Fatalf("hook requirements = %#v", hook)
	}
	if empty := response.Data.Triggers[1]; empty.TriggerID != "empty" || len(empty.Requirements.Required) != 1 || len(empty.Requirements.ProvidedByEntry) != 0 {
		t.Fatalf("empty trigger requirements = %#v", empty)
	}
}

func assertInitialStateRequirements(t *testing.T, requirements core.InitialStateRequirements) {
	t.Helper()
	if len(requirements.Required) != 1 {
		t.Fatalf("required = %#v, want one item", requirements.Required)
	}
	required := requirements.Required[0]
	if required.Path != "shared.request.input" {
		t.Fatalf("required path = %q, want shared.request.input", required.Path)
	}
	if len(required.Nodes) != 1 || required.Nodes[0] != "input" {
		t.Fatalf("required nodes = %#v, want [input]", required.Nodes)
	}
	if required.Type != "string" {
		t.Fatalf("required type = %q, want string", required.Type)
	}
	if len(requirements.ProvidedByUpstream) != 0 {
		t.Fatalf("provided_by_upstream = %#v, want empty", requirements.ProvidedByUpstream)
	}
	if len(requirements.Unresolved) != 0 {
		t.Fatalf("unresolved = %#v, want empty", requirements.Unresolved)
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
	graphID, requestBody := graphSessionRequestBodyForTest(t, body)
	return requestGraphForHashTest(t, engine, http.MethodPost, "/graphs/"+graphID+"/sessions", requestBody)
}

func startGraphRunForTest(t *testing.T, engine *gin.Engine, graph graphLoadResponse, body string) runtime.RunRecord {
	t.Helper()
	return decodeRunRecordResponse(t, serveHTTP(
		engine,
		http.MethodPost,
		"/graphs/"+graph.Graph.ID+"/sessions/"+graph.Graph.GraphSessionID+"/runs",
		body,
	), http.StatusAccepted)
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

func graphSessionRequestBodyForTest(t *testing.T, body string) (string, string) {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode graph test body: %v", err)
	}
	graphID, _ := envelope["graph_id"].(string)
	delete(envelope, "graph_id")
	if graphID == "" {
		definition, _ := envelope["definition"].(map[string]any)
		graphID, _ = definition["name"].(string)
	}
	if graphID == "" {
		t.Fatal("graph test body is missing graph_id and definition.name")
	}
	requestBody, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode graph test body: %v", err)
	}
	return graphID, string(requestBody)
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

func waitForRunTerminalStatus(t *testing.T, runner *runtime.GraphRunner, runID string) runtime.RunRecord {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := runner.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("get run %q: %v", runID, err)
		}
		switch run.Status {
		case runtime.RunStatusCompleted, runtime.RunStatusFailed, runtime.RunStatusCanceled, runtime.RunStatusPaused:
			return run
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for run %q to finish", runID)
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
	if graphID == "" {
		t.Fatal("graph ID is required for run inspection")
	}
	basePath := "/graphs/" + graphID + "/runs/" + runID
	inspectionResponse := serveHTTP(engine, http.MethodGet, basePath+"/inspection", "")
	if inspectionResponse.Code != http.StatusOK {
		t.Fatalf("GET run inspection status = %d, body = %s", inspectionResponse.Code, inspectionResponse.Body.String())
	}
	var inspectionEnvelope struct {
		Data runInspectionResponse `json:"data"`
	}
	if err := json.Unmarshal(inspectionResponse.Body.Bytes(), &inspectionEnvelope); err != nil {
		t.Fatalf("decode run inspection response: %v", err)
	}
	resources := runResourceSet{
		Run:         inspectionEnvelope.Data.Run,
		Steps:       inspectionEnvelope.Data.Steps,
		Checkpoints: inspectionEnvelope.Data.Checkpoints,
		Events:      inspectionEnvelope.Data.Events.Items,
		Interrupt:   inspectionEnvelope.Data.Interrupt,
	}
	artifactResponse := serveHTTP(engine, http.MethodGet, basePath+"/artifacts", "")
	if artifactResponse.Code != http.StatusOK {
		t.Fatalf("GET run artifacts status = %d, body = %s", artifactResponse.Code, artifactResponse.Body.String())
	}
	var artifactEnvelope struct {
		Data []state.ArtifactRef `json:"data"`
	}
	if err := json.Unmarshal(artifactResponse.Body.Bytes(), &artifactEnvelope); err != nil {
		t.Fatalf("decode run artifacts response: %v", err)
	}
	resources.Artifacts = artifactEnvelope.Data
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

func decodeGraphDetailResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) graphDetailResponse {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, wantStatus, response.Body.String())
	}
	var decoded struct {
		Data  graphDetailResponse `json:"data"`
		Error *apiError           `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode graph detail response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %#v", decoded.Error)
	}
	return decoded.Data
}
