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
			manifest := map[string]any{
				"graph_id":            "graph-a",
				"graph_version":       "v1",
				"graph_hash":          "hash",
				"graph_snapshot_hash": "snapshot-hash",
				"graph_session_id":    sessionID,
				"definition_path":     "definition.json",
				"created_at":          time.Date(2026, 8, 2, 1, 2, 3, 0, time.UTC),
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

func TestListCachedGraphsPreservesOriginalGraphID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph with spaces", "v1", "hello"))

	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].ID != "graph with spaces" {
		t.Fatalf("listCachedGraphs() = %#v, want original graph id", graphs)
	}
}

func TestListCachedGraphsIgnoresIncompleteSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
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
}

func TestGraphStorageSeparatesSanitizedIDCollisions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	first := publishGraphForHashTest(t, engine, triggerGraphUploadBody("graph/a", "v1", "first"))
	second := publishGraphForHashTest(t, engine, triggerGraphUploadBody("graph?a", "v1", "second"))
	if filepath.Dir(first.RunnerBaseDir) == filepath.Dir(second.RunnerBaseDir) {
		t.Fatalf("colliding graph IDs share storage directory %q", filepath.Dir(first.RunnerBaseDir))
	}

	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 2 || graphs[0].ID != "graph/a" || graphs[1].ID != "graph?a" {
		t.Fatalf("listCachedGraphs() = %#v, want two isolated graph IDs", graphs)
	}
	for _, graph := range graphs {
		if graph.SessionCount != 1 {
			t.Fatalf("graph summary = %#v, want one session", graph)
		}
	}

	resolved, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph/a"})
	if err != nil {
		t.Fatal(err)
	}
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID != first.Graph.GraphSessionID {
		t.Fatalf("resolved first graph session = %q, want %q", runner.GraphSessionID, first.Graph.GraphSessionID)
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
	uploaded := publishGraphForHashTest(t, engine, triggerGraphUploadBody("CON", "v1", "reserved"))
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

	resolver, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "CON"})
	if err != nil {
		t.Fatal(err)
	}
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID != uploaded.Graph.GraphSessionID {
		t.Fatalf("resolved reserved graph session = %q, want %q", runner.GraphSessionID, uploaded.Graph.GraphSessionID)
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
	started := decodeRunResultResponse(t, serveHTTP(engine, http.MethodPost, "/runs", `{}`), http.StatusOK)
	runPath := filepath.Join(uploaded.RunnerBaseDir, "execution", "runs", started.Run.RunID+".json")
	if err := os.WriteFile(runPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheOnly, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	cacheEngine := gin.New()
	cacheOnly.RegisterRoutes(cacheEngine.Group(""))
	for _, path := range []string{
		"/runs/" + started.Run.RunID + "?graph_id=graph-a",
		"/runs?graph_id=graph-a",
	} {
		response := serveHTTP(cacheEngine, http.MethodGet, path, "")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
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
		response := serveHTTP(engine, http.MethodPut, "/graph", `{
			"definition": {
				"version": "2.0",
				"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
				"entry_point": "missing",
				"finish_point": "missing",
				"nodes": [{"id":"missing","type":"not_registered","state":{}}]
			}
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
		response := serveHTTP(engine, http.MethodPut, "/graph", triggerGraphUploadBody("graph-a", "v1", "hello"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("storage failure status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}
