package neo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestReplayServerLiveWSWaitsAcrossRuns(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	hub := NewLiveHub()
	server := NewReplayServer(t.TempDir(), hub)

	router := gin.New()
	server.RegisterGinRoutes(router.Group("/api"))

	httpServer := httptest.NewServer(router)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	idle := readLiveMsg(t, conn)
	if idle.Type != "idle" {
		t.Fatalf("first live message type = %q, want %q", idle.Type, "idle")
	}

	hub.StartRun(json.RawMessage(`{"id":"graph-one"}`), "Neo Agent", "graph-one")
	snapshotOne := readLiveMsg(t, conn)
	if snapshotOne.Type != "snapshot" {
		t.Fatalf("snapshotOne.Type = %q, want %q", snapshotOne.Type, "snapshot")
	}
	if snapshotOne.GraphRef != "graph-one" {
		t.Fatalf("snapshotOne.GraphRef = %q, want %q", snapshotOne.GraphRef, "graph-one")
	}

	hub.Done()
	done := readLiveMsg(t, conn)
	if done.Type != "done" {
		t.Fatalf("done.Type = %q, want %q", done.Type, "done")
	}

	hub.StartRun(json.RawMessage(`{"id":"graph-two"}`), "Neo Agent", "graph-two")
	snapshotTwo := readLiveMsg(t, conn)
	if snapshotTwo.Type != "snapshot" {
		t.Fatalf("snapshotTwo.Type = %q, want %q", snapshotTwo.Type, "snapshot")
	}
	if snapshotTwo.GraphRef != "graph-two" {
		t.Fatalf("snapshotTwo.GraphRef = %q, want %q", snapshotTwo.GraphRef, "graph-two")
	}
}

func TestReplayServerLiveSnapshotReturnsGraphInfo(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	hub := NewLiveHub()
	server := NewReplayServer(t.TempDir(), hub)

	router := gin.New()
	server.RegisterGinRoutes(router.Group("/api"))

	idleReq := httptest.NewRequest(http.MethodGet, "/api/live", nil)
	idleResp := httptest.NewRecorder()
	router.ServeHTTP(idleResp, idleReq)

	if idleResp.Code != http.StatusOK {
		t.Fatalf("idle live status = %d, want %d", idleResp.Code, http.StatusOK)
	}
	idle := decodeLiveStateResponse(t, idleResp)
	if idle.Running {
		t.Fatalf("idle.Running = true, want false")
	}

	hub.StartRun(json.RawMessage(`{"id":"graph-one","nodes":[{"id":"start"}],"edges":[]}`), "Neo Agent", "graph-one")

	runningReq := httptest.NewRequest(http.MethodGet, "/api/live", nil)
	runningResp := httptest.NewRecorder()
	router.ServeHTTP(runningResp, runningReq)

	if runningResp.Code != http.StatusOK {
		t.Fatalf("running live status = %d, want %d", runningResp.Code, http.StatusOK)
	}
	running := decodeLiveStateResponse(t, runningResp)
	if !running.Running {
		t.Fatalf("running.Running = false, want true")
	}
	if running.GraphRef != "graph-one" {
		t.Fatalf("running.GraphRef = %q, want %q", running.GraphRef, "graph-one")
	}
	var runningGraph struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(running.Graph, &runningGraph); err != nil {
		t.Fatalf("json.Unmarshal(running.Graph) error = %v", err)
	}
	if runningGraph.ID != "graph-one" {
		t.Fatalf("runningGraph.ID = %q, want %q", runningGraph.ID, "graph-one")
	}

	hub.Done()

	doneReq := httptest.NewRequest(http.MethodGet, "/api/live", nil)
	doneResp := httptest.NewRecorder()
	router.ServeHTTP(doneResp, doneReq)

	if doneResp.Code != http.StatusOK {
		t.Fatalf("done live status = %d, want %d", doneResp.Code, http.StatusOK)
	}
	done := decodeLiveStateResponse(t, doneResp)
	if done.Running {
		t.Fatalf("done.Running = true, want false")
	}
	if done.GraphRef != "graph-one" {
		t.Fatalf("done.GraphRef = %q, want %q", done.GraphRef, "graph-one")
	}
	var doneGraph struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(done.Graph, &doneGraph); err != nil {
		t.Fatalf("json.Unmarshal(done.Graph) error = %v", err)
	}
	if doneGraph.ID != "graph-one" {
		t.Fatalf("doneGraph.ID = %q, want %q", doneGraph.ID, "graph-one")
	}
}

func readLiveMsg(t *testing.T, conn *websocket.Conn) LiveMsg {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	var msg LiveMsg
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	return msg
}

func decodeLiveStateResponse(t *testing.T, recorder *httptest.ResponseRecorder) LiveState {
	t.Helper()

	var payload struct {
		Data  LiveState `json:"data"`
		Error string    `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Error != "" {
		t.Fatalf("payload.Error = %q, want empty", payload.Error)
	}
	return payload.Data
}
