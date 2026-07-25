package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dengzii/weaveflow/trigger"
	"github.com/gin-gonic/gin"
)

func TestListCachedGraphsPreservesOriginalGraphID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	postGraphForHashTest(t, engine, triggerGraphUploadBody("graph with spaces", "v1", "hello"))

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
	uploaded := postGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "hello"))
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
	first := postGraphForHashTest(t, engine, triggerGraphUploadBody("graph/a", "v1", "first"))
	second := postGraphForHashTest(t, engine, triggerGraphUploadBody("graph?a", "v1", "second"))
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
	uploaded := postGraphForHashTest(t, engine, triggerGraphUploadBody("CON", "v1", "reserved"))
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
	uploaded := postGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "hello"))
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
		response := serveHTTP(engine, http.MethodPost, "/graph", `{
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
		response := serveHTTP(engine, http.MethodPost, "/graph", triggerGraphUploadBody("graph-a", "v1", "hello"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("storage failure status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}
