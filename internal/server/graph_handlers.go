package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/chatchannel"
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

type graphNodeView struct {
	ID   string            `json:"id"`
	Spec dsl.GraphNodeSpec `json:"spec"`
}

type graphNodesResponse struct {
	Nodes []graphNodeView              `json:"nodes"`
	Specs map[string]dsl.GraphNodeSpec `json:"specs"`
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

func (s *Server) handleGetGraph(c *gin.Context) {
	graph, runner := s.currentGraphRunner()
	if graph == nil {
		writeError(c, http.StatusServiceUnavailable, errGraphNotConfigured)
		return
	}
	def, err := graph.Definition()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	info := graphInfo{
		ID:          "graph",
		Version:     runtime.DefaultGraphVersion,
		EntryPoint:  def.EntryPoint,
		FinishPoint: def.FinishPoint,
	}
	if runner != nil {
		info.ID = firstNonEmpty(runner.GraphID, info.ID)
		info.Version = firstNonEmpty(runner.GraphVersion, info.Version)
		info.GraphHash = strings.TrimSpace(runner.GraphHash)
		info.GraphSnapshotHash = strings.TrimSpace(runner.GraphSnapshotHash)
		info.GraphSessionID = strings.TrimSpace(runner.GraphSessionID)
	}
	writeData(c, http.StatusOK, info)
}

func (s *Server) handleGetGraphDefinition(c *gin.Context) {
	def, err := s.graphDefinition()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, def)
}

func (s *Server) handleGetGraphNodes(c *gin.Context) {
	graph := s.currentGraph()
	if graph == nil {
		writeError(c, http.StatusServiceUnavailable, errGraphNotConfigured)
		return
	}
	specs := graph.NodeSpecs()
	nodes := make([]graphNodeView, 0, len(specs))
	for _, id := range sortedGraphNodeSpecKeys(specs) {
		nodes = append(nodes, graphNodeView{ID: id, Spec: specs[id]})
	}
	writeData(c, http.StatusOK, graphNodesResponse{
		Nodes: nodes,
		Specs: specs,
	})
}

func (s *Server) handleGetGraphInitialStateRequirements(c *gin.Context) {
	graph := s.currentGraph()
	if graph == nil {
		writeError(c, http.StatusServiceUnavailable, errGraphNotConfigured)
		return
	}
	writeData(c, http.StatusOK, graph.InitialStateRequirements())
}

func (s *Server) handleAnalyzeGraphInitialStateRequirements(c *gin.Context) {
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
	writeData(c, http.StatusOK, graph.InitialStateRequirements())
}

func (s *Server) handleGetGraphMermaid(c *gin.Context) {
	graph := s.currentGraph()
	if graph == nil {
		writeError(c, http.StatusServiceUnavailable, errGraphNotConfigured)
		return
	}
	text, err := graph.DrawMermaid()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(text))
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

func (s *Server) graphDefinition() (dsl.GraphDefinition, error) {
	graph := s.currentGraph()
	if graph == nil {
		return dsl.GraphDefinition{}, errGraphNotConfigured
	}
	return graph.Definition()
}

func (s *Server) currentGraph() *wfgraph.Graph {
	graph, _ := s.currentGraphRunner()
	return graph
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

func sortedGraphNodeSpecKeys(input map[string]dsl.GraphNodeSpec) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
