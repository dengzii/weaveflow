package neo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"weaveflow/runtime"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ReplayServer handles graph debug replay API requests.
type ReplayServer struct {
	defaultCacheDir string
	hub             *LiveHub
}

type replayAPIEnvelope struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// NewReplayServer creates a ReplayServer with the given default cache directory.
func NewReplayServer(defaultCacheDir string, hub *LiveHub) *ReplayServer {
	return &ReplayServer{
		defaultCacheDir: strings.TrimSpace(defaultCacheDir),
		hub:             hub,
	}
}

// RegisterGinRoutes registers replay API routes on the given Gin group.
// Typically mounted at /api so that /api/runs and /api/run/* are reachable.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

func (s *ReplayServer) RegisterGinRoutes(group *gin.RouterGroup) {
	group.GET("/runs", s.handleRuns)
	group.GET("/run/*path", s.handleRunRoute)
	group.GET("/live", s.handleLive)
	group.GET("/files", s.handleFiles)
	group.GET("/file/*path", s.handleFile)
	group.GET("/ws", s.handleLiveWS)
}

func (s *ReplayServer) handleRuns(c *gin.Context) {
	explorer, err := newCacheExplorer(s.requestedCacheDir(c))
	if err != nil {
		replayWriteAPIError(c, http.StatusBadRequest, err)
		return
	}

	runs, err := explorer.listRuns(c.Request.Context())
	if err != nil {
		replayWriteAPIError(c, http.StatusInternalServerError, err)
		return
	}

	replayWriteAPIData(c, http.StatusOK, RunsResponse{
		CacheDir: explorer.baseDir,
		Sources:  explorer.sourceMetas(),
		Runs:     runs,
	})
}

func (s *ReplayServer) handleRunRoute(c *gin.Context) {
	explorer, err := newCacheExplorer(s.requestedCacheDir(c))
	if err != nil {
		replayWriteAPIError(c, http.StatusBadRequest, err)
		return
	}

	// c.Param("path") is like /abc123 or /abc123/checkpoint/xyz
	runID, routeType, entityID, err := parseRunRoutePath(c.Param("path"))
	if err != nil {
		replayWriteAPIError(c, http.StatusBadRequest, err)
		return
	}

	sourceID := strings.TrimSpace(c.Query("source"))
	switch routeType {
	case "run":
		data, err := explorer.loadRunDetail(c.Request.Context(), runID, sourceID)
		if err != nil {
			replayWriteLookupError(c, err)
			return
		}
		replayWriteAPIData(c, http.StatusOK, data)
	case "checkpoint":
		data, err := explorer.loadCheckpointDetail(c.Request.Context(), runID, sourceID, entityID)
		if err != nil {
			replayWriteLookupError(c, err)
			return
		}
		replayWriteAPIData(c, http.StatusOK, data)
	case "artifact":
		if c.Query("download") == "1" {
			artifact, _, err := explorer.loadArtifactRaw(c.Request.Context(), runID, sourceID, entityID)
			if err != nil {
				replayWriteLookupError(c, err)
				return
			}
			replayWriteArtifactBinary(c, artifact)
			return
		}
		data, err := explorer.loadArtifactDetail(c.Request.Context(), runID, sourceID, entityID)
		if err != nil {
			replayWriteLookupError(c, err)
			return
		}
		replayWriteAPIData(c, http.StatusOK, data)
	default:
		replayWriteAPIError(c, http.StatusBadRequest, fmt.Errorf("unsupported route"))
	}
}

func (s *ReplayServer) handleFiles(c *gin.Context) {
	cacheDir := s.requestedCacheDir(c)
	explorer, err := newCacheExplorer(cacheDir)
	if err != nil {
		replayWriteAPIError(c, http.StatusBadRequest, err)
		return
	}

	files, err := listCacheFiles(c.Request.Context(), explorer.baseDir)
	if err != nil {
		replayWriteAPIError(c, http.StatusInternalServerError, err)
		return
	}

	replayWriteAPIData(c, http.StatusOK, CacheFilesResponse{
		CacheDir: explorer.baseDir,
		Files:    files,
	})
}

func (s *ReplayServer) handleFile(c *gin.Context) {
	cacheDir := s.requestedCacheDir(c)
	explorer, err := newCacheExplorer(cacheDir)
	if err != nil {
		replayWriteAPIError(c, http.StatusBadRequest, err)
		return
	}

	detail, err := loadCacheFileDetail(explorer.baseDir, strings.TrimPrefix(c.Param("path"), "/"))
	if err != nil {
		replayWriteLookupError(c, err)
		return
	}
	replayWriteAPIData(c, http.StatusOK, detail)
}

func (s *ReplayServer) handleLive(c *gin.Context) {
	if s.hub == nil {
		replayWriteAPIData(c, http.StatusOK, LiveState{})
		return
	}
	replayWriteAPIData(c, http.StatusOK, s.hub.Snapshot())
}

func (s *ReplayServer) requestedCacheDir(c *gin.Context) string {
	cacheDir := strings.TrimSpace(c.Query("cache_dir"))
	if cacheDir != "" {
		return cacheDir
	}
	return s.defaultCacheDir
}

// parseRunRoutePath parses the Gin wildcard path (e.g. "/abc123" or "/abc123/checkpoint/xyz").
func parseRunRoutePath(path string) (runID string, routeType string, entityID string, err error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return "", "", "", fmt.Errorf("run id is required")
	}

	parts := strings.Split(path, "/")
	for i, part := range parts {
		decoded, decodeErr := url.PathUnescape(part)
		if decodeErr != nil {
			return "", "", "", decodeErr
		}
		parts[i] = decoded
	}

	switch len(parts) {
	case 1:
		return parts[0], "run", "", nil
	case 3:
		switch parts[1] {
		case "checkpoint":
			return parts[0], "checkpoint", parts[2], nil
		case "artifact":
			return parts[0], "artifact", parts[2], nil
		}
	}
	return "", "", "", fmt.Errorf("invalid run route")
}

func replayWriteAPIData(c *gin.Context, status int, data any) {
	replayWriteJSON(c, status, replayAPIEnvelope{Data: data})
}

func replayWriteAPIError(c *gin.Context, status int, err error) {
	replayWriteJSON(c, status, replayAPIEnvelope{Error: err.Error()})
}

func replayWriteLookupError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if err != nil && (strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "required")) {
		status = http.StatusNotFound
	}
	replayWriteAPIError(c, status, err)
}

func replayWriteJSON(c *gin.Context, status int, payload replayAPIEnvelope) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(status)
	encoder := json.NewEncoder(c.Writer)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(payload)
}

func (s *ReplayServer) handleLiveWS(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	if s.hub == nil {
		_ = conn.WriteJSON(LiveMsg{Type: "idle"})
		return
	}

	ch, unsub := s.hub.Subscribe()
	defer unsub()

	// Drain any client messages (we ignore them but must read to handle pings/close frames).
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}
}

func replayWriteArtifactBinary(c *gin.Context, artifact runtime.Artifact) {
	contentType := strings.TrimSpace(artifact.MIMEType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", artifact.ID))
	c.Data(http.StatusOK, contentType, artifact.Data)
}
