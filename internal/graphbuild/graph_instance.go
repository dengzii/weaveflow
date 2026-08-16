package graphbuild

import (
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
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
	if cfg.Metadata != nil {
		cloned.Metadata = cloneConfigMap(cfg.Metadata)
	}
	return cloned
}

func cloneGraphDefinition(def dsl.GraphDefinition) dsl.GraphDefinition {
	cloned := def
	if def.Policy != nil {
		policy := *def.Policy
		policy.NodeDefaults = cloneExecutionPolicy(def.Policy.NodeDefaults)
		cloned.Policy = &policy
	}
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
	if def.Metadata != nil {
		cloned.Metadata = cloneConfigMap(def.Metadata)
	}
	return cloned
}

func cloneGraphNodeSpec(node dsl.GraphNodeSpec) dsl.GraphNodeSpec {
	cloned := node
	if node.Policy != nil {
		policy := cloneExecutionPolicy(*node.Policy)
		cloned.Policy = &policy
	}
	if node.Config != nil {
		cloned.Config = cloneConfigMap(node.Config)
	}
	if node.State != nil {
		cloned.State = make(map[string]dsl.StateBinding, len(node.State))
		for key, binding := range node.State {
			cloned.State[key] = binding
		}
	}
	return cloned
}

func cloneExecutionPolicy(policy dsl.ExecutionPolicy) dsl.ExecutionPolicy {
	if policy.Retry == nil {
		return policy
	}
	retry := *policy.Retry
	if policy.Retry.BackoffMultiplier != nil {
		value := *policy.Retry.BackoffMultiplier
		retry.BackoffMultiplier = &value
	}
	if policy.Retry.Jitter != nil {
		value := *policy.Retry.Jitter
		retry.Jitter = &value
	}
	retry.RetryableErrorClasses = clonePolicyErrorClasses(policy.Retry.RetryableErrorClasses)
	retry.NonRetryableErrorClasses = clonePolicyErrorClasses(policy.Retry.NonRetryableErrorClasses)
	policy.Retry = &retry
	return policy
}

func clonePolicyErrorClasses(classes []string) []string {
	if classes == nil {
		return nil
	}
	return append([]string{}, classes...)
}

func cloneGraphEdgeSpec(edge dsl.GraphEdgeSpec) dsl.GraphEdgeSpec {
	cloned := edge
	if edge.Condition != nil {
		copyCondition := *edge.Condition
		if copyCondition.Config != nil {
			copyCondition.Config = cloneConfigMap(copyCondition.Config)
		}
		if copyCondition.State != nil {
			copyCondition.State = make(map[string]dsl.StateBinding, len(copyCondition.State))
			for key, binding := range edge.Condition.State {
				copyCondition.State[key] = binding
			}
		}
		cloned.Condition = &copyCondition
	}
	if edge.Failure != nil {
		failure := *edge.Failure
		failure.Stages = append([]dsl.FailureStage(nil), edge.Failure.Stages...)
		failure.ErrorClasses = append([]string(nil), edge.Failure.ErrorClasses...)
		cloned.Failure = &failure
	}
	return cloned
}

func cloneNodeInstanceConfig(cfg dsl.GraphNodeInstanceConfig) dsl.GraphNodeInstanceConfig {
	cloned := cfg
	if cfg.Config != nil {
		cloned.Config = cloneConfigMap(cfg.Config)
	}
	if len(cfg.Secrets) > 0 {
		cloned.Secrets = make(map[string]dsl.SecretRef, len(cfg.Secrets))
		for key, value := range cfg.Secrets {
			cloned.Secrets[key] = value
		}
	}
	if cfg.Metadata != nil {
		cloned.Metadata = cloneConfigMap(cfg.Metadata)
	}
	return cloned
}

func mergeConfigMaps(base map[string]any, override map[string]any) map[string]any {
	switch {
	case len(base) == 0 && len(override) == 0:
		return nil
	case len(base) == 0:
		return cloneConfigMap(override)
	case len(override) == 0:
		return cloneConfigMap(base)
	}

	merged := cloneConfigMap(base)
	for key, value := range override {
		merged[key] = value
	}
	return cloneConfigMap(merged)
}

func cloneConfigMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	root := state.FromMap(map[string]any{state.SectionShared: input}).Export()
	cloned, _ := root[state.SectionShared].(map[string]any)
	return cloned
}
