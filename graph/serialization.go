package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/graphbuild"
	"github.com/dengzii/weaveflow/state"
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
		g.metadata = cloneNodeConfig(def.Metadata)
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
	resolvedPath, err := resolveGraphFilePath(path)
	if err != nil {
		return dsl.GraphDefinition{}, err
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return dsl.GraphDefinition{}, err
	}
	definition, err := dsl.DeserializeGraphDefinition(data)
	if err != nil {
		return dsl.GraphDefinition{}, fmt.Errorf("load graph definition from %q: %w", resolvedPath, err)
	}
	return definition, nil
}

func (g *Graph) WriteToFile(path string) error {
	resolvedPath, err := resolveGraphFilePath(path)
	if err != nil {
		return err
	}
	definition, err := g.Definition()
	if err != nil {
		return err
	}
	encoded, err := definition.Serialize()
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(resolvedPath), ".graph-*")
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
	return os.Rename(temporaryPath, resolvedPath)
}

func resolveGraphFilePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("graph file path is required")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve graph file path: %w", err)
	}
	return resolved, nil
}

func (g *Graph) DrawMermaid() (string, error) {
	if err := g.Validate(); err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.WriteString("flowchart TD\n")
	builder.WriteString(fmt.Sprintf("    %s[[\"%s\"]]\n", g.entryPoint, g.entryPoint))
	builder.WriteString(fmt.Sprintf("    START --> %s\n", g.entryPoint))
	builder.WriteString("    START([\"START\"])\n")
	builder.WriteString("    style START fill:#90EE90\n")
	for _, nodeID := range g.sortedNodeIDs() {
		if nodeID == g.entryPoint {
			continue
		}
		builder.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", nodeID, nodeID))
	}
	builder.WriteString("    END([\"END\"])\n")
	builder.WriteString("    style END fill:#FFB6C1\n")
	for _, edge := range g.edgeSpecs {
		target := edge.To
		if target == EndNodeRef {
			target = "END"
		}
		if edge.Condition != nil {
			builder.WriteString(fmt.Sprintf("    %s -.-> %s\n", edge.From, target))
			continue
		}
		builder.WriteString(fmt.Sprintf("    %s --> %s\n", edge.From, target))
	}
	if g.finishPoint != "" && len(g.defaultEdges[g.finishPoint]) == 0 && len(g.conditionalEdges[g.finishPoint]) == 0 {
		builder.WriteString(fmt.Sprintf("    %s --> END\n", g.finishPoint))
	}
	builder.WriteString(fmt.Sprintf("    style %s fill:#87CEEB\n", g.entryPoint))
	return builder.String(), nil
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
		spec := cloneGraphNodeSpec(g.nodeSpecs[nodeID])
		if spec.Type == "" {
			return dsl.GraphDefinition{}, fmt.Errorf("node %q is not serializable: missing registered type", nodeID)
		}
		nodes = append(nodes, spec)
	}

	edges := make([]dsl.GraphEdgeSpec, len(g.edgeSpecs))
	for index, edge := range g.edgeSpecs {
		edges[index] = edge
		if edge.Failure != nil {
			failure := *edge.Failure
			failure.Stages = append([]dsl.FailureStage(nil), edge.Failure.Stages...)
			failure.ErrorClasses = append([]string(nil), edge.Failure.ErrorClasses...)
			edges[index].Failure = &failure
		}
		if edge.Condition == nil {
			continue
		}
		condition := *edge.Condition
		if condition.Config != nil {
			condition.Config = cloneNodeConfig(condition.Config)
		}
		if condition.State != nil {
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
		metadata = cloneNodeConfig(g.metadata)
	}
	definition := dsl.GraphDefinition{
		Version:      version,
		Name:         g.name,
		Description:  g.description,
		StateModules: append([]dsl.StateModuleRef(nil), g.stateModules...),
		EntryPoint:   g.serializeNodeRef(g.entryPoint),
		FinishPoint:  g.serializeNodeRef(g.finishPoint),
		Nodes:        nodes,
		Edges:        edges,
		Metadata:     metadata,
	}
	if g.executionPolicyExplicit {
		definition.Policy = definitionExecutionPolicyToDSL(g.executionPolicy)
	}
	return definition, nil
}

func cloneStateBindings(bindings map[string]dsl.StateBinding) map[string]dsl.StateBinding {
	cloned := make(map[string]dsl.StateBinding, len(bindings))
	for key, binding := range bindings {
		cloned[key] = binding
	}
	return cloned
}

func cloneGraphNodeSpec(spec dsl.GraphNodeSpec) dsl.GraphNodeSpec {
	if spec.Config != nil {
		spec.Config = cloneNodeConfig(spec.Config)
	}
	if spec.State != nil {
		spec.State = cloneStateBindings(spec.State)
	}
	if spec.Policy != nil {
		policy := *spec.Policy
		if spec.Policy.Retry != nil {
			retry := *spec.Policy.Retry
			retry.RetryableErrorClasses = clonePolicyErrorClasses(spec.Policy.Retry.RetryableErrorClasses)
			retry.NonRetryableErrorClasses = clonePolicyErrorClasses(spec.Policy.Retry.NonRetryableErrorClasses)
			if spec.Policy.Retry.BackoffMultiplier != nil {
				value := *spec.Policy.Retry.BackoffMultiplier
				retry.BackoffMultiplier = &value
			}
			if spec.Policy.Retry.Jitter != nil {
				value := *spec.Policy.Retry.Jitter
				retry.Jitter = &value
			}
			policy.Retry = &retry
		}
		spec.Policy = &policy
	}
	return spec
}

func clonePolicyErrorClasses(classes []string) []string {
	if classes == nil {
		return nil
	}
	return append([]string{}, classes...)
}

func cloneNodeConfig(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	root := state.FromMap(map[string]any{state.SectionShared: values}).Export()
	cloned, _ := root[state.SectionShared].(map[string]any)
	return cloned
}

func (g *Graph) serializeNodeRef(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	if nodeID == endNodeID {
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
