package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/internal/assistant"
	"github.com/gin-gonic/gin"
)

func (s *Server) registerAssistantRoutes(group *gin.RouterGroup) {
	group.GET("/assistant/status", s.handleAssistantStatus)
	group.POST("/assistant/sessions/:session_id/messages", s.handleAssistantSubmit)
	group.GET("/assistant/sessions/:session_id", s.handleAssistantSession)
	group.GET("/assistant/jobs/:job_id", s.handleAssistantJob)
	group.GET("/assistant/jobs/:job_id/stream", s.handleAssistantJobStream)
}

func (s *Server) handleAssistantStatus(c *gin.Context) {
	configured := s != nil && s.assistant != nil && s.assistant.Configured()
	writeData(c, http.StatusOK, map[string]any{"enabled": configured})
}

func (s *Server) handleAssistantSubmit(c *gin.Context) {
	if s == nil || s.assistant == nil || !s.assistant.Configured() {
		writeError(c, http.StatusNotImplemented, assistant.ErrNotConfigured)
		return
	}
	var request assistant.SubmitRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	request.SessionID = strings.TrimSpace(c.Param("session_id"))
	job, err := s.assistant.Submit(request)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, assistant.ErrClosed) {
			status = http.StatusServiceUnavailable
		}
		writeError(c, status, err)
		return
	}
	writeData(c, http.StatusAccepted, job)
}

func (s *Server) handleAssistantJob(c *gin.Context) {
	if s == nil || s.assistant == nil || !s.assistant.Configured() {
		writeError(c, http.StatusNotImplemented, assistant.ErrNotConfigured)
		return
	}
	job, err := s.assistant.GetJob(c.Param("job_id"))
	if err != nil {
		writeError(c, http.StatusNotFound, err)
		return
	}
	writeData(c, http.StatusOK, job)
}

func (s *Server) handleAssistantJobStream(c *gin.Context) {
	if s == nil || s.assistant == nil || !s.assistant.Configured() {
		writeError(c, http.StatusNotImplemented, assistant.ErrNotConfigured)
		return
	}
	updates, unsubscribe, err := s.assistant.WatchJob(c.Param("job_id"))
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, assistant.ErrClosed) {
			status = http.StatusServiceUnavailable
		}
		writeError(c, status, err)
		return
	}
	defer unsubscribe()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case job, ok := <-updates:
			if !ok {
				return
			}
			if err := writeSSEJSON(c.Writer, "", "", job); err != nil {
				return
			}
			if job.Status == "completed" || job.Status == "failed" {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(c.Writer); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleAssistantSession(c *gin.Context) {
	if s == nil || s.assistant == nil || !s.assistant.Configured() {
		writeError(c, http.StatusNotImplemented, assistant.ErrNotConfigured)
		return
	}
	writeData(c, http.StatusOK, s.assistant.GetSession(c.Param("session_id")))
}
