package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	wfgraph "github.com/dengzii/weaveflow/graph"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"

	"github.com/gin-gonic/gin"
)

type graphInfo struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	EntryPoint  string `json:"entry_point,omitempty"`
	FinishPoint string `json:"finish_point,omitempty"`
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
	StateFields []dsl.StateFieldDefinition `json:"state_fields"`
	NodeTypes   []dsl.NodeTypeSchema       `json:"node_types"`
	Conditions  []dsl.ConditionSchema      `json:"conditions"`
	GraphSchema dsl.JSONSchema             `json:"graph_schema"`
}

func (s *Server) handleGraph(c *gin.Context) {
	def, err := s.graphDefinition()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, graphInfo{
		ID:          s.graphID(),
		Version:     s.graphVersion(),
		EntryPoint:  def.EntryPoint,
		FinishPoint: def.FinishPoint,
	})
}

func (s *Server) handleGraphDefinition(c *gin.Context) {
	def, err := s.graphDefinition()
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, def)
}

func (s *Server) handleGraphNodes(c *gin.Context) {
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

func (s *Server) handleGraphInitialStateRequirements(c *gin.Context) {
	graph := s.currentGraph()
	if graph == nil {
		writeError(c, http.StatusServiceUnavailable, errGraphNotConfigured)
		return
	}
	writeData(c, http.StatusOK, graph.InitialStateRequirements())
}

func (s *Server) handleGraphMermaid(c *gin.Context) {
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

func (s *Server) handleRegistry(c *gin.Context) {
	if s == nil || s.registry == nil {
		writeError(c, http.StatusServiceUnavailable, errRegistryNotConfigured)
		return
	}

	stateFields := make([]dsl.StateFieldDefinition, 0, len(s.registry.StateFields))
	for _, key := range sortedStateFieldKeys(s.registry.StateFields) {
		stateFields = append(stateFields, s.registry.StateFields[key])
	}

	nodeTypes := make([]dsl.NodeTypeSchema, 0, len(s.registry.NodeTypes))
	for _, key := range sortedNodeTypeKeys(s.registry.NodeTypes) {
		nodeTypes = append(nodeTypes, s.registry.NodeTypes[key].NodeTypeSchema)
	}

	conditions := make([]dsl.ConditionSchema, 0, len(s.registry.Conditions))
	for _, key := range sortedConditionKeys(s.registry.Conditions) {
		conditions = append(conditions, s.registry.Conditions[key].ConditionSchema)
	}

	writeData(c, http.StatusOK, registryResponse{
		StateFields: stateFields,
		NodeTypes:   nodeTypes,
		Conditions:  conditions,
		GraphSchema: s.registry.JSONSchema(),
	})
}

func (s *Server) graphDefinition() (dsl.GraphDefinition, error) {
	graph := s.currentGraph()
	if graph == nil {
		return dsl.GraphDefinition{}, errGraphNotConfigured
	}
	return graph.Definition()
}

func (s *Server) graphID() string {
	runner := s.currentRunner()
	if runner != nil {
		if id := strings.TrimSpace(runner.GraphID); id != "" {
			return id
		}
	}
	return "graph"
}

func (s *Server) graphVersion() string {
	runner := s.currentRunner()
	if runner != nil {
		if version := strings.TrimSpace(runner.GraphVersion); version != "" {
			return version
		}
	}
	return runtime.DefaultGraphVersion
}

func (s *Server) currentGraph() *wfgraph.Graph {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.graph
}

func (s *Server) currentRunner() *runtime.GraphRunner {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runner
}

func sortedGraphNodeSpecKeys(input map[string]dsl.GraphNodeSpec) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStateFieldKeys(input map[string]dsl.StateFieldDefinition) []string {
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

func sortedConditionKeys(input map[string]wfregistry.ConditionDefinition) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
