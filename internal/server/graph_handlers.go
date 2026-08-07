package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/trigger"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"

	"github.com/gin-gonic/gin"
)

type graphInfo struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	GraphHash         string `json:"graph_hash,omitempty"`
	GraphSnapshotHash string `json:"graph_snapshot_hash,omitempty"`
	GraphSessionID    string `json:"graph_session_id,omitempty"`
	EntryPoint        string `json:"entry_point,omitempty"`
	FinishPoint       string `json:"finish_point,omitempty"`
}

type registryResponse struct {
	StateModules []dsl.StateModuleDefinition     `json:"state_modules"`
	Capabilities []dsl.StateCapabilityDefinition `json:"capabilities"`
	NodeGroups   []wfregistry.NodeGroup          `json:"node_groups"`
	NodeTypes    []dsl.NodeTypeSchema            `json:"node_types"`
	Conditions   []dsl.ConditionSchema           `json:"conditions"`
	GraphSchema  dsl.JSONSchema                  `json:"graph_schema"`
	ChatChannels []chatchannel.Definition        `json:"chat_channels"`
}

type triggerInitialStateRequirements struct {
	TriggerID    string                        `json:"trigger_id"`
	Requirements core.InitialStateRequirements `json:"requirements"`
}

type graphInitialStateAnalysis struct {
	Direct   core.InitialStateRequirements     `json:"direct"`
	Triggers []triggerInitialStateRequirements `json:"triggers"`
}

func (s *Server) handleAnalyzeGraphInitialStateRequirements(c *gin.Context) {
	graphID, ok := requireGraphIDPathParam(c)
	if !ok {
		return
	}
	req, err := bindGraphUpload(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	if s == nil || s.registry == nil {
		writeError(c, http.StatusServiceUnavailable, errRegistryNotConfigured)
		return
	}
	graph, err := wfgraph.NewBuilder(s.registry).Build(req.Definition, &wfregistry.BuildContext{})
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	analysis, err := analyzeGraphInitialState(graph, graphID, req.Triggers)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	writeData(c, http.StatusOK, analysis)
}

func analyzeGraphInitialState(graph *wfgraph.Graph, graphID string, payloads []triggerPayload) (graphInitialStateAnalysis, error) {
	analysis := graphInitialStateAnalysis{
		Direct:   graph.InitialStateRequirements(),
		Triggers: make([]triggerInitialStateRequirements, 0, len(payloads)),
	}
	for _, payload := range payloads {
		item := payload.toTrigger(graphID).Normalize(time.Now().UTC())
		if err := item.Validate(); err != nil {
			return graphInitialStateAnalysis{}, err
		}
		requirements, err := graphInitialStateRequirementsForTrigger(graph, item)
		if err != nil {
			return graphInitialStateAnalysis{}, err
		}
		analysis.Triggers = append(analysis.Triggers, triggerInitialStateRequirements{
			TriggerID:    item.ID,
			Requirements: requirements,
		})
	}
	return analysis, nil
}

func graphInitialStateRequirementsForTrigger(graph *wfgraph.Graph, item trigger.Trigger) (core.InitialStateRequirements, error) {
	contract, err := item.ProducedStateContract()
	if err != nil {
		return core.InitialStateRequirements{}, fmt.Errorf("trigger %q state contract: %w", item.ID, err)
	}
	provider := &core.EntryStateProvider{ID: "trigger:" + item.ID, Contract: contract}
	return graph.InitialStateRequirementsFor(provider), nil
}

func validateGraphTriggerState(graph *wfgraph.Graph, items []trigger.Trigger) error {
	for _, item := range items {
		requirements, err := graphInitialStateRequirementsForTrigger(graph, item)
		if err != nil {
			return err
		}
		missing := make([]string, 0, len(requirements.Required)+len(requirements.Unresolved))
		for _, requirement := range append(requirements.Required, requirements.Unresolved...) {
			if pathText := strings.TrimSpace(requirement.Path); pathText != "" {
				missing = append(missing, pathText)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("trigger %q does not provide required graph state: %s", item.ID, strings.Join(missing, ", "))
		}
	}
	return nil
}

func (s *Server) handleGetRegistry(c *gin.Context) {
	if s == nil || s.registry == nil {
		writeError(c, http.StatusServiceUnavailable, errRegistryNotConfigured)
		return
	}

	modulesByKey := s.registry.StateModuleDefinitions()
	stateModules := make([]dsl.StateModuleDefinition, 0, len(modulesByKey))
	for _, key := range sortedStateModuleKeys(modulesByKey) {
		stateModules = append(stateModules, modulesByKey[key])
	}
	capabilitiesByID := s.registry.CapabilityDefinitions()
	capabilities := make([]dsl.StateCapabilityDefinition, 0, len(capabilitiesByID))
	for _, key := range sortedCapabilityKeys(capabilitiesByID) {
		capabilities = append(capabilities, capabilitiesByID[key])
	}
	nodeGroupsByName := s.registry.NodeGroupDefinitions()
	nodeGroups := make([]wfregistry.NodeGroup, 0, len(nodeGroupsByName))
	for _, key := range sortedNodeGroupKeys(nodeGroupsByName) {
		nodeGroups = append(nodeGroups, nodeGroupsByName[key])
	}

	nodeTypes := make([]dsl.NodeTypeSchema, 0, len(s.registry.NodeTypes))
	for _, key := range sortedNodeTypeKeys(s.registry.NodeTypes) {
		nodeTypes = append(nodeTypes, s.registry.NodeTypes[key].NodeTypeSchema)
	}

	conditions := make([]dsl.ConditionSchema, 0, len(s.registry.Conditions))
	for _, key := range sortedConditionKeys(s.registry.Conditions) {
		conditions = append(conditions, s.registry.Conditions[key].ConditionSchema)
	}
	var chatChannels []chatchannel.Definition
	if s.chatChannels != nil {
		chatChannels = s.chatChannels.Definitions()
	}

	writeData(c, http.StatusOK, registryResponse{
		StateModules: stateModules,
		Capabilities: capabilities,
		NodeGroups:   nodeGroups,
		NodeTypes:    nodeTypes,
		Conditions:   conditions,
		GraphSchema:  s.registry.JSONSchema(),
		ChatChannels: chatChannels,
	})
}

func (s *Server) currentRunner() *runtime.GraphRunner {
	_, runner := s.currentGraphRunner()
	return runner
}

func (s *Server) currentGraphRunner() (*wfgraph.Graph, *runtime.GraphRunner) {
	if s == nil || s.runtime == nil {
		return nil, nil
	}
	session := s.runtime.currentSession()
	graph, runner := session.graph, session.runner
	return graph, runner
}

func sortedStateModuleKeys(input map[string]dsl.StateModuleDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedCapabilityKeys(input map[string]dsl.StateCapabilityDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeTypeKeys(input map[string]wfregistry.NodeTypeDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeGroupKeys(input map[string]wfregistry.NodeGroup) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedConditionKeys(input map[string]wfregistry.ConditionDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
