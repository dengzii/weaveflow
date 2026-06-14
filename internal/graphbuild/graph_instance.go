package graphbuild

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/config"
)

func ApplyGraphInstanceConfig(def dsl.GraphDefinition, instance dsl.GraphInstanceConfig) (dsl.GraphDefinition, error) {
	def = dsl.NormalizeGraphDefinition(def)
	if err := def.Validate(); err != nil {
		return dsl.GraphDefinition{}, err
	}
	if err := instance.Validate(); err != nil {
		return dsl.GraphDefinition{}, err
	}

	disabled := make(map[string]struct{}, len(instance.NodeConfigs))
	nodeConfigs := make(map[string]dsl.GraphNodeInstanceConfig, len(instance.NodeConfigs))
	for nodeID, nodeConfig := range instance.NodeConfigs {
		trimmed := strings.TrimSpace(nodeID)
		if trimmed == "" {
			return dsl.GraphDefinition{}, fmt.Errorf("graph instance node config key is required")
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
			return dsl.GraphDefinition{}, fmt.Errorf("graph instance node config %q not found in definition", nodeID)
		}
	}
	if _, disabledEntry := disabled[def.EntryPoint]; disabledEntry {
		return dsl.GraphDefinition{}, fmt.Errorf("graph entry point %q is disabled by instance config", def.EntryPoint)
	}
	if _, disabledFinish := disabled[def.FinishPoint]; disabledFinish {
		return dsl.GraphDefinition{}, fmt.Errorf("graph finish point %q is disabled by instance config", def.FinishPoint)
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

func CloneGraphInstanceConfig(cfg dsl.GraphInstanceConfig) dsl.GraphInstanceConfig {
	cloned := cfg
	if len(cfg.NodeConfigs) > 0 {
		cloned.NodeConfigs = make(map[string]dsl.GraphNodeInstanceConfig, len(cfg.NodeConfigs))
		for key, value := range cfg.NodeConfigs {
			cloned.NodeConfigs[key] = cloneNodeInstanceConfig(value)
		}
	}
	if len(cfg.Secrets) > 0 {
		cloned.Secrets = make(map[string]dsl.SecretRef, len(cfg.Secrets))
		for key, value := range cfg.Secrets {
			cloned.Secrets[key] = value
		}
	}
	if len(cfg.Memory) > 0 {
		cloned.Memory = config.CloneMap(cfg.Memory)
	}
	if len(cfg.Metadata) > 0 {
		cloned.Metadata = config.CloneMap(cfg.Metadata)
	}
	return cloned
}

func cloneGraphDefinition(def dsl.GraphDefinition) dsl.GraphDefinition {
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
		cloned.Metadata = config.CloneMap(def.Metadata)
	}
	return cloned
}

func cloneGraphNodeSpec(node dsl.GraphNodeSpec) dsl.GraphNodeSpec {
	cloned := node
	if len(node.Config) > 0 {
		cloned.Config = config.CloneMap(node.Config)
	}
	return cloned
}

func cloneGraphEdgeSpec(edge dsl.GraphEdgeSpec) dsl.GraphEdgeSpec {
	cloned := edge
	if edge.Condition != nil {
		copyCondition := *edge.Condition
		if len(copyCondition.Config) > 0 {
			copyCondition.Config = config.CloneMap(copyCondition.Config)
		}
		cloned.Condition = &copyCondition
	}
	return cloned
}

func cloneNodeInstanceConfig(cfg dsl.GraphNodeInstanceConfig) dsl.GraphNodeInstanceConfig {
	cloned := cfg
	if len(cfg.Config) > 0 {
		cloned.Config = config.CloneMap(cfg.Config)
	}
	if len(cfg.Secrets) > 0 {
		cloned.Secrets = make(map[string]dsl.SecretRef, len(cfg.Secrets))
		for key, value := range cfg.Secrets {
			cloned.Secrets[key] = value
		}
	}
	if len(cfg.Metadata) > 0 {
		cloned.Metadata = config.CloneMap(cfg.Metadata)
	}
	return cloned
}

func mergeConfigMaps(base map[string]any, override map[string]any) map[string]any {
	switch {
	case len(base) == 0 && len(override) == 0:
		return nil
	case len(base) == 0:
		return config.CloneMap(override)
	case len(override) == 0:
		return config.CloneMap(base)
	}

	merged := config.CloneMap(base)
	for key, value := range override {
		merged[key] = value
	}
	return merged
}
