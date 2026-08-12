package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	Settings     *graphRuntimeSettingsRequest
	Triggers     []triggerPayload
}

type graphUploadEnvelope struct {
	Definition   json.RawMessage              `json:"definition"`
	GraphVersion string                       `json:"graph_version"`
	Settings     *graphRuntimeSettingsRequest `json:"settings"`
	Triggers     []triggerPayload             `json:"triggers,omitempty"`
}

type graphLoadResponse struct {
	Graph         graphInfo               `json:"graph"`
	Definition    dsl.GraphDefinition     `json:"definition"`
	RunnerBaseDir string                  `json:"runner_base_dir,omitempty"`
	Settings      graphRuntimeSettings    `json:"settings"`
	Warnings      []runtime.WarningRecord `json:"warnings,omitempty"`
}

type graphSessionManifest struct {
	GraphID             string    `json:"graph_id"`
	GraphName           string    `json:"graph_name,omitempty"`
	GraphVersion        string    `json:"graph_version"`
	NodeCount           int       `json:"node_count"`
	GraphHash           string    `json:"graph_hash"`
	GraphSnapshotHash   string    `json:"graph_snapshot_hash"`
	GraphSessionID      string    `json:"graph_session_id"`
	DefinitionPath      string    `json:"definition_path"`
	SettingsPath        string    `json:"settings_path"`
	RuntimeSettingsHash string    `json:"runtime_settings_hash"`
	CreatedAt           time.Time `json:"created_at"`
}

const (
	maxGraphUploadBodyBytes   int64 = 8 << 20
	retainedGraphSessionCount       = 5
)

func (s *Server) handleCreateGraphSession(c *gin.Context) {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	req, err := bindGraphUpload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	req.GraphID = graphID
	if req.Settings == nil {
		writeError(c, http.StatusBadRequest, fmt.Errorf("graph upload settings are required"))
		return
	}

	resp, err := s.configureGraph(req)
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
		GraphVersion: strings.TrimSpace(envelope.GraphVersion),
		Settings:     envelope.Settings,
		Triggers:     envelope.Triggers,
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

func (s *Server) configureGraph(req graphUploadRequest) (graphLoadResponse, error) {
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

	previousSettings, err := s.runtimeSettingsForGraph(graphID)
	if err != nil {
		return graphLoadResponse{}, err
	}
	nextSettings := previousSettings
	apiKey, apiKeyProvided, err := applyGraphSettingsRequest(&nextSettings, *req.Settings)
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("%w: %v", errInvalidRequest, err)
	}
	if !apiKeyProvided {
		apiKey = firstNonEmpty(firstGraphModelAPIKey(nextSettings), nextSettings.Environment["OPENAI_API_KEY"], os.Getenv("OPENAI_API_KEY"))
	}
	markGraphModelAPIKeys(&nextSettings, apiKey)
	baseContext, err := s.buildRuntimeContext(nextSettings, apiKey)
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("%w: %v", errInvalidRequest, err)
	}
	runtimeSettingsHash, err := graphRuntimeSettingsHash(nextSettings)
	if err != nil {
		return graphLoadResponse{}, err
	}

	current := s.runtime.currentSession()
	currentGraph := current.graph
	currentRunner := current.runner
	if graphUploadMatchesSession(current, graphID, graphVersion, graphHash, graphSnapshotHash, runtimeSettingsHash) {
		runnerBaseDir := s.uploadedGraphBaseDir(graphID, currentRunner.GraphSessionID())
		if err := s.pruneGraphSessions(graphID, currentRunner.GraphSessionID()); err != nil {
			return graphLoadResponse{}, err
		}
		if currentGraph == nil {
			currentGraph = graph
		}
		return graphResponse(currentGraph, currentRunner, runnerBaseDir, current.settings)
	}

	return s.installUploadedGraph(graph, def, graphID, graphVersion, graphHash, graphSnapshotHash, runtimeSettingsHash, nextSettings, baseContext)
}

func (s *Server) installUploadedGraph(
	graph *wfgraph.Graph,
	def dsl.GraphDefinition,
	graphID string,
	graphVersion string,
	graphHash string,
	graphSnapshotHash string,
	runtimeSettingsHash string,
	settings graphRuntimeSettings,
	baseContext context.Context,
) (graphLoadResponse, error) {
	runnerBaseDir := s.nextUploadedGraphBaseDir(graphID)
	graphSessionID := graphSessionIDFromBaseDir(runnerBaseDir)
	if err := writeGraphSessionSnapshot(runnerBaseDir, graphSessionManifest{
		GraphID:             graphID,
		GraphName:           strings.TrimSpace(def.Name),
		GraphVersion:        graphVersion,
		NodeCount:           len(def.Nodes),
		GraphHash:           graphHash,
		GraphSnapshotHash:   graphSnapshotHash,
		GraphSessionID:      graphSessionID,
		DefinitionPath:      "definition.json",
		SettingsPath:        graphRuntimeSettingsFileName,
		RuntimeSettingsHash: runtimeSettingsHash,
		CreatedAt:           time.Now().UTC(),
	}, def, settings); err != nil {
		return graphLoadResponse{}, err
	}
	if err := s.pruneGraphSessions(graphID, graphSessionID); err != nil {
		if cleanupErr := os.RemoveAll(runnerBaseDir); cleanupErr != nil {
			return graphLoadResponse{}, fmt.Errorf("prune graph sessions: %v; remove new graph session: %w", err, cleanupErr)
		}
		return graphLoadResponse{}, fmt.Errorf("prune graph sessions: %w", err)
	}

	cfg := s.cfg
	cfg.Graph = graph
	cfg.GraphID = graphID
	cfg.GraphVersion = graphVersion
	cfg.GraphHash = graphHash
	cfg.GraphSnapshotHash = graphSnapshotHash
	cfg.GraphSessionID = graphSessionID
	runner, err := newDefaultRunner(graph, cfg, s.graphHistoryBaseDir(graphID), s.events)
	if err != nil {
		return graphLoadResponse{}, err
	}

	s.runtime.installSession(graphRuntimeSession{
		graph:       graph,
		runner:      runner,
		baseContext: baseContext,
		settings:    settings,
	})

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
		Settings:      graphSettingsResponse(settings),
		Warnings:      runner.StartupWarnings(),
	}, nil
}

func graphUploadMatchesSession(
	session graphRuntimeSession,
	graphID string,
	graphVersion string,
	graphHash string,
	graphSnapshotHash string,
	runtimeSettingsHash string,
) bool {
	runner := session.runner
	if runner == nil {
		return false
	}
	settingsHash, err := graphRuntimeSettingsHash(session.settings)
	if err != nil || settingsHash != strings.TrimSpace(runtimeSettingsHash) {
		return false
	}
	return effectiveRunnerGraphID(runner) == strings.TrimSpace(graphID) &&
		firstNonEmpty(runner.GraphVersion(), runtime.DefaultGraphVersion) == strings.TrimSpace(graphVersion) &&
		strings.TrimSpace(runner.GraphHash()) == strings.TrimSpace(graphHash) &&
		strings.TrimSpace(runner.GraphSnapshotHash()) == strings.TrimSpace(graphSnapshotHash)
}

func graphResponse(
	graph *wfgraph.Graph,
	runner *runtime.GraphRunner,
	runnerBaseDir string,
	settings graphRuntimeSettings,
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
			Version:           firstNonEmpty(runner.GraphVersion(), runtime.DefaultGraphVersion),
			GraphHash:         strings.TrimSpace(runner.GraphHash()),
			GraphSnapshotHash: strings.TrimSpace(runner.GraphSnapshotHash()),
			GraphSessionID:    strings.TrimSpace(runner.GraphSessionID()),
			EntryPoint:        def.EntryPoint,
			FinishPoint:       def.FinishPoint,
		},
		Definition:    def,
		RunnerBaseDir: runnerBaseDir,
		Settings:      graphSettingsResponse(settings),
		Warnings:      runner.StartupWarnings(),
	}, nil
}

func (s *Server) uploadedGraphBaseDir(graphID string, graphSessionID string) string {
	if s == nil || strings.TrimSpace(s.baseDir) == "" || strings.TrimSpace(graphSessionID) == "" {
		return ""
	}
	return filepath.Join(s.baseDir, "graphs", graphStorageKey(graphID), strings.TrimSpace(graphSessionID))
}

func (s *Server) graphHistoryBaseDir(graphID string) string {
	if s == nil || strings.TrimSpace(s.baseDir) == "" {
		return ""
	}
	return filepath.Join(graphStorageDirectory(s.baseDir, graphID), "history")
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

func writeGraphSessionSnapshot(baseDir string, manifest graphSessionManifest, def dsl.GraphDefinition, settings graphRuntimeSettings) error {
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
	if err := persistGraphRuntimeSettings(baseDir, settings); err != nil {
		return fmt.Errorf("write graph runtime settings snapshot: %w", err)
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

func graphRuntimeSettingsHash(settings graphRuntimeSettings) (string, error) {
	data, err := encodeGraphRuntimeSettings(settings)
	if err != nil {
		return "", err
	}
	return graphRuntimeSettingsDataHash(data), nil
}

func graphRuntimeSettingsDataHash(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:])
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

func (s *Server) pruneGraphSessions(graphID string, protectedSessionID string) error {
	if s == nil || strings.TrimSpace(s.baseDir) == "" {
		return nil
	}
	protectedSessionID = strings.TrimSpace(protectedSessionID)
	activeSessionIDs := map[string]struct{}{}
	if s.runtime != nil {
		activeSessionIDs = s.runtime.activeSessionIDs(graphID)
	}
	resumableSessionIDs, err := s.resumableRunSessionIDs(graphID)
	if err != nil {
		return err
	}
	for sessionID := range resumableSessionIDs {
		activeSessionIDs[sessionID] = struct{}{}
	}

	graphDir := graphStorageDirectory(s.baseDir, graphID)
	entries, err := os.ReadDir(graphDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read graph sessions: %w", err)
	}

	type graphSessionCandidate struct {
		id        string
		createdAt time.Time
	}
	candidates := make([]graphSessionCandidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, complete, err := readCachedGraphSession(graphDir, entry.Name())
		if err != nil {
			return fmt.Errorf("inspect graph session %q: %w", entry.Name(), err)
		}
		if !complete || manifest.GraphID != strings.TrimSpace(graphID) {
			continue
		}
		candidates = append(candidates, graphSessionCandidate{
			id:        entry.Name(),
			createdAt: manifest.CreatedAt,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].id == protectedSessionID {
			return true
		}
		if candidates[j].id == protectedSessionID {
			return false
		}
		if candidates[i].createdAt.Equal(candidates[j].createdAt) {
			return candidates[i].id > candidates[j].id
		}
		return candidates[i].createdAt.After(candidates[j].createdAt)
	})

	for _, candidate := range candidates[min(retainedGraphSessionCount, len(candidates)):] {
		if _, active := activeSessionIDs[candidate.id]; active {
			continue
		}
		if err := os.RemoveAll(filepath.Join(graphDir, candidate.id)); err != nil {
			return fmt.Errorf("remove graph session %q: %w", candidate.id, err)
		}
		if s.runtime != nil {
			s.runtime.removeSession(graphID, candidate.id)
		}
	}
	return nil
}

func (s *Server) resumableRunSessionIDs(graphID string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	graphDir := graphStorageDirectory(s.baseDir, graphID)
	if strings.TrimSpace(graphDir) == "" {
		return result, nil
	}
	storeDirs := []string{filepath.Join(graphDir, "history")}
	entries, err := os.ReadDir(graphDir)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan graph sessions for resumable runs: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "history" {
			storeDirs = append(storeDirs, filepath.Join(graphDir, entry.Name()))
		}
	}
	for _, storeDir := range storeDirs {
		store := runtime.NewFileExecutionStore(filepath.Join(storeDir, "execution"))
		runs, listErr := store.ListRuns(context.Background(), runtime.RunFilter{Statuses: []runtime.RunStatus{
			runtime.RunStatusPending,
			runtime.RunStatusRunning,
			runtime.RunStatusPaused,
		}})
		if listErr != nil {
			return nil, fmt.Errorf("scan resumable runs in %q: %w", storeDir, listErr)
		}
		for _, run := range runs {
			if sessionID := strings.TrimSpace(run.GraphSessionID); sessionID != "" {
				result[sessionID] = struct{}{}
			}
		}
	}
	return result, nil
}
