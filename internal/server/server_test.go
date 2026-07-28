package server

import (
	"context"
	"encoding/json"
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

	srv := &Server{registry: reg}
	engine := gin.New()
	engine.GET("/registry", srv.handleRegistry)
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
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(`{
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
		ginContext.Request = httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(body))
		if _, err := bindGraphUpload(ginContext); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("bindGraphUpload(%s) error = %v, want unknown field", body, err)
		}
	}
}

func TestBindGraphSettingsRequestRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"modle": {}}`,
		`{"memory": {"enabled": true, "path": "legacy"}}`,
	} {
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginContext.Request = httptest.NewRequest(http.MethodPut, "/graph/settings", strings.NewReader(body))
		if _, err := bindGraphSettingsRequest(ginContext); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("bindGraphSettingsRequest(%s) error = %v, want unknown field", body, err)
		}
	}
}

func TestCurrentGraphEnvironmentIgnoresLegacyOpenAIBaseURL(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "http://legacy.invalid/v1")
	if value, ok := currentGraphEnvironment()["OPENAI_API_BASE"]; ok {
		t.Fatalf("currentGraphEnvironment() exposed OPENAI_API_BASE=%q", value)
	}
	if models := graphModelSettingsFromContext(context.Background()); len(models) != 0 {
		t.Fatalf("graphModelSettingsFromContext() used legacy base URL: %#v", models)
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

func TestHandleToolsReturnsRuntimeToolDefinitions(t *testing.T) {
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

	w := serveHTTP(engine, http.MethodGet, "/tools", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /tools status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Data  toolsResponse `json:"data"`
		Error string        `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode tools response: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response error = %q", response.Error)
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

func TestHandleGraphSettingsUpdatesRuntimeContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
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

	body := `{
		"environment": {"WEAVEFLOW_TEST_FLAG": "enabled", "WEAVEFLOW_TEST_TOKEN": "secret-token"},
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
	w := serveHTTP(engine, http.MethodPut, "/graph/settings", body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /graph/settings status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data  graphRuntimeSettings `json:"data"`
		Error string               `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response error = %q", response.Error)
	}
	if !response.Data.Model.Enabled || response.Data.Model.Provider != "openai" || response.Data.Model.Model != "gpt-test" {
		t.Fatalf("model settings = %#v", response.Data.Model)
	}
	if len(response.Data.Models) != 2 {
		t.Fatalf("models length = %d, want 2", len(response.Data.Models))
	}
	if response.Data.Models[0].ID != core.DefaultModelID || response.Data.Models[0].Model != "gpt-test" {
		t.Fatalf("default model settings = %#v", response.Data.Models[0])
	}
	if response.Data.Models[1].ID != "fast" || response.Data.Models[1].Model != "gpt-fast" {
		t.Fatalf("fast model settings = %#v", response.Data.Models[1])
	}
	if !response.Data.Model.APIKeyConfigured || !response.Data.Models[0].APIKeyConfigured || !response.Data.Models[1].APIKeyConfigured {
		t.Fatalf("api key configured flags = model %#v models %#v", response.Data.Model, response.Data.Models)
	}
	if !response.Data.Memory.Enabled || response.Data.Memory.Directory == "" {
		t.Fatalf("memory settings = %#v", response.Data.Memory)
	}
	if response.Data.Environment["WEAVEFLOW_TEST_FLAG"] != "enabled" {
		t.Fatalf("environment = %#v", response.Data.Environment)
	}
	if _, ok := response.Data.Environment["WEAVEFLOW_TEST_TOKEN"]; ok {
		t.Fatalf("settings response leaked secret environment name: %#v", response.Data.Environment)
	}
	if strings.Contains(w.Body.String(), "test-key") || strings.Contains(w.Body.String(), "secret-token") {
		t.Fatalf("settings response leaked api key: %s", w.Body.String())
	}
	if got := os.Getenv("WEAVEFLOW_TEST_TOKEN"); got != "secret-token" {
		t.Fatalf("WEAVEFLOW_TEST_TOKEN = %q, want secret-token", got)
	}
	if got := os.Getenv("WEAVEFLOW_TEST_FLAG"); got != "enabled" {
		t.Fatalf("WEAVEFLOW_TEST_FLAG = %q, want enabled", got)
	}
	if got := os.Getenv("OPENAI_MODEL"); got != "gpt-test" {
		t.Fatalf("OPENAI_MODEL = %q, want gpt-test", got)
	}

	coreCtx := core.NewContext(srv.baseCtx)
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

	w = serveHTTP(engine, http.MethodPut, "/graph/settings", `{"environment":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("clear visible environment status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := os.Getenv("WEAVEFLOW_TEST_FLAG"); got != "" {
		t.Fatalf("removed WEAVEFLOW_TEST_FLAG = %q, want empty", got)
	}
	if got := os.Getenv("WEAVEFLOW_TEST_TOKEN"); got != "secret-token" {
		t.Fatalf("omitted secret WEAVEFLOW_TEST_TOKEN = %q, want preserved", got)
	}

	w = serveHTTP(engine, http.MethodPut, "/graph/settings", `{"environment":{"WEAVEFLOW_TEST_TOKEN":""}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("clear secret environment status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := os.Getenv("WEAVEFLOW_TEST_TOKEN"); got != "" {
		t.Fatalf("cleared WEAVEFLOW_TEST_TOKEN = %q, want empty", got)
	}
}

func TestHandleToolsConcurrentWithSettingsUpdates(t *testing.T) {
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

	const requests = 32
	var wg sync.WaitGroup
	for range requests {
		wg.Add(2)
		go func() {
			defer wg.Done()
			response := serveHTTP(engine, http.MethodGet, "/tools", "")
			if response.Code != http.StatusOK {
				t.Errorf("GET /tools status = %d, body = %s", response.Code, response.Body.String())
			}
		}()
		go func() {
			defer wg.Done()
			response := serveHTTP(engine, http.MethodPut, "/graph/settings", `{"memory":{"enabled":false}}`)
			if response.Code != http.StatusOK {
				t.Errorf("PUT /graph/settings status = %d, body = %s", response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
}

func TestGraphRuntimeSettingsIncludesToolEnvironment(t *testing.T) {
	expected := map[string]string{
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
		Data  []runtime.Event `json:"data"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response error = %q", response.Error)
	}
	if len(response.Data) != 1 || response.Data[0].ID != event.ID {
		t.Fatalf("response data = %#v, want one %q event", response.Data, event.ID)
	}
}

func TestPostGraphConfiguresRunnerForDebugRun(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
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

func TestPostGraphMetadataOnlyChangeKeepsSemanticHash(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	first := postGraphForHashTest(t, engine, `{
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
			"metadata": {"web": {"positions": {"input": {"x": 10, "y": 20}}}}
		}
	}`)
	time.Sleep(time.Millisecond)
	second := postGraphForHashTest(t, engine, `{
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
			"metadata": {"web": {"positions": {"input": {"x": 30, "y": 40}}}}
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

	w := serveHTTP(engine, http.MethodPost, "/graph", graphBody)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	start := serveHTTP(engine, http.MethodPost, "/runs", `{}`)
	result := decodeRunResultResponse(t, start, http.StatusOK)

	deleted := decodeRunRecordResponse(t, serveHTTP(engine, http.MethodDelete, "/runs/"+result.Run.RunID+"?graph_id=debug-graph", ""), http.StatusOK)
	if deleted.RunID != result.Run.RunID {
		t.Fatalf("deleted run id = %q, want %q", deleted.RunID, result.Run.RunID)
	}

	w = serveHTTP(engine, http.MethodGet, "/runs/"+result.Run.RunID+"/detail?graph_id=debug-graph", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET deleted run detail status = %d, body = %s", w.Code, w.Body.String())
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

	w := serveHTTP(engine, http.MethodPost, "/graph", graphBody)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	start := serveHTTP(engine, http.MethodPost, "/runs", `{}`)
	result := decodeRunResultResponse(t, start, http.StatusOK)

	cacheOnlyServer, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("New cache-only server error = %v", err)
	}
	cacheOnlyEngine := gin.New()
	cacheOnlyServer.RegisterRoutes(cacheOnlyEngine.Group(""))

	deleted := decodeRunRecordResponse(t, serveHTTP(cacheOnlyEngine, http.MethodDelete, "/runs/"+result.Run.RunID+"?graph_id=debug-graph", ""), http.StatusOK)
	if deleted.RunID != result.Run.RunID {
		t.Fatalf("deleted run id = %q, want %q", deleted.RunID, result.Run.RunID)
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
		req := httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(graphBody))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
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

func TestGetRunDetailAggregatesDebugRecords(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
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

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/detail", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs/:run_id/detail status = %d, body = %s", w.Code, w.Body.String())
	}

	var detailResponse struct {
		Data  runDetail `json:"data"`
		Error string    `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detailResponse); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailResponse.Error != "" {
		t.Fatalf("response error = %q", detailResponse.Error)
	}
	if detailResponse.Data.Run.RunID != runID {
		t.Fatalf("detail run id = %q, want %q", detailResponse.Data.Run.RunID, runID)
	}
	if len(detailResponse.Data.Steps) == 0 {
		t.Fatal("detail steps are empty")
	}
	if len(detailResponse.Data.Checkpoints) == 0 {
		t.Fatal("detail checkpoints are empty")
	}
	if len(detailResponse.Data.Events) == 0 {
		t.Fatal("detail events are empty")
	}
	if detailResponse.Data.Artifacts == nil {
		t.Fatal("detail artifacts is nil")
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
	req := httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
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
		Error string    `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startResponse); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResponse.Error != "" {
		t.Fatalf("start response error = %q", startResponse.Error)
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

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/runs/"+startResponse.Data.Run.RunID+"/detail", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs/:run_id/detail status = %d, body = %s", w.Code, w.Body.String())
	}
	var detailResponse struct {
		Data runDetail `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detailResponse); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailResponse.Data.Interrupt == nil {
		t.Fatal("detail interrupt is nil")
	}
	if detailResponse.Data.Interrupt.Message != "waiting for resume input" {
		t.Fatalf("detail interrupt message = %q, want waiting for resume input", detailResponse.Data.Interrupt.Message)
	}
	var pausedPayload map[string]any
	for _, event := range detailResponse.Data.Events {
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
		Error string    `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resumeResponse); err != nil {
		t.Fatalf("decode resume response: %v", err)
	}
	if resumeResponse.Error != "" {
		t.Fatalf("resume response error = %q", resumeResponse.Error)
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
	req := httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
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

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/runs/"+startResponse.Data.Run.RunID+"/detail?graph_id=interrupt-graph", nil)
	cacheOnlyEngine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /runs/:run_id/detail status = %d, body = %s", w.Code, w.Body.String())
	}
	var detailResponse struct {
		Data runDetail `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detailResponse); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailResponse.Data.Run.Status != runtime.RunStatusCanceled {
		t.Fatalf("detail run status = %q, want %q", detailResponse.Data.Run.Status, runtime.RunStatusCanceled)
	}
	if !hasRuntimeEvent(detailResponse.Data.Events, runtime.EventRunCanceled) {
		t.Fatal("detail events missing run.canceled")
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
	req = httptest.NewRequest(http.MethodPost, "/graph", strings.NewReader(graphBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
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
		Error string                        `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode requirements response: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response error = %q", response.Error)
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

func postGraphForHashTest(t *testing.T, engine *gin.Engine, body string) graphLoadResponse {
	t.Helper()
	w := serveHTTP(engine, http.MethodPost, "/graph", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /graph status = %d, body = %s", w.Code, w.Body.String())
	}
	var decoded struct {
		Data  graphLoadResponse `json:"data"`
		Error string            `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	if decoded.Error != "" {
		t.Fatalf("response error = %q", decoded.Error)
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

func decodeRunRecordResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) runtime.RunRecord {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data  runtime.RunRecord `json:"data"`
		Error string            `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode run record response: %v", err)
	}
	if decoded.Error != "" {
		t.Fatalf("response error = %q", decoded.Error)
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
		Error string    `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode run result response: %v", err)
	}
	if decoded.Error != "" {
		t.Fatalf("response error = %q", decoded.Error)
	}
	return decoded.Data
}
