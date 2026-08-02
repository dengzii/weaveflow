package server

import (
	"crypto/sha256"
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

type graphUploadEnvelope struct {
	Definition   json.RawMessage `json:"definition"`
	GraphID      string          `json:"graph_id"`
	GraphVersion string          `json:"graph_version"`
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
	Official          bool      `json:"official,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

const maxGraphUploadBodyBytes int64 = 8 << 20

func (s *Server) handleUpdateGraph(c *gin.Context) {
	req, err := bindGraphUpload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}

	resp, err := s.configureUploadedGraph(req)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, resp)
}

func (s *Server) handlePublishGraph(c *gin.Context) {
	req, err := bindGraphUpload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}

	resp, err := s.configurePushedGraph(req)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, resp)
}

func bindGraphUpload(c *gin.Context) (graphUploadRequest, error) {
	body, err := readRequestBody(c.Request.Body, maxGraphUploadBodyBytes)
	if err != nil {
		return graphUploadRequest{}, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return graphUploadRequest{}, fmt.Errorf("graph definition is required")
	}

	var envelope graphUploadEnvelope
	if err := decodeStrictJSON(body, &envelope); err != nil {
		return graphUploadRequest{}, fmt.Errorf("invalid graph upload: %w", err)
	}
	if len(envelope.Definition) == 0 {
		return graphUploadRequest{}, fmt.Errorf("graph upload definition is required")
	}
	definition, err := dsl.DeserializeGraphDefinition(envelope.Definition)
	if err != nil {
		return graphUploadRequest{}, fmt.Errorf("invalid definition: %w", err)
	}
	return graphUploadRequest{
		Definition:   definition,
		GraphID:      strings.TrimSpace(envelope.GraphID),
		GraphVersion: strings.TrimSpace(envelope.GraphVersion),
	}, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON body contains multiple values")
		}
		return err
	}
	return nil
}

func (s *Server) configureUploadedGraph(req graphUploadRequest) (graphLoadResponse, error) {
	return s.configureGraph(req, false)
}

func (s *Server) configurePushedGraph(req graphUploadRequest) (graphLoadResponse, error) {
	return s.configureGraph(req, true)
}

func (s *Server) configureGraph(req graphUploadRequest, official bool) (graphLoadResponse, error) {
	if s == nil {
		return graphLoadResponse{}, errGraphNotConfigured
	}
	s.runtime.graphUpdateMu.Lock()
	defer s.runtime.graphUpdateMu.Unlock()

	if s.registry == nil {
		return graphLoadResponse{}, errRegistryNotConfigured
	}

	graph, err := wfgraph.NewBuilder(s.registry).Build(req.Definition, &wfregistry.BuildContext{})
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("%w: %v", errInvalidGraphDefinition, err)
	}
	def, err := graph.Definition()
	if err != nil {
		return graphLoadResponse{}, err
	}

	graphID := firstNonEmpty(req.GraphID, metadataString(def.Metadata, "id"), strings.TrimSpace(def.Name), s.cfg.GraphID, "graph")
	graphVersion := firstNonEmpty(req.GraphVersion, metadataString(def.Metadata, "graph_version"), s.cfg.GraphVersion, runtime.DefaultGraphVersion)
	graphHash, err := graph.SemanticHash()
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("hash graph definition: %w", err)
	}
	graphSnapshotHash, err := graph.SnapshotHash()
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("hash graph snapshot: %w", err)
	}

	// Reuse identical graph sessions. Publishing an active draft only changes
	// its Official flag so repeated runs and pushes do not create new versions.
	currentGraph, currentRunner, currentOfficial := s.currentGraphState()
	if graphUploadMatchesRunner(currentRunner, graphID, graphVersion, graphHash, graphSnapshotHash) {
		runnerBaseDir := s.uploadedGraphBaseDir(graphID, currentRunner.GraphSessionID)
		if official && !currentOfficial {
			if strings.TrimSpace(currentRunner.GraphSessionID) == "" {
				return s.installUploadedGraph(graph, def, graphID, graphVersion, graphHash, graphSnapshotHash, true)
			}
			var err error
			runnerBaseDir, err = s.promoteGraphSession(graphID, currentRunner.GraphSessionID)
			if err != nil {
				return graphLoadResponse{}, err
			}
			s.runtime.promoteCurrentSession(graphID, currentRunner)
			currentOfficial = true
		}
		if currentGraph == nil {
			currentGraph = graph
		}
		return graphResponse(currentGraph, currentRunner, runnerBaseDir, currentOfficial)
	}

	return s.installUploadedGraph(graph, def, graphID, graphVersion, graphHash, graphSnapshotHash, official)
}

func (s *Server) installUploadedGraph(
	graph *wfgraph.Graph,
	def dsl.GraphDefinition,
	graphID string,
	graphVersion string,
	graphHash string,
	graphSnapshotHash string,
	official bool,
) (graphLoadResponse, error) {
	runnerBaseDir := s.nextUploadedGraphBaseDir(graphID)
	graphSessionID := graphSessionIDFromBaseDir(runnerBaseDir)
	if err := writeGraphSessionSnapshot(runnerBaseDir, graphSessionManifest{
		GraphID:           graphID,
		GraphVersion:      graphVersion,
		GraphHash:         graphHash,
		GraphSnapshotHash: graphSnapshotHash,
		GraphSessionID:    graphSessionID,
		DefinitionPath:    "definition.json",
		Official:          official,
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

	s.runtime.installSession(graphRuntimeSession{
		graph:    graph,
		runner:   runner,
		official: official,
	})

	return graphLoadResponse{
		Graph: graphInfo{
			ID:                graphID,
			Version:           graphVersion,
			GraphHash:         graphHash,
			GraphSnapshotHash: graphSnapshotHash,
			GraphSessionID:    graphSessionID,
			Official:          official,
			EntryPoint:        def.EntryPoint,
			FinishPoint:       def.FinishPoint,
		},
		Definition:    def,
		RunnerBaseDir: runnerBaseDir,
		Warnings:      runner.StartupWarnings,
	}, nil
}

func graphUploadMatchesRunner(
	runner *runtime.GraphRunner,
	graphID string,
	graphVersion string,
	graphHash string,
	graphSnapshotHash string,
) bool {
	if runner == nil {
		return false
	}
	return effectiveRunnerGraphID(runner) == strings.TrimSpace(graphID) &&
		firstNonEmpty(runner.GraphVersion, runtime.DefaultGraphVersion) == strings.TrimSpace(graphVersion) &&
		strings.TrimSpace(runner.GraphHash) == strings.TrimSpace(graphHash) &&
		strings.TrimSpace(runner.GraphSnapshotHash) == strings.TrimSpace(graphSnapshotHash)
}

func graphResponse(
	graph *wfgraph.Graph,
	runner *runtime.GraphRunner,
	runnerBaseDir string,
	official bool,
) (graphLoadResponse, error) {
	if graph == nil || runner == nil {
		return graphLoadResponse{}, errGraphNotConfigured
	}
	def, err := graph.Definition()
	if err != nil {
		return graphLoadResponse{}, err
	}
	return graphLoadResponse{
		Graph: graphInfo{
			ID:                effectiveRunnerGraphID(runner),
			Version:           firstNonEmpty(runner.GraphVersion, runtime.DefaultGraphVersion),
			GraphHash:         strings.TrimSpace(runner.GraphHash),
			GraphSnapshotHash: strings.TrimSpace(runner.GraphSnapshotHash),
			GraphSessionID:    strings.TrimSpace(runner.GraphSessionID),
			Official:          official,
			EntryPoint:        def.EntryPoint,
			FinishPoint:       def.FinishPoint,
		},
		Definition:    def,
		RunnerBaseDir: runnerBaseDir,
		Warnings:      runner.StartupWarnings,
	}, nil
}

func (s *Server) uploadedGraphBaseDir(graphID string, graphSessionID string) string {
	if s == nil || strings.TrimSpace(s.baseDir) == "" || strings.TrimSpace(graphSessionID) == "" {
		return ""
	}
	return filepath.Join(s.baseDir, "graphs", graphStorageKey(graphID), strings.TrimSpace(graphSessionID))
}

func (s *Server) promoteGraphSession(graphID string, graphSessionID string) (string, error) {
	graphDir := graphStorageDirectory(s.baseDir, graphID)
	manifest, complete, err := readCachedGraphSession(graphDir, graphSessionID)
	if err != nil {
		return "", err
	}
	if !complete || manifest.GraphID != graphID {
		return "", fmt.Errorf("promote graph session %q: session not found", graphSessionID)
	}
	baseDir := filepath.Join(graphDir, graphSessionID)
	if manifest.Official {
		return baseDir, nil
	}
	manifest.Official = true
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serialize graph session manifest: %w", err)
	}
	data = append(data, '\n')
	if err := writeGraphSessionFile(filepath.Join(baseDir, "graph.json"), data); err != nil {
		return "", fmt.Errorf("promote graph session %q: %w", graphSessionID, err)
	}
	return baseDir, nil
}

func (s *Server) nextUploadedGraphBaseDir(graphID string) string {
	if s == nil || strings.TrimSpace(s.baseDir) == "" {
		return ""
	}
	return filepath.Join(s.baseDir, "graphs", graphStorageKey(graphID), time.Now().UTC().Format("20060102T150405.000000000Z"))
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
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(baseDir, 0o700); err != nil {
		return err
	}
	definition, err := def.Serialize()
	if err != nil {
		return fmt.Errorf("serialize graph definition snapshot: %w", err)
	}
	if err := writeGraphSessionFile(filepath.Join(baseDir, manifest.DefinitionPath), definition); err != nil {
		return fmt.Errorf("write graph definition snapshot: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize graph session manifest: %w", err)
	}
	data = append(data, '\n')
	if err := writeGraphSessionFile(filepath.Join(baseDir, "graph.json"), data); err != nil {
		return fmt.Errorf("write graph session manifest: %w", err)
	}
	return nil
}

func writeGraphSessionFile(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".graph-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
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
	runes := []rune(out)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return out
}

func graphStorageKey(graphID string) string {
	graphID = strings.TrimSpace(graphID)
	safe := safePathSegment(graphID)
	if graphID == safe && !isReservedGraphStorageKey(safe) {
		return safe
	}
	hash := sha256.Sum256([]byte(graphID))
	suffix := fmt.Sprintf("%x", hash[:6])
	runes := []rune(safe)
	maxPrefix := 80 - len(suffix) - 1
	if len(runes) > maxPrefix {
		runes = runes[:maxPrefix]
	}
	return string(runes) + "-" + suffix
}

func isReservedGraphStorageKey(value string) bool {
	base := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func graphStorageDirectory(baseDir string, graphID string) string {
	return filepath.Join(baseDir, "graphs", graphStorageKey(graphID))
}
