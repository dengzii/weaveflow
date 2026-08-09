package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/state"
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

func TestGraphUploadStoresSettingsInTheSameSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	first := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "hello"))
	second := putGraphForHashTest(t, engine, graphUploadBodyWithSettings(
		"graph-a",
		"v1",
		"hello",
		`{"environment":{"MODE":"second"},"models":[],"memory":{"enabled":false}}`,
	))
	if second.Graph.GraphSessionID == first.Graph.GraphSessionID {
		t.Fatalf("settings change reused session %q", second.Graph.GraphSessionID)
	}
	if second.Settings.Environment["MODE"] != "second" {
		t.Fatalf("upload response settings = %#v, want MODE=second", second.Settings)
	}

	manifestData, err := os.ReadFile(filepath.Join(second.RunnerBaseDir, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest graphSessionManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SettingsPath != graphRuntimeSettingsFileName || manifest.RuntimeSettingsHash == "" {
		t.Fatalf("graph session manifest settings = %#v", manifest)
	}
	settings, found, err := loadGraphRuntimeSettings(second.RunnerBaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if !found || settings.Environment["MODE"] != "second" {
		t.Fatalf("stored settings = %#v, found=%t", settings, found)
	}
	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 1 || graphs[0].SessionCount != 2 {
		t.Fatalf("cached graphs = %#v, want two sessions", graphs)
	}
	detail := decodeGraphDetailResponse(t, serveHTTP(engine, http.MethodGet, "/graphs/graph-a", ""), http.StatusOK)
	if detail.Settings.Environment["MODE"] != "second" {
		t.Fatalf("graph detail settings = %#v, want MODE=second", detail.Settings)
	}
	resolved, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID() != second.Graph.GraphSessionID {
		t.Fatalf("trigger runner session = %q, want %q", runner.GraphSessionID(), second.Graph.GraphSessionID)
	}
}

func TestGraphUploadRetainsFiveLatestSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	uploads := make([]graphLoadResponse, 0, retainedGraphSessionCount+2)
	for version := 1; version <= retainedGraphSessionCount+2; version++ {
		if version > 1 {
			time.Sleep(time.Millisecond)
		}
		uploads = append(uploads, putGraphForHashTest(t, engine, triggerGraphUploadBody(
			"graph-a",
			fmt.Sprintf("v%d", version),
			fmt.Sprintf("content-%d", version),
		)))
	}

	removedCount := len(uploads) - retainedGraphSessionCount
	for index, upload := range uploads {
		_, statErr := os.Stat(upload.RunnerBaseDir)
		if index < removedCount {
			if !os.IsNotExist(statErr) {
				t.Fatalf("old graph session %q still exists: %v", upload.Graph.GraphSessionID, statErr)
			}
			continue
		}
		if statErr != nil {
			t.Fatalf("retained graph session %q: %v", upload.Graph.GraphSessionID, statErr)
		}
	}

	graphs, err := srv.listCachedGraphs()
	if err != nil {
		t.Fatal(err)
	}
	latest := uploads[len(uploads)-1]
	if len(graphs) != 1 || graphs[0].SessionCount != retainedGraphSessionCount || graphs[0].LatestSession != latest.Graph.GraphSessionID {
		t.Fatalf("cached graphs = %#v, want %d sessions ending at %q", graphs, retainedGraphSessionCount, latest.Graph.GraphSessionID)
	}
}

func TestGraphUploadRetainsActiveHistoricalSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	first := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "content-1"))
	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	var done <-chan struct{}
	defer func() {
		if !released {
			close(release)
		}
		if done != nil {
			<-done
		}
	}()
	activeGraph := newRunControlTestGraph(t, started, release, true)
	activeRunner := mustNewDefaultRunner(t, activeGraph, Config{
		GraphID:           first.Graph.ID,
		GraphVersion:      first.Graph.Version,
		GraphHash:         first.Graph.GraphHash,
		GraphSnapshotHash: first.Graph.GraphSnapshotHash,
		GraphSessionID:    first.Graph.GraphSessionID,
	}, first.RunnerBaseDir, nil)
	srv.runtime.removeSession(first.Graph.ID, first.Graph.GraphSessionID)
	srv.runtime.installSession(graphRuntimeSession{
		graph:       activeGraph,
		runner:      activeRunner,
		baseContext: context.Background(),
	})
	_, done, err = activeRunner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, started, "historical run node start")

	uploads := []graphLoadResponse{first}
	for version := 2; version <= retainedGraphSessionCount+2; version++ {
		time.Sleep(time.Millisecond)
		uploads = append(uploads, putGraphForHashTest(t, engine, triggerGraphUploadBody(
			"graph-a",
			fmt.Sprintf("v%d", version),
			fmt.Sprintf("content-%d", version),
		)))
	}
	if _, err := os.Stat(first.RunnerBaseDir); err != nil {
		t.Fatalf("active historical session was pruned: %v", err)
	}
	if _, err := os.Stat(uploads[1].RunnerBaseDir); !os.IsNotExist(err) {
		t.Fatalf("old inactive session still exists: %v", err)
	}

	close(release)
	released = true
	waitForSignal(t, done, "historical run completion")
	latest := uploads[len(uploads)-1]
	if err := srv.pruneGraphSessions("graph-a", latest.Graph.GraphSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.RunnerBaseDir); !os.IsNotExist(err) {
		t.Fatalf("inactive historical session was not pruned: %v", err)
	}
	if cached := srv.runtime.session("graph-a", first.Graph.GraphSessionID); cached.runner != nil {
		t.Fatal("pruned historical session remained in runtime manager")
	}
}
