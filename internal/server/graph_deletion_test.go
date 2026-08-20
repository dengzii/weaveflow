package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/internal/memory"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
)

func TestDeleteGraphCascadesStoredRuntimeAndTriggers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDirectory := t.TempDir()
	testServer, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testServer.Close() })
	engine := gin.New()
	testServer.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("delete-graph", "v1", "hello"))
	replaced := serveHTTP(engine, http.MethodPut, "/graphs/delete-graph/triggers", `{"triggers":[{"id":"delete-hook","type":"webhook","enabled":true,"webhook":{}}]}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace triggers status = %d, body = %s", replaced.Code, replaced.Body.String())
	}
	started := startGraphRunForTest(t, engine, uploaded, `{}`)
	activeRunner := testServer.runtime.session("delete-graph", uploaded.Graph.GraphSessionID).runner
	waitForRunTerminalStatus(t, activeRunner, started.RunID)
	deadline := time.Now().Add(2 * time.Second)
	for activeRunner.ActiveRunCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if activeRunner.ActiveRunCount() != 0 {
		t.Fatal("run remained active after reaching terminal status")
	}
	if _, err := testServer.MemoryStore().Put(context.Background(), memory.Namespace("user"), memory.MemoryRecord{
		Key: "profile", Content: "retained", SourceRunID: started.RunID,
	}, ""); err != nil {
		t.Fatalf("store source memory: %v", err)
	}

	response := serveHTTP(engine, http.MethodDelete, "/graphs/delete-graph", "")
	if response.Code != http.StatusOK {
		t.Fatalf("delete graph status = %d, body = %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data graphDeletionResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Data.GraphID != "delete-graph" || decoded.Data.DeletedTriggerCount != 1 {
		t.Fatalf("delete response = %#v", decoded.Data)
	}
	if _, err := os.Stat(graphStorageDirectory(baseDirectory, "delete-graph")); !os.IsNotExist(err) {
		t.Fatalf("graph storage still exists: %v", err)
	}
	if cached := testServer.runtime.session("delete-graph", uploaded.Graph.GraphSessionID); cached.runner != nil {
		t.Fatal("deleted graph session remained cached")
	}
	items, err := testServer.triggers.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("deleted graph triggers = %#v", items)
	}
	for key := range testServer.events.partitions {
		if key.GraphID == "delete-graph" {
			t.Fatalf("deleted graph event partition remained: %#v", key)
		}
	}
	if detail := serveHTTP(engine, http.MethodGet, "/graphs/delete-graph", ""); detail.Code != http.StatusNotFound {
		t.Fatalf("deleted graph detail status = %d, body = %s", detail.Code, detail.Body.String())
	}
	if inspection := serveHTTP(engine, http.MethodGet, "/graphs/delete-graph/runs/"+started.RunID+"/inspection", ""); inspection.Code != http.StatusNotFound {
		t.Fatalf("deleted run inspection status = %d, body = %s", inspection.Code, inspection.Body.String())
	}
	if _, err := testServer.MemoryStore().Get(context.Background(), memory.Namespace("user"), "profile"); err != nil {
		t.Fatalf("source memory after graph deletion = %v, want retained record", err)
	}
}

func TestDeleteGraphRejectsActiveRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testServer, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testServer.Close() })
	engine := gin.New()
	testServer.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("active-graph", "v1", "hello"))

	startedExecution := make(chan struct{})
	releaseExecution := make(chan struct{})
	workflow := newRunControlTestGraph(t, startedExecution, releaseExecution, true)
	memoryStore := runtime.NewMemoryRuntimeStore()
	activeRunner, err := newRunnerWithStore(workflow, Config{
		GraphID:          "active-graph",
		GraphVersion:     "v1",
		GraphSessionID:   uploaded.Graph.GraphSessionID,
		ExecutionStore:   memoryStore,
		CheckpointStore:  memoryStore,
		ArtifactStore:    runtime.NewMemoryArtifactStore(),
		EventSink:        memoryStore,
		TransactionStore: memoryStore,
	}, testServer.baseDir, testServer.events, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	testServer.runtime.installSession(graphRuntimeSession{graph: workflow, runner: activeRunner, baseContext: context.Background()})
	_, done, err := activeRunner.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatal(err)
	}
	<-startedExecution

	response := serveHTTP(engine, http.MethodDelete, "/graphs/active-graph", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("delete active graph status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(graphStorageDirectory(testServer.baseDir, "active-graph")); err != nil {
		t.Fatalf("active graph storage was removed: %v", err)
	}
	close(releaseExecution)
	<-done
}
