package graph

import (
	"fmt"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	fruntime "github.com/dengzii/weaveflow/runtime"
)

func (g *Graph) SetExecutionPolicy(policy fruntime.GraphExecutionPolicy) error {
	return g.setExecutionPolicy(policy, true)
}

func (g *Graph) setExecutionPolicy(policy fruntime.GraphExecutionPolicy, explicit bool) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	policy = fruntime.CloneGraphExecutionPolicy(policy)
	if err := policy.Validate(); err != nil {
		return err
	}
	nodePolicies := make(map[string]fruntime.ExecutionPolicy, len(g.nodePolicies))
	for nodeID, nodePolicy := range g.nodePolicies {
		nodePolicies[nodeID] = fruntime.CloneExecutionPolicy(nodePolicy)
	}
	for nodeID, spec := range g.nodeSpecs {
		if spec.Policy == nil {
			continue
		}
		nodePolicy, err := executionPolicyFromDSL(spec.Policy, policy.NodeDefaults)
		if err != nil {
			return fmt.Errorf("node %q execution policy: %w", nodeID, err)
		}
		nodePolicies[nodeID] = nodePolicy
	}
	g.executionPolicy = policy
	g.executionPolicyExplicit = explicit
	g.nodePolicies = nodePolicies
	g.runLimiter = core.NewConcurrencyLimiter(policy.Limits.MaxConcurrentRuns)
	g.nodeLimiter = core.NewConcurrencyLimiter(policy.Limits.MaxConcurrentNodes)
	g.toolLimiter = core.NewConcurrencyLimiter(policy.Limits.MaxConcurrentTools)
	g.rebuildNodeLimiters()
	return nil
}

func (g *Graph) ExecutionPolicy() fruntime.GraphExecutionPolicy {
	if g == nil {
		return fruntime.DefaultGraphExecutionPolicy()
	}
	return fruntime.CloneGraphExecutionPolicy(g.executionPolicy)
}

func (g *Graph) SetNodeExecutionPolicy(nodeID string, policy fruntime.ExecutionPolicy) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	nodeID = strings.TrimSpace(nodeID)
	if _, ok := g.nodes[nodeID]; !ok {
		return fmt.Errorf("node %q not found", nodeID)
	}
	policy = fruntime.CloneExecutionPolicy(policy)
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("node %q execution policy: %w", nodeID, err)
	}
	if g.nodePolicies == nil {
		g.nodePolicies = map[string]fruntime.ExecutionPolicy{}
	}
	g.nodePolicies[nodeID] = policy
	if spec, ok := g.nodeSpecs[nodeID]; ok {
		spec.Policy = executionPolicyToDSL(policy)
		g.nodeSpecs[nodeID] = spec
	}
	g.nodeLimiters[nodeID] = core.NewConcurrencyLimiter(policy.MaxConcurrency)
	return nil
}

func (g *Graph) nodeExecutionPolicy(nodeID string) fruntime.ExecutionPolicy {
	if g != nil {
		if policy, ok := g.nodePolicies[nodeID]; ok {
			return fruntime.CloneExecutionPolicy(policy)
		}
		return fruntime.CloneExecutionPolicy(g.executionPolicy.NodeDefaults)
	}
	return fruntime.DefaultGraphExecutionPolicy().NodeDefaults
}

func (g *Graph) applyDefinitionExecutionPolicy(def dsl.GraphDefinition) error {
	policy := g.ExecutionPolicy()
	if def.Policy != nil {
		var err error
		policy, err = graphExecutionPolicyFromDSL(def.Policy)
		if err != nil {
			return err
		}
		if err := g.SetExecutionPolicy(policy); err != nil {
			return err
		}
	}
	g.rebuildNodeLimiters()
	return nil
}

func (g *Graph) rebuildNodeLimiters() {
	if g == nil {
		return
	}
	g.nodeLimiters = make(map[string]*core.ConcurrencyLimiter, len(g.nodes))
	for nodeID := range g.nodes {
		policy := g.nodeExecutionPolicy(nodeID)
		g.nodeLimiters[nodeID] = core.NewConcurrencyLimiter(policy.MaxConcurrency)
	}
}

func graphExecutionPolicyFromDSL(spec *dsl.GraphExecutionPolicy) (fruntime.GraphExecutionPolicy, error) {
	policy := fruntime.DefaultGraphExecutionPolicy()
	if spec == nil {
		return policy, nil
	}
	limits := spec.Limits
	if limits.MaxSuperSteps > 0 {
		policy.Limits.MaxSuperSteps = limits.MaxSuperSteps
	}
	if limits.MaxNodeExecutions > 0 {
		policy.Limits.MaxNodeExecutions = limits.MaxNodeExecutions
	}
	if limits.MaxFanOut > 0 {
		policy.Limits.MaxFanOut = limits.MaxFanOut
	}
	if limits.MaxConcurrentRuns > 0 {
		policy.Limits.MaxConcurrentRuns = limits.MaxConcurrentRuns
	}
	if limits.MaxConcurrentNodes > 0 {
		policy.Limits.MaxConcurrentNodes = limits.MaxConcurrentNodes
	}
	if limits.MaxConcurrentTools > 0 {
		policy.Limits.MaxConcurrentTools = limits.MaxConcurrentTools
	}
	if limits.MaxStateBytes > 0 {
		policy.Limits.MaxStateBytes = limits.MaxStateBytes
	}
	if strings.TrimSpace(limits.MaxWallTime) != "" {
		parsed, err := parsePositiveDuration("max wall time", limits.MaxWallTime)
		if err != nil {
			return fruntime.GraphExecutionPolicy{}, err
		}
		policy.Limits.MaxWallTime = parsed
	}
	nodeDefaults, err := executionPolicyFromDSL(&spec.NodeDefaults, policy.NodeDefaults)
	if err != nil {
		return fruntime.GraphExecutionPolicy{}, fmt.Errorf("node defaults: %w", err)
	}
	policy.NodeDefaults = nodeDefaults
	return policy, policy.Validate()
}

func executionPolicyFromDSL(spec *dsl.ExecutionPolicy, base fruntime.ExecutionPolicy) (fruntime.ExecutionPolicy, error) {
	policy := fruntime.CloneExecutionPolicy(base)
	if spec == nil {
		return policy, nil
	}
	if strings.TrimSpace(spec.Timeout) != "" {
		parsed, err := parsePositiveDuration("timeout", spec.Timeout)
		if err != nil {
			return fruntime.ExecutionPolicy{}, err
		}
		policy.Timeout = parsed
	}
	if spec.MaxConcurrency > 0 {
		policy.MaxConcurrency = spec.MaxConcurrency
	}
	if spec.Retry != nil {
		retry := spec.Retry
		retryableExplicit := retry.RetryableErrorClasses != nil
		nonRetryableExplicit := retry.NonRetryableErrorClasses != nil
		if retry.MaxAttempts > 0 {
			policy.Retry.MaxAttempts = retry.MaxAttempts
		}
		if strings.TrimSpace(retry.InitialInterval) != "" {
			parsed, err := parseNonNegativeDuration("retry initial interval", retry.InitialInterval)
			if err != nil {
				return fruntime.ExecutionPolicy{}, err
			}
			policy.Retry.InitialInterval = parsed
		}
		if strings.TrimSpace(retry.MaxInterval) != "" {
			parsed, err := parseNonNegativeDuration("retry max interval", retry.MaxInterval)
			if err != nil {
				return fruntime.ExecutionPolicy{}, err
			}
			policy.Retry.MaxInterval = parsed
		}
		if retry.BackoffMultiplier != nil {
			policy.Retry.BackoffMultiplier = *retry.BackoffMultiplier
		}
		if retry.Jitter != nil {
			policy.Retry.Jitter = *retry.Jitter
		}
		if retryableExplicit {
			policy.Retry.RetryableErrorClasses = errorClasses(retry.RetryableErrorClasses)
		}
		if nonRetryableExplicit {
			policy.Retry.NonRetryableErrorClasses = errorClasses(retry.NonRetryableErrorClasses)
		}
		if retryableExplicit && !nonRetryableExplicit {
			policy.Retry.NonRetryableErrorClasses = excludeErrorClasses(policy.Retry.NonRetryableErrorClasses, policy.Retry.RetryableErrorClasses)
		}
		if nonRetryableExplicit && !retryableExplicit {
			policy.Retry.RetryableErrorClasses = excludeErrorClasses(policy.Retry.RetryableErrorClasses, policy.Retry.NonRetryableErrorClasses)
		}
	}
	return policy, policy.Validate()
}

func excludeErrorClasses(classes, excluded []core.ErrorClass) []core.ErrorClass {
	blocked := make(map[core.ErrorClass]struct{}, len(excluded))
	for _, class := range excluded {
		blocked[class] = struct{}{}
	}
	result := make([]core.ErrorClass, 0, len(classes))
	for _, class := range classes {
		if _, ok := blocked[class]; !ok {
			result = append(result, class)
		}
	}
	return result
}

func graphExecutionPolicyToDSL(policy fruntime.GraphExecutionPolicy) *dsl.GraphExecutionPolicy {
	return &dsl.GraphExecutionPolicy{
		Limits: dsl.GraphLimits{
			MaxSuperSteps:      policy.Limits.MaxSuperSteps,
			MaxNodeExecutions:  policy.Limits.MaxNodeExecutions,
			MaxFanOut:          policy.Limits.MaxFanOut,
			MaxConcurrentRuns:  policy.Limits.MaxConcurrentRuns,
			MaxConcurrentNodes: policy.Limits.MaxConcurrentNodes,
			MaxConcurrentTools: policy.Limits.MaxConcurrentTools,
			MaxStateBytes:      policy.Limits.MaxStateBytes,
			MaxWallTime:        policy.Limits.MaxWallTime.String(),
		},
		NodeDefaults: *executionPolicyToDSL(policy.NodeDefaults),
	}
}

func executionPolicyToDSL(policy fruntime.ExecutionPolicy) *dsl.ExecutionPolicy {
	backoffMultiplier := policy.Retry.BackoffMultiplier
	jitter := policy.Retry.Jitter
	return &dsl.ExecutionPolicy{
		Timeout:        policy.Timeout.String(),
		MaxConcurrency: policy.MaxConcurrency,
		Retry: &dsl.RetryPolicy{
			MaxAttempts:              policy.Retry.MaxAttempts,
			InitialInterval:          policy.Retry.InitialInterval.String(),
			MaxInterval:              policy.Retry.MaxInterval.String(),
			BackoffMultiplier:        &backoffMultiplier,
			Jitter:                   &jitter,
			RetryableErrorClasses:    errorClassStrings(policy.Retry.RetryableErrorClasses),
			NonRetryableErrorClasses: errorClassStrings(policy.Retry.NonRetryableErrorClasses),
		},
	}
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := parseNonNegativeDuration(name, value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return duration, nil
}

func parseNonNegativeDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s %q is invalid: %w", name, value, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s cannot be negative", name)
	}
	return duration, nil
}

func errorClasses(values []string) []core.ErrorClass {
	out := make([]core.ErrorClass, 0, len(values))
	for _, value := range values {
		if class := strings.TrimSpace(value); class != "" {
			out = append(out, core.ErrorClass(class))
		}
	}
	return out
}

func errorClassStrings(values []core.ErrorClass) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if class := strings.TrimSpace(string(value)); class != "" {
			out = append(out, class)
		}
	}
	return out
}
