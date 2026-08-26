package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

func TestReadCachedGraphSessionRequiresCurrentManifestFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		want  string
	}{
		{field: "graph_version", want: "graph version is missing"},
		{field: "graph_hash", want: "graph hash is missing"},
		{field: "graph_snapshot_hash", want: "graph snapshot hash is missing"},
		{field: "graph_session_id", want: "manifest id is missing"},
		{field: "definition_path", want: "definition path is missing"},
		{field: "settings_path", want: "settings path is missing"},
		{field: "runtime_settings_hash", want: "runtime settings hash is missing"},
		{field: "created_at", want: "created_at is missing"},
		{field: "legacy", want: "unknown field"},
	}
	for _, test := range tests {
		t.Run(test.field, func(t *testing.T) {
			t.Parallel()

			graphDirectory := t.TempDir()
			sessionID := "20260802T010203.000000000Z"
			sessionDirectory := filepath.Join(graphDirectory, sessionID)
			if err := os.MkdirAll(sessionDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sessionDirectory, "definition.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			settings := graphRuntimeSettings{Environment: map[string]string{}, Models: []graphModelSettings{}}
			settingsHash, err := graphRuntimeSettingsHash(settings)
			if err != nil {
				t.Fatal(err)
			}
			if err := persistGraphRuntimeSettings(sessionDirectory, settings); err != nil {
				t.Fatal(err)
			}
			manifest := map[string]any{
				"graph_id":              "graph-a",
				"graph_version":         "v1",
				"graph_hash":            "hash",
				"graph_snapshot_hash":   "snapshot-hash",
				"graph_session_id":      sessionID,
				"definition_path":       "definition.json",
				"settings_path":         graphRuntimeSettingsFileName,
				"runtime_settings_hash": settingsHash,
				"created_at":            time.Date(2026, 8, 2, 1, 2, 3, 0, time.UTC),
			}
			if test.field == "legacy" {
				manifest[test.field] = true
			} else {
				delete(manifest, test.field)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sessionDirectory, "graph.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			_, _, err = readCachedGraphSession(graphDirectory, sessionID)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readCachedGraphSession() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadCachedGraphSessionHashesStoredSettingsBytes(t *testing.T) {
	t.Parallel()

	graphDirectory := t.TempDir()
	sessionID := "20260807T102510.733454600Z"
	sessionDirectory := filepath.Join(graphDirectory, sessionID)
	if err := os.MkdirAll(sessionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDirectory, "definition.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settingsData := []byte(`{
  "version": 4,
  "environment": {},
  "environment_secrets": {},
  "models": [
    {
      "id": "default",
      "enabled": true,
      "provider": "openai"
    }
  ]
}
`)
	settingsPath := filepath.Join(sessionDirectory, graphRuntimeSettingsFileName)
	if err := os.WriteFile(settingsPath, settingsData, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := graphSessionManifest{
		GraphID:             "graph-a",
		GraphVersion:        "v1",
		GraphHash:           "hash",
		GraphSnapshotHash:   "snapshot-hash",
		GraphSessionID:      sessionID,
		DefinitionPath:      "definition.json",
		SettingsPath:        graphRuntimeSettingsFileName,
		RuntimeSettingsHash: graphRuntimeSettingsDataHash(settingsData),
		CreatedAt:           time.Date(2026, 8, 7, 10, 25, 10, 0, time.UTC),
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDirectory, "graph.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, complete, err := readCachedGraphSession(graphDirectory, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !complete || loaded.GraphSessionID != sessionID {
		t.Fatalf("readCachedGraphSession() = %#v, %t, want complete session %q", loaded, complete, sessionID)
	}

	if err := os.WriteFile(settingsPath, append(settingsData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = readCachedGraphSession(graphDirectory, sessionID)
	if err == nil || !strings.Contains(err.Error(), "runtime settings hash mismatch") {
		t.Fatalf("readCachedGraphSession() error = %v, want runtime settings hash mismatch", err)
	}
}

func TestListCachedGraphsPreservesPortableGraphID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph.with.dots", "v1", "hello"))

	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].ID != "graph.with.dots" {
		t.Fatalf("listCachedGraphs() = %#v, want original graph id", graphs)
	}
}

func TestListCachedGraphsIgnoresIncompleteSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "hello"))
	if err := os.MkdirAll(filepath.Join(filepath.Dir(uploaded.RunnerBaseDir), "zzzz-incomplete"), 0o755); err != nil {
		t.Fatal(err)
	}

	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].SessionCount != 1 || graphs[0].LatestSession != uploaded.Graph.GraphSessionID {
		t.Fatalf("listCachedGraphs() = %#v, want only completed session %q", graphs, uploaded.Graph.GraphSessionID)
	}
	if graphs[0].GraphVersion != "v1" || graphs[0].Name != "graph-a" || graphs[0].NodeCount != 1 || graphs[0].UpdatedAt.IsZero() {
		t.Fatalf("latest graph summary = %#v, want uploaded graph metadata", graphs[0])
	}
}

func TestListCachedGraphsLoadsLatestDefinition(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "first"))
	time.Sleep(time.Millisecond)
	latest := putGraphForHashTest(t, engine, graphUploadBodyWithSettings(
		"graph-a",
		"v2",
		"latest",
		`{"environment":{"MODE":"latest"},"models":[]}`,
	))

	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 {
		t.Fatalf("listCachedGraphs() = %#v, want one graph", graphs)
	}
	graph := graphs[0]
	if graph.SessionCount != 2 || graph.LatestSession != latest.Graph.GraphSessionID {
		t.Fatalf("graph sessions = %#v, want latest session %q", graph, latest.Graph.GraphSessionID)
	}
	if graph.GraphVersion != "v2" || graph.Name != "graph-a" || graph.NodeCount != 1 {
		t.Fatalf("graph summary = %#v, want latest v2 metadata", graph)
	}
	detail := decodeGraphDetailResponse(t, serveHTTP(engine, http.MethodGet, "/graphs/graph-a", ""), http.StatusOK)
	if detail.Settings.Environment["MODE"] != "latest" {
		t.Fatalf("graph detail settings = %#v, want MODE=latest", detail.Settings)
	}
	if len(detail.Definition.Nodes) != 1 || detail.Definition.Nodes[0].Config["content"] != "latest" {
		t.Fatalf("graph detail definition = %#v, want latest content", detail.Definition)
	}
}

func TestGetGraphSessionDetailLoadsExactSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	first := putGraphForHashTest(t, engine, graphUploadBodyWithSettings(
		"graph-a",
		"v1",
		"first",
		`{"environment":{"MODE":"first"},"models":[]}`,
	))
	time.Sleep(time.Millisecond)
	putGraphForHashTest(t, engine, graphUploadBodyWithSettings(
		"graph-a",
		"v2",
		"latest",
		`{"environment":{"MODE":"latest"},"models":[]}`,
	))

	detail := decodeGraphDetailResponse(
		t,
		serveHTTP(engine, http.MethodGet, "/graphs/graph-a/sessions/"+first.Graph.GraphSessionID, ""),
		http.StatusOK,
	)
	if detail.Graph.GraphSessionID != first.Graph.GraphSessionID || detail.Graph.Version != "v1" {
		t.Fatalf("graph detail identity = %#v, want session %q version v1", detail.Graph, first.Graph.GraphSessionID)
	}
	if detail.LatestSession.ID == first.Graph.GraphSessionID {
		t.Fatalf("graph detail latest session = %#v, want a different latest session", detail.LatestSession)
	}
	if detail.Settings.Environment["MODE"] != "first" {
		t.Fatalf("graph detail settings = %#v, want MODE=first", detail.Settings)
	}
	if len(detail.Definition.Nodes) != 1 || detail.Definition.Nodes[0].Config["content"] != "first" {
		t.Fatalf("graph detail definition = %#v, want first content", detail.Definition)
	}
}

func TestGraphRoutesRejectNonPortableGraphIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	for _, test := range []struct {
		graphID string
		pathID  string
	}{
		{graphID: "graph with spaces", pathID: "graph%20with%20spaces"},
		{graphID: "graph?id", pathID: "graph%3Fid"},
	} {
		_, body := graphSessionRequestBodyForTest(t, triggerGraphUploadBody(test.graphID, "v1", "content"))
		response := serveHTTP(engine, http.MethodPost, "/graphs/"+test.pathID+"/sessions", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST graph %q status = %d, want 400; body = %s", test.graphID, response.Code, response.Body.String())
		}
	}
}

func TestGraphStorageSupportsWindowsReservedGraphID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("CON", "v1", "reserved"))
	if filepath.Base(filepath.Dir(uploaded.RunnerBaseDir)) == "CON" {
		t.Fatalf("reserved graph ID used directly as storage directory: %q", uploaded.RunnerBaseDir)
	}
	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].ID != "CON" {
		t.Fatalf("listCachedGraphs() = %#v, want graph ID CON", graphs)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Server.Close() before resolver restart error = %v", err)
	}

	resolver, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "CON"})
	if err != nil {
		t.Fatal(err)
	}
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID() != uploaded.Graph.GraphSessionID {
		t.Fatalf("resolved reserved graph session = %q, want %q", runner.GraphSessionID(), uploaded.Graph.GraphSessionID)
	}
}

func TestGraphCacheSurfacesCorruptRunRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "hello"))
	started := decodeRunRecordResponse(t, serveHTTP(
		engine,
		http.MethodPost,
		"/graphs/graph-a/sessions/"+uploaded.Graph.GraphSessionID+"/runs",
		`{}`,
	), http.StatusAccepted)
	activeRunner := srv.runtime.session("graph-a", uploaded.Graph.GraphSessionID).runner
	waitForRunTerminalStatus(t, activeRunner, started.RunID)
	waitForRunInactive(t, activeRunner, started.RunID)
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	runPath := filepath.Join(srv.graphHistoryBaseDir("graph-a"), "execution", "runs", started.RunID+".json")
	if err := os.WriteFile(runPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheOnly, err := New(context.Background(), Config{BaseDir: baseDir})
	if err == nil {
		_ = cacheOnly.Close()
		t.Fatal("New() succeeded with a corrupt durable run record")
	}
}

func TestGraphUploadDistinguishesInvalidDefinitionFromStorageFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid definition", func(t *testing.T) {
		srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		engine := gin.New()
		srv.RegisterRoutes(engine.Group(""))
		response := serveHTTP(engine, http.MethodPost, "/graphs/graph-a/sessions", `{
			"definition": {
				"version": "1.0",
				"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
				"entry_point": "missing",
				"finish_point": "missing",
				"nodes": [{"id":"missing","type":"not_registered","state":{}}]
			},
			"settings": {"environment":{},"models":[]}
		}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid graph status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("storage failure", func(t *testing.T) {
		baseDir := t.TempDir()
		srv, err := New(context.Background(), Config{BaseDir: baseDir})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(baseDir, "graphs"), []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		engine := gin.New()
		srv.RegisterRoutes(engine.Group(""))
		_, body := graphSessionRequestBodyForTest(t, triggerGraphUploadBody("graph-a", "v1", "hello"))
		response := serveHTTP(engine, http.MethodPost, "/graphs/graph-a/sessions", body)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("storage failure status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}
