package weaveflow

import (
	"fmt"
	"strings"
	"weaveflow/dsl"
)

func ApplyGraphInstanceConfig(def GraphDefinition, instance dsl.GraphInstanceConfig) (GraphDefinition, error) {
	def = dsl.NormalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return GraphDefinition{}, err
	}
	if err := instance.Validate(); err != nil {
		return GraphDefinition{}, err
	}

	disabled := make(map[string]struct{}, len(instance.NodeConfigs))
	nodeConfigs := make(map[string]dsl.GraphNodeInstanceConfig, len(instance.NodeConfigs))
	for nodeID, nodeConfig := range instance.NodeConfigs {
		trimmed := strings.TrimSpace(nodeID)
		if trimmed == "" {
			return GraphDefinition{}, fmt.Errorf("graph instance node config key is required")
		}
		nodeConfigs[trimmed] = cloneNodeInstanceConfig(nodeConfig)
		if nodeConfig.Disabled {
			disabled[trimmed] = struct{}{}
		}
	}

	nodeIDs := make(map[string]struct{}, len(def.Nodes))
	for _, node := range def.Nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	for nodeID := range nodeConfigs {
		if _, ok := nodeIDs[nodeID]; !ok {
			return GraphDefinition{}, fmt.Errorf("graph instance node config %q not found in definition", nodeID)
		}
	}
	if _, disabledEntry := disabled[def.EntryPoint]; disabledEntry {
		return GraphDefinition{}, fmt.Errorf("graph entry point %q is disabled by instance config", def.EntryPoint)
	}
	if _, disabledFinish := disabled[def.FinishPoint]; disabledFinish {
		return GraphDefinition{}, fmt.Errorf("graph finish point %q is disabled by instance config", def.FinishPoint)
	}

	applied := cloneGraphDefinition(def)
	applied.Nodes = applied.Nodes[:0]
	for _, node := range def.Nodes {
		if _, skip := disabled[node.ID]; skip {
			continue
		}
		copyNode := cloneGraphNodeSpec(node)
		if instanceNode, ok := nodeConfigs[node.ID]; ok {
			copyNode.Config = mergeConfigMaps(node.Config, instanceNode.Config)
		}
		applied.Nodes = append(applied.Nodes, copyNode)
	}

	applied.Edges = applied.Edges[:0]
	for _, edge := range def.Edges {
		if _, skip := disabled[edge.From]; skip {
			continue
		}
		if _, skip := disabled[edge.To]; skip {
			continue
		}
		applied.Edges = append(applied.Edges, cloneGraphEdgeSpec(edge))
	}
	return applied, nil
}

func cloneGraphDefinition(def GraphDefinition) GraphDefinition {
	cloned := def
	if len(def.Nodes) > 0 {
		cloned.Nodes = make([]dsl.GraphNodeSpec, 0, len(def.Nodes))
		for _, node := range def.Nodes {
			cloned.Nodes = append(cloned.Nodes, cloneGraphNodeSpec(node))
		}
	}
	if len(def.Edges) > 0 {
		cloned.Edges = make([]dsl.GraphEdgeSpec, 0, len(def.Edges))
		for _, edge := range def.Edges {
			cloned.Edges = append(cloned.Edges, cloneGraphEdgeSpec(edge))
		}
	}
	if len(def.Metadata) > 0 {
		cloned.Metadata = cloneMap(def.Metadata)
	}
	return cloned
}

func cloneGraphNodeSpec(node dsl.GraphNodeSpec) dsl.GraphNodeSpec {
	cloned := node
	if len(node.Config) > 0 {
		cloned.Config = cloneMap(node.Config)
	}
	return cloned
}

func cloneGraphEdgeSpec(edge dsl.GraphEdgeSpec) dsl.GraphEdgeSpec {
	cloned := edge
	if edge.Condition != nil {
		copyCondition := *edge.Condition
		if len(copyCondition.Config) > 0 {
			copyCondition.Config = cloneMap(copyCondition.Config)
		}
		cloned.Condition = &copyCondition
	}
	return cloned
}

func cloneNodeInstanceConfig(cfg dsl.GraphNodeInstanceConfig) dsl.GraphNodeInstanceConfig {
	cloned := cfg
	if len(cfg.Config) > 0 {
		cloned.Config = cloneMap(cfg.Config)
	}
	if len(cfg.Secrets) > 0 {
		cloned.Secrets = make(map[string]dsl.SecretRef, len(cfg.Secrets))
		for key, value := range cfg.Secrets {
			cloned.Secrets[key] = value
		}
	}
	if len(cfg.Metadata) > 0 {
		cloned.Metadata = cloneMap(cfg.Metadata)
	}
	return cloned
}

func mergeConfigMaps(base map[string]any, override map[string]any) map[string]any {
	switch {
	case len(base) == 0 && len(override) == 0:
		return nil
	case len(base) == 0:
		return cloneMap(override)
	case len(override) == 0:
		return cloneMap(base)
	}

	merged := cloneMap(base)
	for key, value := range override {
		merged[key] = value
	}
	return merged
}
