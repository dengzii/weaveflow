package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

func TestRepeatedGraphUploadReusesCurrentSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	body := triggerGraphUploadBody("graph-a", "v1", "hello")

	first := putGraphForHashTest(t, engine, body)
	firstRunner := srv.currentRunner()
	second := putGraphForHashTest(t, engine, body)

	if second.Graph.GraphSessionID != first.Graph.GraphSessionID {
		t.Fatalf("repeated upload session = %q, want %q", second.Graph.GraphSessionID, first.Graph.GraphSessionID)
	}
	if second.RunnerBaseDir != first.RunnerBaseDir {
		t.Fatalf("repeated upload base dir = %q, want %q", second.RunnerBaseDir, first.RunnerBaseDir)
	}
	if srv.currentRunner() != firstRunner {
		t.Fatal("repeated upload replaced the current runner")
	}
	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].SessionCount != 1 {
		t.Fatalf("cached graphs = %#v, want one session", graphs)
	}
}

func TestPublishPromotesMatchingDraftWithoutCreatingSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	body := triggerGraphUploadBody("graph-a", "v1", "hello")

	draft := putGraphForHashTest(t, engine, body)
	published := publishGraphForHashTest(t, engine, body)
	repeated := publishGraphForHashTest(t, engine, body)
	runUpload := putGraphForHashTest(t, engine, body)

	if !published.Graph.Official || !repeated.Graph.Official {
		t.Fatalf("publish responses were not official: first=%t repeated=%t", published.Graph.Official, repeated.Graph.Official)
	}
	if published.Graph.GraphSessionID != draft.Graph.GraphSessionID || repeated.Graph.GraphSessionID != draft.Graph.GraphSessionID {
		t.Fatalf(
			"publish sessions = %q and %q, want draft session %q",
			published.Graph.GraphSessionID,
			repeated.Graph.GraphSessionID,
			draft.Graph.GraphSessionID,
		)
	}
	if !runUpload.Graph.Official || runUpload.Graph.GraphSessionID != draft.Graph.GraphSessionID {
		t.Fatalf(
			"run upload returned official=%t session=%q, want official session %q",
			runUpload.Graph.Official,
			runUpload.Graph.GraphSessionID,
			draft.Graph.GraphSessionID,
		)
	}
	manifestData, err := os.ReadFile(filepath.Join(draft.RunnerBaseDir, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest graphSessionManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Official {
		t.Fatal("draft session manifest was not promoted")
	}
	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].SessionCount != 1 {
		t.Fatalf("cached graphs = %#v, want one promoted session", graphs)
	}
	resolved, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID != draft.Graph.GraphSessionID {
		t.Fatalf("trigger runner session = %q, want %q", runner.GraphSessionID, draft.Graph.GraphSessionID)
	}
}
