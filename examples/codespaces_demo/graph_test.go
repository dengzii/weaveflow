package codespaces_demo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/server"
	"github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
)

func TestGraphBuildsAndRunsWithoutInitialInput(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("graph.json")
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	definition, err := dsl.DeserializeGraphDefinition(data)
	if err != nil {
		t.Fatalf("DeserializeGraphDefinition(): %v", err)
	}
	workflow, err := wfgraph.NewBuilder(builtin.NewDefaultRegistry()).Build(definition, &registry.BuildContext{})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	requirements := workflow.InitialStateRequirements()
	if len(requirements.Required) != 0 || len(requirements.Unresolved) != 0 {
		t.Fatalf("initial state requirements = %#v", requirements)
	}
	if err := workflow.ValidateInitialState(state.NewState()); err != nil {
		t.Fatalf("ValidateInitialState(): %v", err)
	}

	result, err := workflow.Run(context.Background(), state.NewState())
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	answer, exists := state.ReadPath(result, "shared.final.answer")
	if !exists {
		t.Fatal("shared.final.answer is missing")
	}
	const want = "WeaveFlow Codespaces demo completed. Inspect State, Events, Steps, and Checkpoints for this Run."
	if answer != want {
		t.Fatalf("shared.final.answer = %#v, want %q", answer, want)
	}
}

func TestGraphPublishesAsSessionAndRunsThroughServer(t *testing.T) {
	data, err := os.ReadFile("graph.json")
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.New(ctx, server.Config{
		BaseDir:             t.TempDir(),
		RuntimeStoreBackend: server.RuntimeStoreSQLite,
	})
	if err != nil {
		cancel()
		t.Fatalf("server.New(): %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if err := srv.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploadBody, err := json.Marshal(map[string]any{
		"definition":    json.RawMessage(data),
		"graph_version": "1.0",
		"settings": map[string]any{
			"environment":         map[string]string{},
			"environment_secrets": map[string]any{},
			"models":              []any{},
			"tool_permissions":    []string{},
			"tool_approvals":      map[string]bool{},
		},
		"triggers":   []any{},
		"mode":       "create",
		"request_id": "container-bootstrap-codespaces_demo-1.0",
	})
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}

	uploaded := requestJSON[struct {
		Graph struct {
			ID             string `json:"id"`
			GraphSessionID string `json:"graph_session_id"`
		} `json:"graph"`
	}](t, engine, http.MethodPost, "/graphs/codespaces_demo/sessions", uploadBody, http.StatusOK)
	if uploaded.Graph.ID != "codespaces_demo" || uploaded.Graph.GraphSessionID == "" {
		t.Fatalf("uploaded graph = %#v", uploaded.Graph)
	}

	graphs := requestJSON[struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}](t, engine, http.MethodGet, "/graphs", nil, http.StatusOK)
	if len(graphs.Items) != 1 || graphs.Items[0].ID != "codespaces_demo" {
		t.Fatalf("graphs = %#v", graphs.Items)
	}

	run := requestJSON[runtime.RunRecord](
		t,
		engine,
		http.MethodPost,
		"/graphs/codespaces_demo/sessions/"+uploaded.Graph.GraphSessionID+"/runs",
		[]byte(`{"initial_state":{"shared":{},"scopes":{}}}`),
		http.StatusAccepted,
	)
	deadline := time.Now().Add(5 * time.Second)
	for run.Status != runtime.RunStatusCompleted && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		run, err = srv.Runner().GetRun(ctx, run.RunID)
		if err != nil {
			t.Fatalf("GetRun(): %v", err)
		}
	}
	if run.Status != runtime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", run.Status, runtime.RunStatusCompleted)
	}
}

func requestJSON[T any](
	t *testing.T,
	engine http.Handler,
	method string,
	path string,
	body []byte,
	wantStatus int,
) T {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, response.Code, wantStatus, response.Body.String())
	}
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s %s response: %v", method, path, err)
	}
	return envelope.Data
}
