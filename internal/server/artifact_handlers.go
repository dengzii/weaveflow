package server

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"

	"github.com/gin-gonic/gin"
)

type artifactResponse struct {
	Artifact runtime.Artifact `json:"artifact"`
	Size     int              `json:"size"`
	Text     string           `json:"text,omitempty"`
	Data     string           `json:"data_base64,omitempty"`
}

func (s *Server) handleListArtifacts(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id is required"))
		return
	}
	artifacts, err := reader.ListArtifacts(c.Request.Context(), runID)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, artifacts)
}

func (s *Server) handleGetArtifact(c *gin.Context) {
	reader := s.resolveRunReader(c)
	if reader == nil {
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	artifactID := strings.TrimSpace(c.Param("artifact_id"))
	if runID == "" || artifactID == "" {
		writeError(c, http.StatusBadRequest, fmt.Errorf("run_id and artifact_id are required"))
		return
	}
	artifact, err := reader.LoadArtifact(c.Request.Context(), state.ArtifactRef{RunID: runID, ID: artifactID})
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	if artifact.ID == "" && len(artifact.Data) == 0 {
		writeError(c, http.StatusNotFound, runtime.ErrRunnerRecordNotFound)
		return
	}
	if shouldSendRawArtifact(c) {
		contentType := artifact.MIMEType
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/octet-stream"
		}
		if isTruthy(c.Query("download")) {
			c.Header("Content-Disposition", "attachment; filename="+artifactFilename(artifact))
		}
		c.Data(http.StatusOK, contentType, artifact.Data)
		return
	}
	writeData(c, http.StatusOK, buildArtifactResponse(artifact))
}

func shouldSendRawArtifact(c *gin.Context) bool {
	return isTruthy(c.Query("raw")) || isTruthy(c.Query("download"))
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func buildArtifactResponse(artifact runtime.Artifact) artifactResponse {
	resp := artifactResponse{
		Artifact: artifact,
		Size:     len(artifact.Data),
	}
	contentType := strings.ToLower(strings.TrimSpace(artifact.MIMEType))
	if isTextContent(contentType, artifact.Data) {
		resp.Text = string(artifact.Data)
		return resp
	}
	if len(artifact.Data) > 0 {
		resp.Data = base64.StdEncoding.EncodeToString(artifact.Data)
	}
	return resp
}

func isTextContent(contentType string, data []byte) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	switch contentType {
	case "application/json", "application/xml", "application/javascript", "application/x-yaml", "application/yaml":
		return true
	}
	if contentType == "" {
		return utf8.Valid(data)
	}
	return false
}

func artifactFilename(artifact runtime.Artifact) string {
	name := strings.TrimSpace(artifact.ID)
	if name == "" {
		name = "artifact"
	}
	if ext := extensionForMimeType(artifact.MIMEType); ext != "" && !strings.HasSuffix(name, ext) {
		name += ext
	}
	return fmt.Sprintf("%q", name)
}

func extensionForMimeType(mimeType string) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		return ""
	}
	extensions, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(extensions) == 0 {
		return ""
	}
	return extensions[0]
}
