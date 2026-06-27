package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"

	"github.com/gin-gonic/gin"
)

type graphUploadRequest struct {
	Definition   dsl.GraphDefinition
	GraphID      string
	GraphVersion string
}

type graphLoadResponse struct {
	Graph         graphInfo               `json:"graph"`
	Definition    dsl.GraphDefinition     `json:"definition"`
	RunnerBaseDir string                  `json:"runner_base_dir,omitempty"`
	Warnings      []runtime.WarningRecord `json:"warnings,omitempty"`
}

func (s *Server) handleSetGraph(c *gin.Context) {
	req, err := bindGraphUpload(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	resp, err := s.configureUploadedGraph(req)
	if err != nil {
		status := statusForError(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadRequest
		}
		writeError(c, status, err)
		return
	}
	writeData(c, http.StatusOK, resp)
}

func bindGraphUpload(c *gin.Context) (graphUploadRequest, error) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return graphUploadRequest{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return graphUploadRequest{}, fmt.Errorf("graph definition is required")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return graphUploadRequest{}, fmt.Errorf("invalid JSON body: %w", err)
	}

	req := graphUploadRequest{}
	req.GraphID = stringField(raw, "graph_id", "id")
	req.GraphVersion = stringField(raw, "graph_version")

	switch {
	case len(raw["definition"]) > 0:
		if err := json.Unmarshal(raw["definition"], &req.Definition); err != nil {
			return graphUploadRequest{}, fmt.Errorf("invalid definition: %w", err)
		}
	case len(raw["graph"]) > 0:
		if err := json.Unmarshal(raw["graph"], &req.Definition); err != nil {
			return graphUploadRequest{}, fmt.Errorf("invalid graph: %w", err)
		}
	default:
		if err := json.Unmarshal(body, &req.Definition); err != nil {
			return graphUploadRequest{}, fmt.Errorf("invalid graph definition: %w", err)
		}
	}
	return req, nil
}

func (s *Server) configureUploadedGraph(req graphUploadRequest) (graphLoadResponse, error) {
	if s == nil {
		return graphLoadResponse{}, errGraphNotConfigured
	}
	if s.registry == nil {
		return graphLoadResponse{}, errRegistryNotConfigured
	}

	graph, err := wfgraph.BuildGraph(s.registry, req.Definition, &wfregistry.BuildContext{})
	if err != nil {
		return graphLoadResponse{}, err
	}
	def, err := graph.Definition()
	if err != nil {
		return graphLoadResponse{}, err
	}

	graphID := firstNonEmpty(req.GraphID, metadataString(def.Metadata, "id"), strings.TrimSpace(def.Name), s.cfg.GraphID, "graph")
	graphVersion := firstNonEmpty(req.GraphVersion, metadataString(def.Metadata, "graph_version"), s.cfg.GraphVersion, runtime.DefaultGraphVersion)
	runnerBaseDir := s.nextUploadedGraphBaseDir(graphID)

	cfg := s.cfg
	cfg.Graph = graph
	cfg.GraphID = graphID
	cfg.GraphVersion = graphVersion
	runner := newDefaultRunner(graph, cfg, runnerBaseDir)
	attachEventHub(runner, s.events)

	s.mu.Lock()
	s.graph = graph
	s.runner = runner
	s.mu.Unlock()

	return graphLoadResponse{
		Graph: graphInfo{
			ID:          graphID,
			Version:     graphVersion,
			EntryPoint:  def.EntryPoint,
			FinishPoint: def.FinishPoint,
		},
		Definition:    def,
		RunnerBaseDir: runnerBaseDir,
		Warnings:      runner.StartupWarnings,
	}, nil
}

func (s *Server) nextUploadedGraphBaseDir(graphID string) string {
	if s == nil || strings.TrimSpace(s.baseDir) == "" {
		return ""
	}
	base := filepath.Join(s.baseDir, "graphs", safePathSegment(graphID), time.Now().UTC().Format("20060102T150405.000000000Z"))
	_ = os.MkdirAll(base, 0o755)
	return base
}

func stringField(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if len(raw[key]) == 0 {
			continue
		}
		var text string
		if err := json.Unmarshal(raw[key], &text); err == nil {
			if text = strings.TrimSpace(text); text != "" {
				return text
			}
		}
	}
	return ""
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	text, _ := metadata[key].(string)
	return strings.TrimSpace(text)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "graph"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ". ")
	if out == "" {
		return "graph"
	}
	if len(out) > 80 {
		return out[:80]
	}
	return out
}
