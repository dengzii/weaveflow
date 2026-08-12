// Package dsl defines serializable Graph Definition v2 data transfer objects.
package dsl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type JSONSchema map[string]any

const EndNodeRef = "__end__"

func (r JSONSchema) WriteToFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	bytes, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = f.WriteString(string(bytes))
	return err
}

const GraphDefinitionVersion = "2.0"

type StateFieldDefinition struct {
	Path        string     `json:"path"`
	Description string     `json:"description,omitempty"`
	Schema      JSONSchema `json:"schema"`
}

type StateCapabilityFieldDefinition struct {
	Name          string             `json:"name"`
	Schema        JSONSchema         `json:"schema"`
	MergeStrategy StateMergeStrategy `json:"merge_strategy,omitempty"`
}

type StateCapabilityDefinition struct {
	ID     string                           `json:"id"`
	Schema JSONSchema                       `json:"schema"`
	Fields []StateCapabilityFieldDefinition `json:"fields"`
}

type StateModuleDefinition struct {
	Name         string                      `json:"name"`
	Version      string                      `json:"version"`
	Fields       []StateFieldDefinition      `json:"fields,omitempty"`
	Capabilities []StateCapabilityDefinition `json:"capabilities,omitempty"`
}

type StateModuleRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type GraphNodeSpecProvider interface {
	GraphNodeSpec() GraphNodeSpec
}

type GraphConditionSpec struct {
	ID     string                  `json:"id,omitempty"`
	Type   string                  `json:"type"`
	Config map[string]any          `json:"config,omitempty"`
	State  map[string]StateBinding `json:"state,omitempty"`
}

type StateBinding struct {
	Path string `json:"path"`
}

type GraphEdgeSpec struct {
	From      string              `json:"from"`
	To        string              `json:"to"`
	Condition *GraphConditionSpec `json:"condition,omitempty"`
}

type GraphDefinition struct {
	Version      string                `json:"version"`
	Name         string                `json:"name,omitempty"`
	Description  string                `json:"description,omitempty"`
	StateModules []StateModuleRef      `json:"state_modules"`
	EntryPoint   string                `json:"entry_point,omitempty"`
	FinishPoint  string                `json:"finish_point,omitempty"`
	Nodes        []GraphNodeSpec       `json:"nodes"`
	Edges        []GraphEdgeSpec       `json:"edges,omitempty"`
	Policy       *GraphExecutionPolicy `json:"policy,omitempty"`
	Metadata     map[string]any        `json:"metadata,omitempty"`
}

func NormalizeGraphConditionSpec(spec GraphConditionSpec) GraphConditionSpec {
	spec.ID = strings.TrimSpace(spec.ID)
	spec.Type = strings.TrimSpace(spec.Type)
	if len(spec.Config) == 0 {
		spec.Config = nil
	}
	if len(spec.State) == 0 {
		spec.State = nil
	} else {
		bindings := make(map[string]StateBinding, len(spec.State))
		for name, binding := range spec.State {
			binding.Path = strings.TrimSpace(binding.Path)
			bindings[name] = binding
		}
		spec.State = bindings
	}
	return spec
}

func CloneGraphConditionSpec(spec GraphConditionSpec) GraphConditionSpec {
	spec = NormalizeGraphConditionSpec(spec)
	if len(spec.Config) > 0 {
		spec.Config = cloneConditionConfig(spec.Config)
	}
	if len(spec.State) > 0 {
		bindings := spec.State
		spec.State = make(map[string]StateBinding, len(bindings))
		for name, binding := range bindings {
			spec.State[name] = binding
		}
	}
	return spec
}

func cloneConditionConfig(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneConditionConfigValue(value)
	}
	return cloned
}

func cloneConditionConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneConditionConfig(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneConditionConfigValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func NormalizeGraphDefinition(def GraphDefinition) GraphDefinition {
	if len(def.StateModules) > 0 {
		def.StateModules = append([]StateModuleRef(nil), def.StateModules...)
	}
	if len(def.Nodes) > 0 {
		def.Nodes = append([]GraphNodeSpec(nil), def.Nodes...)
	}
	if len(def.Edges) > 0 {
		def.Edges = append([]GraphEdgeSpec(nil), def.Edges...)
	}
	def.Version = strings.TrimSpace(def.Version)
	for i := range def.StateModules {
		def.StateModules[i].Name = strings.TrimSpace(def.StateModules[i].Name)
		def.StateModules[i].Version = strings.TrimSpace(def.StateModules[i].Version)
	}
	for i := range def.Nodes {
		def.Nodes[i].ID = strings.TrimSpace(def.Nodes[i].ID)
		def.Nodes[i].Name = strings.TrimSpace(def.Nodes[i].Name)
		def.Nodes[i].Type = strings.TrimSpace(def.Nodes[i].Type)
		if def.Nodes[i].Name == "" && def.Nodes[i].ID != "" {
			def.Nodes[i].Name = def.Nodes[i].ID
		}
		if def.Nodes[i].State == nil {
			def.Nodes[i].State = map[string]StateBinding{}
		} else {
			bindings := make(map[string]StateBinding, len(def.Nodes[i].State))
			for name, binding := range def.Nodes[i].State {
				binding.Path = strings.TrimSpace(binding.Path)
				bindings[name] = binding
			}
			def.Nodes[i].State = bindings
		}
		if def.Nodes[i].Policy != nil {
			policy := *def.Nodes[i].Policy
			def.Nodes[i].Policy = &policy
		}
	}
	if def.Policy != nil {
		policy := *def.Policy
		def.Policy = &policy
	}
	for i := range def.Edges {
		def.Edges[i].From = strings.TrimSpace(def.Edges[i].From)
		def.Edges[i].To = strings.TrimSpace(def.Edges[i].To)
		if def.Edges[i].Condition != nil {
			condition := NormalizeGraphConditionSpec(*def.Edges[i].Condition)
			def.Edges[i].Condition = &condition
		}
	}
	return def
}

func (d GraphDefinition) Validate() error {
	def := NormalizeGraphDefinition(d)
	if def.Version != GraphDefinitionVersion {
		return fmt.Errorf("graph definition version must be %q, got %q", GraphDefinitionVersion, def.Version)
	}
	if len(def.StateModules) == 0 {
		return fmt.Errorf("graph definition must include at least one state module")
	}
	moduleRefs := map[string]struct{}{}
	for _, module := range def.StateModules {
		if module.Name == "" || module.Version == "" {
			return fmt.Errorf("graph state module name and version are required")
		}
		key := module.Name + "\x00" + module.Version
		if _, exists := moduleRefs[key]; exists {
			return fmt.Errorf("graph state module %q version %q is duplicated", module.Name, module.Version)
		}
		moduleRefs[key] = struct{}{}
	}

	if len(def.Nodes) == 0 {
		return fmt.Errorf("graph definition must include at least one nodes")
	}
	nodeIDs := map[string]struct{}{}
	for _, node := range def.Nodes {
		if node.ID == "" {
			return fmt.Errorf("graph node id is required")
		}
		if node.Type == "" {
			return fmt.Errorf("graph node %q type is required", node.ID)
		}
		if _, exists := nodeIDs[node.ID]; exists {
			return fmt.Errorf("graph node id %q is duplicated", node.ID)
		}
		nodeIDs[node.ID] = struct{}{}
	}

	if def.EntryPoint != "" {
		if _, ok := nodeIDs[def.EntryPoint]; !ok {
			return fmt.Errorf("graph entry point %q not found", def.EntryPoint)
		}
	}
	if def.FinishPoint != "" {
		if _, ok := nodeIDs[def.FinishPoint]; !ok {
			return fmt.Errorf("graph finish point %q not found", def.FinishPoint)
		}
	}

	edgePairs := map[string]struct{}{}
	for _, edge := range def.Edges {
		if edge.From == "" || edge.To == "" {
			return fmt.Errorf("graph edge requires from and to")
		}
		pairKey := edge.From + "\x00" + edge.To
		if _, exists := edgePairs[pairKey]; exists {
			return fmt.Errorf("graph edge %q -> %q is duplicated", edge.From, edge.To)
		}
		edgePairs[pairKey] = struct{}{}
		if _, ok := nodeIDs[edge.From]; !ok {
			return fmt.Errorf("graph edge source %q not found", edge.From)
		}
		if edge.To != EndNodeRef {
			if _, ok := nodeIDs[edge.To]; !ok {
				return fmt.Errorf("graph edge target %q not found", edge.To)
			}
		}
		if edge.Condition != nil && edge.Condition.Type == "" {
			return fmt.Errorf("graph edge condition type is required")
		}
	}
	return nil
}

func (d GraphDefinition) Serialize() ([]byte, error) {
	nd := NormalizeGraphDefinition(d)
	if err := nd.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(nd, "", "  ")
}

func DeserializeGraphDefinition(data []byte) (GraphDefinition, error) {
	var def GraphDefinition
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&def); err != nil {
		return GraphDefinition{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return GraphDefinition{}, fmt.Errorf("graph definition contains multiple JSON values")
		}
		return GraphDefinition{}, err
	}
	def = NormalizeGraphDefinition(def)
	return def, def.Validate()
}
