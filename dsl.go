package falcon

import (
	"encoding/json"
	"fmt"
	"os"
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

const (
	GraphDefinitionVersion = "1.0"
	CommonStateSchemaID    = "falcon.common.state"
)

type StateFieldDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Schema      JSONSchema `json:"schema"`
}

type GraphNodeSpec struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
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

func normalizeGraphDefinition(def GraphDefinition) GraphDefinition {
	if def.Version == "" {
		def.Version = GraphDefinitionVersion
	}
	if def.StateSchema == "" {
		def.StateSchema = CommonStateSchemaID
	}
	return def
}

func (d GraphDefinition) Validate() error {
	d = normalizeGraphDefinition(d)

	if len(d.Nodes) == 0 {
		return fmt.Errorf("graph definition must include at least one node")
	}
	nodeIDs := map[string]struct{}{}
	for _, node := range d.Nodes {
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

	if d.EntryPoint != "" {
		if _, ok := nodeIDs[d.EntryPoint]; !ok {
			return fmt.Errorf("graph entry point %q not found", d.EntryPoint)
		}
	}
	if d.FinishPoint != "" {
		if _, ok := nodeIDs[d.FinishPoint]; !ok {
			return fmt.Errorf("graph finish point %q not found", d.FinishPoint)
		}
	}

	for _, edge := range d.Edges {
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

func SerializeGraphDefinition(def GraphDefinition) ([]byte, error) {
	def = normalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(def, "", "  ")
}

func DeserializeGraphDefinition(data []byte) (GraphDefinition, error) {
	var def GraphDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return GraphDefinition{}, err
	}
	def = normalizeGraphDefinition(def)
	return def, def.Validate()
}

type GraphNodeSpecProvider interface {
	GraphNodeSpec() GraphNodeSpec
}
