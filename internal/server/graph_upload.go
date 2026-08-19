package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	"github.com/dengzii/weaveflow/internal/trigger"
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
	Mode         graphCommitMode
	ExpectedHead string
	RequestID    string
}

type graphCommitMode string

const (
	graphCommitCreate    graphCommitMode = "create"
	graphCommitOverwrite graphCommitMode = "overwrite"
)

type graphUploadEnvelope struct {
	Definition   json.RawMessage              `json:"definition"`
	GraphVersion string                       `json:"graph_version"`
	Settings     *graphRuntimeSettingsRequest `json:"settings"`
	Triggers     []triggerPayload             `json:"triggers,omitempty"`
	Mode         graphCommitMode              `json:"mode,omitempty"`
	ExpectedHead string                       `json:"expected_graph_session_id,omitempty"`
	RequestID    string                       `json:"request_id"`
}

type graphLoadResponse struct {
	Graph         graphInfo               `json:"graph"`
	Definition    dsl.GraphDefinition     `json:"definition"`
	RunnerBaseDir string                  `json:"runner_base_dir,omitempty"`
	Settings      graphRuntimeSettings    `json:"settings"`
	Warnings      []runtime.WarningRecord `json:"warnings,omitempty"`
	Triggers      []trigger.Trigger       `json:"triggers"`
	TriggerIDMap  map[string]string       `json:"trigger_id_mapping,omitempty"`
	candidateDir  string
}

type graphCommitJournal struct {
	GraphID          string            `json:"graph_id"`
	GraphSessionID   string            `json:"graph_session_id"`
	RequestID        string            `json:"request_id"`
	Response         graphLoadResponse `json:"response"`
	PreviousTriggers []trigger.Trigger `json:"previous_triggers"`
}

type graphHeadConflictError struct {
	Expected string
	Current  graphSessionManifest
}

func (conflict *graphHeadConflictError) Error() string {
	return fmt.Sprintf("%v: expected %q, current %q", errGraphHeadConflict, conflict.Expected, conflict.Current.GraphSessionID)
}

func (conflict *graphHeadConflictError) Unwrap() error {
	return errGraphHeadConflict
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
	if receipt, found, err := s.loadGraphCommitReceipt(graphID, req.RequestID); err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	} else if found {
		writeData(c, http.StatusOK, receipt)
		return
	}

	resp, err := s.commitGraph(c.Request.Context(), setupRequestOwner(c), req)
	if err != nil {
		var conflict *graphHeadConflictError
		if errors.As(err, &conflict) {
			writeErrorData(c, http.StatusConflict, err, map[string]any{
				"current_head": graphSessionSummary{ID: conflict.Current.GraphSessionID, CreatedAt: conflict.Current.CreatedAt},
			})
			return
		}
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, resp)
}

func (s *Server) commitGraph(ctx context.Context, setupOwner string, req graphUploadRequest) (graphLoadResponse, error) {
	if s == nil || s.triggers == nil {
		return graphLoadResponse{}, errRunnerNotConfigured
	}
	s.chatSetupSaveMu.Lock()
	defer s.chatSetupSaveMu.Unlock()

	items := make([]trigger.Trigger, 0, len(req.Triggers))
	releases := make([]func(bool), 0, len(req.Triggers)*2)
	committed := false
	defer func() {
		for _, release := range releases {
			release(committed)
		}
	}()
	for _, itemPayload := range req.Triggers {
		item := itemPayload.toTrigger(req.GraphID)
		if err := normalizeTriggerCredential(&item); err != nil {
			return graphLoadResponse{}, err
		}
		setupRelease, err := s.applyChatSetup(ctx, setupOwner, itemPayload.ChatSetupSessionID, &item)
		if err != nil {
			return graphLoadResponse{}, err
		}
		releases = append(releases, setupRelease)
		secretRelease, err := s.externalizeChatChannelSecrets(ctx, &item)
		if err != nil {
			return graphLoadResponse{}, err
		}
		releases = append(releases, secretRelease)
		items = append(items, item)
	}

	replacement, idMapping, err := s.triggers.PrepareGraphReplacement(
		ctx,
		req.GraphID,
		items,
		req.Mode == graphCommitCreate,
	)
	if err != nil {
		return graphLoadResponse{}, err
	}
	defer func() { _ = replacement.Rollback(context.WithoutCancel(ctx)) }()
	if len(idMapping) > 0 {
		req.Definition = remapDefinitionTriggerIDs(req.Definition, idMapping)
	}

	response, err := s.configureGraphWithPublish(ctx, req, func(candidate graphLoadResponse) error {
		replacement.SetGraphSessionID(candidate.Graph.GraphSessionID)
		session := s.runtime.session(candidate.Graph.ID, candidate.Graph.GraphSessionID)
		if session.graph == nil {
			return fmt.Errorf("candidate graph session %q is unavailable", candidate.Graph.GraphSessionID)
		}
		if err := validateGraphTriggerState(session.graph, replacement.Items()); err != nil {
			return err
		}
		candidate.Triggers = make([]trigger.Trigger, 0, len(replacement.Items()))
		for _, item := range replacement.Items() {
			candidate.Triggers = append(candidate.Triggers, s.publicTrigger(item))
		}
		candidate.TriggerIDMap = idMapping
		if err := s.writeGraphCommitJournal(candidate, req.RequestID, replacement.PreviousItems()); err != nil {
			return err
		}
		return replacement.Persist(ctx)
	})
	if err != nil {
		_ = s.removeGraphCommitJournal(req.GraphID)
		return graphLoadResponse{}, err
	}
	response.Triggers = make([]trigger.Trigger, 0, len(replacement.Items()))
	for _, item := range replacement.Items() {
		response.Triggers = append(response.Triggers, s.publicTrigger(item))
	}
	response.TriggerIDMap = idMapping
	receiptWritten := true
	if err := s.writeGraphCommitReceipt(req.GraphID, req.RequestID, response); err != nil {
		receiptWritten = false
		response.Warnings = append(response.Warnings, runtime.WarningRecord{
			Code:    "graph_commit_receipt_failed",
			Message: err.Error(),
		})
	}
	if receiptWritten {
		if err := s.removeGraphCommitJournal(req.GraphID); err != nil {
			response.Warnings = append(response.Warnings, runtime.WarningRecord{
				Code:    "graph_commit_journal_cleanup_failed",
				Message: err.Error(),
			})
		}
	}
	replacement.Commit()
	committed = true
	if err := s.sweepManagedSecrets(context.WithoutCancel(ctx)); err != nil {
		response.Warnings = append(response.Warnings, runtime.WarningRecord{
			Code:    "managed_secret_cleanup_failed",
			Message: err.Error(),
		})
	}
	return response, nil
}

func remapDefinitionTriggerIDs(definition dsl.GraphDefinition, idMapping map[string]string) dsl.GraphDefinition {
	if len(idMapping) == 0 || definition.Metadata == nil {
		return definition
	}
	web, ok := definition.Metadata["web"].(map[string]any)
	if !ok {
		return definition
	}
	triggerNodes, ok := web["trigger_nodes"].(map[string]any)
	if !ok {
		return definition
	}
	remapped := make(map[string]any, len(triggerNodes))
	for triggerID, position := range triggerNodes {
		if nextID := strings.TrimSpace(idMapping[triggerID]); nextID != "" {
			remapped[nextID] = position
		} else {
			remapped[triggerID] = position
		}
	}
	metadata := make(map[string]any, len(definition.Metadata))
	for key, value := range definition.Metadata {
		metadata[key] = value
	}
	remappedWeb := make(map[string]any, len(web))
	for key, value := range web {
		remappedWeb[key] = value
	}
	remappedWeb["trigger_nodes"] = remapped
	metadata["web"] = remappedWeb
	definition.Metadata = metadata
	return definition
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
	if envelope.Mode != graphCommitCreate && envelope.Mode != graphCommitOverwrite {
		return graphUploadRequest{}, invalidRequestf("mode must be create or overwrite")
	}
	if envelope.Triggers == nil {
		return graphUploadRequest{}, invalidRequestf("triggers is required")
	}
	if envelope.Settings == nil {
		return graphUploadRequest{}, invalidRequestf("settings is required")
	}
	envelope.RequestID = strings.TrimSpace(envelope.RequestID)
	if envelope.RequestID == "" {
		return graphUploadRequest{}, invalidRequestf("request_id is required")
	}
	if len(envelope.RequestID) > 200 {
		return graphUploadRequest{}, invalidRequestf("request_id exceeds 200 characters")
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
		Mode:         envelope.Mode,
		ExpectedHead: strings.TrimSpace(envelope.ExpectedHead),
		RequestID:    envelope.RequestID,
	}, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
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

func (s *Server) configureGraphWithPublish(
	ctx context.Context,
	req graphUploadRequest,
	publish func(graphLoadResponse) error,
) (graphLoadResponse, error) {
	if s == nil {
		return graphLoadResponse{}, errGraphNotConfigured
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.runtime.graphUpdateMu.Lock()
	defer s.runtime.graphUpdateMu.Unlock()
	if s.registry == nil {
		return graphLoadResponse{}, errRegistryNotConfigured
	}
	if err := s.validateGraphCommitHead(req); err != nil {
		return graphLoadResponse{}, err
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
	settingsRequest := *req.Settings
	settingsRequest.Models = append([]graphModelSettingsRequest(nil), req.Settings.Models...)
	for index := range settingsRequest.Models {
		settingsRequest.Models[index].CredentialValue = ""
	}
	if err := applyGraphSettingsRequest(&nextSettings, settingsRequest); err != nil {
		return graphLoadResponse{}, fmt.Errorf("%w: %v", errInvalidRequest, err)
	}
	credentialRelease, err := s.applyGraphModelCredentialChanges(ctx, req.Settings, nextSettings)
	if err != nil {
		return graphLoadResponse{}, err
	}
	committed := false
	defer func() { credentialRelease(committed) }()
	baseContext, err := s.buildRuntimeContext(nextSettings)
	if err != nil {
		return graphLoadResponse{}, fmt.Errorf("%w: %v", errInvalidRequest, err)
	}
	runtimeSettingsHash, err := graphRuntimeSettingsHash(nextSettings)
	if err != nil {
		return graphLoadResponse{}, err
	}

	current := s.runtime.currentSession()
	previous := current
	currentGraph := current.graph
	currentRunner := current.runner
	var response graphLoadResponse
	if graphUploadMatchesSession(current, graphID, graphVersion, graphHash, graphSnapshotHash, runtimeSettingsHash) {
		runnerBaseDir := s.uploadedGraphBaseDir(graphID, currentRunner.GraphSessionID())
		if currentGraph == nil {
			currentGraph = graph
		}
		current.graph = currentGraph
		current.baseContext = baseContext
		current.settings = nextSettings
		s.runtime.refreshSession(current)
		response, err = s.graphResponse(currentGraph, currentRunner, runnerBaseDir, nextSettings)
	} else {
		response, err = s.installUploadedGraph(graph, def, graphID, graphVersion, graphHash, graphSnapshotHash, runtimeSettingsHash, nextSettings, baseContext, true)
	}
	if err != nil {
		return graphLoadResponse{}, err
	}
	if publish != nil {
		if err := publish(response); err != nil {
			s.rollbackConfiguredGraph(previous, response)
			return graphLoadResponse{}, err
		}
	}
	if err := s.publishGraphCandidate(response); err != nil {
		s.rollbackConfiguredGraph(previous, response)
		return graphLoadResponse{}, err
	}
	if err := s.pruneGraphSessions(graphID, response.Graph.GraphSessionID); err != nil {
		response.Warnings = append(response.Warnings, runtime.WarningRecord{
			Code:    "graph_session_prune_failed",
			Message: fmt.Sprintf("prune graph sessions: %v", err),
		})
	}
	committed = true
	return response, nil
}

func (s *Server) validateGraphCommitHead(req graphUploadRequest) error {
	if req.Mode == "" {
		return nil
	}
	graphID := strings.TrimSpace(req.GraphID)
	latest, err := s.latestGraphSession(graphID)
	switch req.Mode {
	case graphCommitCreate:
		if err == nil {
			return fmt.Errorf("%w: graph %q currently points to session %q", errGraphAlreadyExists, graphID, latest.manifest.GraphSessionID)
		}
		if !os.IsNotExist(err) {
			return err
		}
	case graphCommitOverwrite:
		if err != nil {
			return err
		}
		expected := strings.TrimSpace(req.ExpectedHead)
		if expected == "" {
			return invalidRequestf("expected_graph_session_id is required for overwrite")
		}
		if expected != latest.manifest.GraphSessionID {
			return &graphHeadConflictError{Expected: expected, Current: latest.manifest}
		}
	default:
		return invalidRequestf("commit mode must be create or overwrite")
	}
	return nil
}

func (s *Server) rollbackConfiguredGraph(previous graphRuntimeSession, response graphLoadResponse) {
	if s == nil || s.runtime == nil {
		return
	}
	newSessionID := strings.TrimSpace(response.Graph.GraphSessionID)
	previousSessionID := ""
	if previous.runner != nil {
		previousSessionID = strings.TrimSpace(previous.runner.GraphSessionID())
	}
	if newSessionID != "" && newSessionID != previousSessionID {
		s.runtime.removeSession(response.Graph.ID, newSessionID)
		_ = os.RemoveAll(response.RunnerBaseDir)
		_ = os.RemoveAll(response.candidateDir)
	}
	if previous.runner != nil {
		s.runtime.installSession(previous)
	}
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
	candidate bool,
) (graphLoadResponse, error) {
	runnerBaseDir := s.nextUploadedGraphBaseDir(graphID)
	graphSessionID := graphSessionIDFromBaseDir(runnerBaseDir)
	snapshotBaseDir := runnerBaseDir
	if candidate {
		snapshotBaseDir = filepath.Join(filepath.Dir(runnerBaseDir), ".candidates", graphSessionID)
	}
	if err := writeGraphSessionSnapshot(snapshotBaseDir, graphSessionManifest{
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
	cfg := s.cfg
	cfg.Graph = graph
	cfg.GraphID = graphID
	cfg.GraphVersion = graphVersion
	cfg.GraphHash = graphHash
	cfg.GraphSnapshotHash = graphSnapshotHash
	cfg.GraphSessionID = graphSessionID
	runner, err := s.runtime.newRunner(graph, cfg, s.graphHistoryBaseDir(graphID), s.events)
	if err != nil {
		if cleanupErr := os.RemoveAll(snapshotBaseDir); cleanupErr != nil {
			return graphLoadResponse{}, fmt.Errorf("create graph runner: %v; remove new graph session: %w", err, cleanupErr)
		}
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
		Settings:      s.graphSettingsResponse(settings),
		Warnings:      runner.StartupWarnings(),
		candidateDir:  snapshotBaseDir,
	}, nil
}

func (s *Server) publishGraphCandidate(response graphLoadResponse) error {
	if strings.TrimSpace(response.candidateDir) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(response.RunnerBaseDir), 0o700); err != nil {
		return err
	}
	if err := os.Rename(response.candidateDir, response.RunnerBaseDir); err != nil {
		return fmt.Errorf("publish graph candidate: %w", err)
	}
	return nil
}

func (s *Server) graphCommitJournalPath(graphID string) string {
	return filepath.Join(graphStorageDirectory(s.baseDir, graphID), ".commit.json")
}

func (s *Server) writeGraphCommitJournal(response graphLoadResponse, requestID string, previous []trigger.Trigger) error {
	journal := graphCommitJournal{
		GraphID:          response.Graph.ID,
		GraphSessionID:   response.Graph.GraphSessionID,
		RequestID:        requestID,
		Response:         response,
		PreviousTriggers: previous,
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize graph commit journal: %w", err)
	}
	data = append(data, '\n')
	path := s.graphCommitJournalPath(response.Graph.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := writeGraphSessionFile(path, data); err != nil {
		return fmt.Errorf("write graph commit journal: %w", err)
	}
	return nil
}

func (s *Server) removeGraphCommitJournal(graphID string) error {
	err := os.Remove(s.graphCommitJournalPath(graphID))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove graph commit journal: %w", err)
	}
	return nil
}

func (s *Server) graphCommitReceiptPath(graphID string, requestID string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(requestID)))
	return filepath.Join(graphStorageDirectory(s.baseDir, graphID), ".commits", fmt.Sprintf("%x.json", hash[:]))
}

func (s *Server) loadGraphCommitReceipt(graphID string, requestID string) (graphLoadResponse, bool, error) {
	data, err := os.ReadFile(s.graphCommitReceiptPath(graphID, requestID))
	if os.IsNotExist(err) {
		return graphLoadResponse{}, false, nil
	}
	if err != nil {
		return graphLoadResponse{}, false, fmt.Errorf("read graph commit receipt: %w", err)
	}
	var response graphLoadResponse
	if err := decodeStrictJSON(data, &response); err != nil {
		return graphLoadResponse{}, false, fmt.Errorf("decode graph commit receipt: %w", err)
	}
	return response, true, nil
}

func (s *Server) writeGraphCommitReceipt(graphID string, requestID string, response graphLoadResponse) error {
	path := s.graphCommitReceiptPath(graphID, requestID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize graph commit receipt: %w", err)
	}
	data = append(data, '\n')
	if err := writeGraphSessionFile(path, data); err != nil {
		return fmt.Errorf("write graph commit receipt: %w", err)
	}
	return nil
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

func (s *Server) graphResponse(
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
		Settings:      s.graphSettingsResponse(settings),
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
		reader, openErr := openCachedRuntimeReader(storeDir)
		if openErr != nil {
			return nil, fmt.Errorf("open runtime store %q: %w", storeDir, openErr)
		}
		if reader == nil {
			continue
		}
		runs, listErr := reader.ListRuns(context.Background(), runtime.RunFilter{Statuses: []runtime.RunStatus{
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
