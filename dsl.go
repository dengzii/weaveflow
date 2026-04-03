package falcon

import (
	"encoding/json"
	fruntime "falcon/runtime"
	"fmt"
	"os"
	"strings"
)

type JSONSchema map[string]any

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

const GraphDefinitionVersion = "1.0"

type StateFieldDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Schema      JSONSchema `json:"schema"`
}

// GraphNodeSpec describes a static node inside GraphDefinition.
// Runtime-bound values such as model paths, secret references, and per-instance
// overrides should live in GraphInstanceConfig instead of Config.
type GraphNodeSpec struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
}

type GraphNodeSpecProvider interface {
	GraphNodeSpec() GraphNodeSpec
}

type GraphConditionSpec struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config,omitempty"`
}

type GraphEdgeSpec struct {
	From      string              `json:"from"`
	To        string              `json:"to"`
	Condition *GraphConditionSpec `json:"condition,omitempty"`
}

type GraphDefinition struct {
	Version     string          `json:"version"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	StateSchema string          `json:"state_schema,omitempty"`
	EntryPoint  string          `json:"entry_point,omitempty"`
	FinishPoint string          `json:"finish_point,omitempty"`
	Nodes       []GraphNodeSpec `json:"nodes"`
	Edges       []GraphEdgeSpec `json:"edges,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

func normalizeGraphConditionSpec(spec GraphConditionSpec) GraphConditionSpec {
	spec.Type = strings.TrimSpace(spec.Type)
	if len(spec.Config) == 0 {
		spec.Config = nil
	}
	return spec
}

func normalizeGraphDefinition(def GraphDefinition) GraphDefinition {
	if def.Version == "" {
		def.Version = GraphDefinitionVersion
	}
	if def.StateSchema == "" {
		def.StateSchema = fruntime.CommonStateSchemaID
	}
	for i := range def.Nodes {
		def.Nodes[i].ID = strings.TrimSpace(def.Nodes[i].ID)
		def.Nodes[i].Name = strings.TrimSpace(def.Nodes[i].Name)
		def.Nodes[i].Type = strings.TrimSpace(def.Nodes[i].Type)
		if def.Nodes[i].Name == "" && def.Nodes[i].ID != "" {
			def.Nodes[i].Name = def.Nodes[i].ID
		}
	}
	for i := range def.Edges {
		def.Edges[i].From = strings.TrimSpace(def.Edges[i].From)
		def.Edges[i].To = strings.TrimSpace(def.Edges[i].To)
		if def.Edges[i].Condition != nil {
			condition := normalizeGraphConditionSpec(*def.Edges[i].Condition)
			def.Edges[i].Condition = &condition
		}
	}
	return def
}

func (d GraphDefinition) Validate() error {
	def := normalizeGraphDefinition(d)

	if len(def.Nodes) == 0 {
		return fmt.Errorf("graph definition must include at least one node")
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

	for _, edge := range def.Edges {
		if edge.From == "" || edge.To == "" {
			return fmt.Errorf("graph edge requires from and to")
		}
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
	nd := normalizeGraphDefinition(d)
	if err := nd.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(nd, "", "  ")
}

func DeserializeGraphDefinition(data []byte) (GraphDefinition, error) {
	var def GraphDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return GraphDefinition{}, err
	}
	def = normalizeGraphDefinition(def)
	return def, def.Validate()
}
