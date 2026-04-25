package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"weaveflow/runtime"
)

//go:embed static/*
var staticFiles embed.FS

type appServer struct {
	defaultCacheDir string
	indexTemplate   *template.Template
	staticHandler   http.Handler
}

type indexViewData struct {
	DefaultCacheDir string
}

type apiEnvelope struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func newAppServer(defaultCacheDir string) (*appServer, error) {
	indexTemplate, err := template.ParseFS(staticFiles, "static/index.html")
	if err != nil {
		return nil, err
	}

	staticDir, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	return &appServer{
		defaultCacheDir: strings.TrimSpace(defaultCacheDir),
		indexTemplate:   indexTemplate,
		staticHandler:   http.StripPrefix("/static/", http.FileServer(http.FS(staticDir))),
	}, nil
}

func (s *appServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", s.staticHandler)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/run/", s.handleRunRoute)
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

func (s *appServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTemplate.Execute(w, indexViewData{
		DefaultCacheDir: s.requestedCacheDir(r),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *appServer) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	explorer, err := newCacheExplorer(s.requestedCacheDir(r))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	runs, err := explorer.listRuns(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}

	writeAPIData(w, http.StatusOK, RunsResponse{
		CacheDir: explorer.baseDir,
		Sources:  explorer.sourceMetas(),
		Runs:     runs,
	})
}

func (s *appServer) handleRunRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	explorer, err := newCacheExplorer(s.requestedCacheDir(r))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	runID, routeType, entityID, err := parseRunRoute(r.URL.Path)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	sourceID := strings.TrimSpace(r.URL.Query().Get("source"))
	switch routeType {
	case "run":
		data, err := explorer.loadRunDetail(r.Context(), runID, sourceID)
		if err != nil {
			writeLookupError(w, err)
			return
		}
		writeAPIData(w, http.StatusOK, data)
	case "checkpoint":
		data, err := explorer.loadCheckpointDetail(r.Context(), runID, sourceID, entityID)
		if err != nil {
			writeLookupError(w, err)
			return
		}
		writeAPIData(w, http.StatusOK, data)
	case "artifact":
		if r.URL.Query().Get("download") == "1" {
			artifact, _, err := explorer.loadArtifactRaw(r.Context(), runID, sourceID, entityID)
			if err != nil {
				writeLookupError(w, err)
				return
			}
			writeArtifactBinary(w, artifact)
			return
		}

		data, err := explorer.loadArtifactDetail(r.Context(), runID, sourceID, entityID)
		if err != nil {
			writeLookupError(w, err)
			return
		}
		writeAPIData(w, http.StatusOK, data)
	default:
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("unsupported route"))
	}
}

func (s *appServer) requestedCacheDir(r *http.Request) string {
	cacheDir := strings.TrimSpace(r.URL.Query().Get("cache_dir"))
	if cacheDir != "" {
		return cacheDir
	}
	return s.defaultCacheDir
}

func parseRunRoute(path string) (runID string, routeType string, entityID string, err error) {
	raw := strings.TrimPrefix(path, "/api/run/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", "", "", fmt.Errorf("run id is required")
	}

	parts := strings.Split(raw, "/")
	for index, part := range parts {
		decoded, decodeErr := url.PathUnescape(part)
		if decodeErr != nil {
			return "", "", "", decodeErr
		}
		parts[index] = decoded
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

func writeAPIData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, apiEnvelope{Data: data})
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiEnvelope{Error: err.Error()})
}

func writeLookupError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if err != nil && (strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "required")) {
		status = http.StatusNotFound
	}
	writeAPIError(w, status, err)
}

func writeJSON(w http.ResponseWriter, status int, payload apiEnvelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(payload)
}

func writeArtifactBinary(w http.ResponseWriter, artifact runtime.Artifact) {
	contentType := strings.TrimSpace(artifact.MIMEType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", artifact.ID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(artifact.Data)
}
