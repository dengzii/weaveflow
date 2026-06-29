package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
	langgraph "github.com/smallnest/langgraphgo/graph"
)

type contractTestNode struct {
	core.NodeBase
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
}

func newInterruptTestNode(spec dsl.GraphNodeSpec) *interruptTestNode {
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	return &interruptTestNode{
		NodeBase: core.NewNodeBase(core.NodeSpec{
			ID:   spec.ID,
			Name: name,
		}),
	}
}

func (n *interruptTestNode) Execute(_ core.Context, access *state.Access) error {
	if value, ok := access.ReadAny(state.Shared("resume")); ok && value == "ok" {
		return nil
	}
	return &langgraph.NodeInterrupt{Node: n.ID(), Value: "waiting for resume input"}
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
			"version": "1.0",
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "human_message",
					"config": {"content": "hello"}
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

	var response struct {
		Data runResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if response.Data.Run.GraphID != "debug-graph" {
		t.Fatalf("run graph id = %q, want debug-graph", response.Data.Run.GraphID)
	}
	if response.Data.Run.Status != runtime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", response.Data.Run.Status, runtime.RunStatusCompleted)
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
			"version": "1.0",
			"name": "debug-graph",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "human_message",
					"config": {"content": "hello"}
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
	if err := reg.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:          "interrupt_once",
			Title:         "Interrupt Once",
			StateContract: &dsl.StateContract{},
		},
		Build: func(_ *wfregistry.BuildContext, spec dsl.GraphNodeSpec) (core.Node, error) {
			return newInterruptTestNode(spec), nil
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
			"version": "1.0",
			"name": "interrupt-graph",
			"entry_point": "wait",
			"finish_point": "wait",
			"nodes": [
				{"id": "wait", "type": "interrupt_once"}
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

func TestGraphInitialStateRequirementsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := wfregistry.NewRegistry()
	contract := dsl.StateContract{
		Fields: []dsl.StateFieldRef{
			{
				Path:        "shared.request.input",
				Mode:        dsl.StateAccessRead,
				Required:    true,
				Description: "User request input.",
				Schema:      dsl.JSONSchema{"type": "string"},
			},
		},
	}
	if err := reg.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type:          "requires_input",
			Title:         "Requires Input",
			StateContract: &contract,
		},
		Build: func(_ *wfregistry.BuildContext, spec dsl.GraphNodeSpec) (core.Node, error) {
			return newContractTestNode(spec), nil
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
			"version": "1.0",
			"name": "requires-input",
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "requires_input"
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
