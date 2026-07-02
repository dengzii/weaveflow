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

type graphSessionManifest struct {
	GraphID           string    `json:"graph_id"`
	GraphVersion      string    `json:"graph_version"`
	GraphHash         string    `json:"graph_hash"`
	GraphSnapshotHash string    `json:"graph_snapshot_hash"`
	GraphSessionID    string    `json:"graph_session_id"`
	DefinitionPath    string    `json:"definition_path"`
	CreatedAt         time.Time `json:"created_at"`
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
	graphSessionID := graphSessionIDFromBaseDir(runnerBaseDir)
	graphHash, err := dsl.SemanticGraphHash(def)
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("hash graph definition: %w", err)
	}
	graphSnapshotHash, err := dsl.SnapshotGraphHash(def)
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("hash graph snapshot: %w", err)
	}
	if err := writeGraphSessionSnapshot(runnerBaseDir, graphSessionManifest{
		GraphID:           graphID,
		GraphVersion:      graphVersion,
		GraphHash:         graphHash,
		GraphSnapshotHash: graphSnapshotHash,
		GraphSessionID:    graphSessionID,
		DefinitionPath:    "definition.json",
		CreatedAt:         time.Now().UTC(),
	}, def); err != nil {
		return graphLoadResponse{}, err
	}

	cfg := s.cfg
	cfg.Graph = graph
	cfg.GraphID = graphID
	cfg.GraphVersion = graphVersion
	cfg.GraphHash = graphHash
	cfg.GraphSnapshotHash = graphSnapshotHash
	cfg.GraphSessionID = graphSessionID
	runner := newDefaultRunner(graph, cfg, runnerBaseDir)
	attachEventHub(runner, s.events)

	s.mu.Lock()
	s.graph = graph
	s.runner = runner
	s.mu.Unlock()

	return graphLoadResponse{
		Graph: graphInfo{
			ID:                graphID,
			Version:           graphVersion,
			GraphHash:         graphHash,
			GraphSnapshotHash: graphSnapshotHash,
			GraphSessionID:    graphSessionID,
			EntryPoint:        def.EntryPoint,
			FinishPoint:       def.FinishPoint,
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

func graphSessionIDFromBaseDir(baseDir string) string {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return ""
	}
	return strings.TrimSpace(filepath.Base(baseDir))
}

func writeGraphSessionSnapshot(baseDir string, manifest graphSessionManifest, def dsl.GraphDefinition) error {
	if strings.TrimSpace(baseDir) == "" {
		return nil
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	definition, err := def.Serialize()
	if err != nil {
		return fmt.Errorf("serialize graph definition snapshot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, manifest.DefinitionPath), definition, 0o644); err != nil {
		return fmt.Errorf("write graph definition snapshot: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize graph session manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(baseDir, "graph.json"), data, 0o644); err != nil {
		return fmt.Errorf("write graph session manifest: %w", err)
	}
	return nil
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
