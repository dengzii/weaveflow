package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/state"

	langgraph "github.com/smallnest/langgraphgo/graph"
)

func (g *Graph) setDefinitionMetadata(def dsl.GraphDefinition) {
	if g == nil {
		return
	}
	g.version = strings.TrimSpace(def.Version)
	g.name = strings.TrimSpace(def.Name)
	g.description = strings.TrimSpace(def.Description)
	g.stateModules = append([]dsl.StateModuleRef(nil), def.StateModules...)
	if len(def.Metadata) > 0 {
		g.metadata = config.CloneMap(def.Metadata)
	} else {
		g.metadata = nil
	}
}

func (g *Graph) setStateBindingSemantics(bindings []dsl.StateBindingSemantic) {
	if g == nil || len(bindings) == 0 {
		if g != nil {
			g.stateBindingSemantics = nil
		}
		return
	}
	g.stateBindingSemantics = append([]dsl.StateBindingSemantic(nil), bindings...)
}

func (g *Graph) SemanticHash() (string, error) {
	if g == nil {
		return "", fmt.Errorf("graph is nil")
	}
	definition, err := g.Definition()
	if err != nil {
		return "", err
	}
	bindings := g.stateBindingSemantics
	if len(bindings) == 0 && g.registry != nil {
		resolved, resolveErr := graphbuild.ResolveGraphBindings(definition, g.registry)
		if resolveErr != nil {
			return "", resolveErr
		}
		bindings = graphbuild.StateBindingSemantics(resolved)
	}
	return dsl.SemanticGraphHashWithStateBindings(definition, bindings)
}

func (g *Graph) SnapshotHash() (string, error) {
	if g == nil {
		return "", fmt.Errorf("graph is nil")
	}
	definition, err := g.Definition()
	if err != nil {
		return "", err
	}
	return dsl.SnapshotGraphHash(definition)
}

func LoadGraphDefinitionFile(path string) (dsl.GraphDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dsl.GraphDefinition{}, err
	}
	definition, err := dsl.DeserializeGraphDefinition(data)
	if err != nil {
		return dsl.GraphDefinition{}, fmt.Errorf("load graph definition from %q: %w", path, err)
	}
	return definition, nil
}

func (g *Graph) WriteToFile(path string) error {
	definition, err := g.Definition()
	if err != nil {
		return err
	}
	encoded, err := definition.Serialize()
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".graph-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (g *Graph) DrawMermaid() (string, error) {
	compiled := langgraph.NewStateGraph[*state.State]()
	if err := g.buildStateGraph(compiled, func(string, core.Node) {}); err != nil {
		return "", err
	}
	exporter := langgraph.NewExporter(compiled)
	return exporter.DrawMermaid(), nil
}

func (g *Graph) Definition() (dsl.GraphDefinition, error) {
	if err := g.Validate(); err != nil {
		return dsl.GraphDefinition{}, err
	}

	nodeIDs := make([]string, 0, len(g.nodes))
	for nodeID := range g.nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(leftIndex, rightIndex int) bool {
		left := g.nodeSpecs[nodeIDs[leftIndex]]
		right := g.nodeSpecs[nodeIDs[rightIndex]]
		if left.ID == right.ID {
			return left.Name < right.Name
		}
		return left.ID < right.ID
	})

	nodes := make([]dsl.GraphNodeSpec, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		spec := g.nodeSpecs[nodeID]
		if spec.Type == "" {
			return dsl.GraphDefinition{}, fmt.Errorf("node %q is not serializable: missing registered type", nodeID)
		}
		if len(spec.Config) > 0 {
			spec.Config = config.CloneMap(spec.Config)
		}
		if len(spec.State) > 0 {
			spec.State = cloneStateBindings(spec.State)
		}
		nodes = append(nodes, spec)
	}

	edges := make([]dsl.GraphEdgeSpec, len(g.edgeSpecs))
	for index, edge := range g.edgeSpecs {
		edges[index] = edge
		if edge.Condition == nil {
			continue
		}
		condition := *edge.Condition
		if len(condition.Config) > 0 {
			condition.Config = config.CloneMap(condition.Config)
		}
		if len(condition.State) > 0 {
			condition.State = cloneStateBindings(condition.State)
		}
		edges[index].Condition = &condition
	}

	version := g.version
	if version == "" {
		version = dsl.GraphDefinitionVersion
	}
	var metadata map[string]any
	if len(g.metadata) > 0 {
		metadata = config.CloneMap(g.metadata)
	}
	return dsl.GraphDefinition{
		Version:      version,
		Name:         g.name,
		Description:  g.description,
		StateModules: append([]dsl.StateModuleRef(nil), g.stateModules...),
		EntryPoint:   g.serializeNodeRef(g.entryPoint),
		FinishPoint:  g.serializeNodeRef(g.finishPoint),
		Nodes:        nodes,
		Edges:        edges,
		Metadata:     metadata,
	}, nil
}

func cloneStateBindings(bindings map[string]dsl.StateBinding) map[string]dsl.StateBinding {
	cloned := make(map[string]dsl.StateBinding, len(bindings))
	for key, binding := range bindings {
		cloned[key] = binding
	}
	return cloned
}

func (g *Graph) serializeNodeRef(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	if nodeID == langgraph.END {
		return EndNodeRef
	}
	return nodeID
}

func (g *Graph) nodeDisplayName(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	if spec, ok := g.nodeSpecs[nodeID]; ok {
		if name := strings.TrimSpace(spec.Name); name != "" {
			return name
		}
		if id := strings.TrimSpace(spec.ID); id != "" {
			return id
		}
	}
	return nodeID
}
