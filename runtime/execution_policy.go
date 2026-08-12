package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/core"
)

const (
	DefaultMaxSuperSteps      = 1000
	DefaultMaxNodeExecutions  = 10000
	DefaultMaxFanOut          = 128
	DefaultMaxConcurrentRuns  = 32
	DefaultMaxConcurrentNodes = 32
	DefaultMaxConcurrentTools = 16
	DefaultMaxStateBytes      = 16 << 20
	DefaultMaxWallTime        = 30 * time.Minute
	DefaultNodeTimeout        = 5 * time.Minute
	DefaultNodeConcurrency    = 8
)

type RetryPolicy struct {
	MaxAttempts              int
	InitialInterval          time.Duration
	MaxInterval              time.Duration
	BackoffMultiplier        float64
	Jitter                   float64
	RetryableErrorClasses    []core.ErrorClass
	NonRetryableErrorClasses []core.ErrorClass
}

type ExecutionPolicy struct {
	Timeout        time.Duration
	MaxConcurrency int
	Retry          RetryPolicy
}

type GraphLimits struct {
	MaxSuperSteps      int
	MaxNodeExecutions  int
	MaxFanOut          int
	MaxConcurrentRuns  int
	MaxConcurrentNodes int
	MaxConcurrentTools int
	MaxStateBytes      int64
	MaxWallTime        time.Duration
}

type GraphExecutionPolicy struct {
	Limits       GraphLimits
	NodeDefaults ExecutionPolicy
}

func DefaultGraphExecutionPolicy() GraphExecutionPolicy {
	return GraphExecutionPolicy{
		Limits: GraphLimits{
			MaxSuperSteps:      DefaultMaxSuperSteps,
			MaxNodeExecutions:  DefaultMaxNodeExecutions,
			MaxFanOut:          DefaultMaxFanOut,
			MaxConcurrentRuns:  DefaultMaxConcurrentRuns,
			MaxConcurrentNodes: DefaultMaxConcurrentNodes,
			MaxConcurrentTools: DefaultMaxConcurrentTools,
			MaxStateBytes:      DefaultMaxStateBytes,
			MaxWallTime:        DefaultMaxWallTime,
		},
		NodeDefaults: ExecutionPolicy{
			Timeout:        DefaultNodeTimeout,
			MaxConcurrency: DefaultNodeConcurrency,
			Retry: RetryPolicy{
				MaxAttempts:       1,
				InitialInterval:   time.Second,
				MaxInterval:       30 * time.Second,
				BackoffMultiplier: 2,
				Jitter:            0.2,
				RetryableErrorClasses: []core.ErrorClass{
					core.ErrorTimeout,
					core.ErrorRateLimited,
					core.ErrorUnavailable,
					core.ErrorSideEffectFailed,
				},
				NonRetryableErrorClasses: []core.ErrorClass{
					core.ErrorInvalidInput,
					core.ErrorCanceled,
					core.ErrorPermissionDenied,
					core.ErrorResourceExhausted,
					core.ErrorNonRetryable,
				},
			},
		},
	}
}

func (policy GraphExecutionPolicy) Validate() error {
	limits := policy.Limits
	positiveLimits := []struct {
		name  string
		value int64
	}{
		{"max super steps", int64(limits.MaxSuperSteps)},
		{"max node executions", int64(limits.MaxNodeExecutions)},
		{"max fan-out", int64(limits.MaxFanOut)},
		{"max concurrent runs", int64(limits.MaxConcurrentRuns)},
		{"max concurrent nodes", int64(limits.MaxConcurrentNodes)},
		{"max concurrent tools", int64(limits.MaxConcurrentTools)},
		{"max state bytes", limits.MaxStateBytes},
		{"max wall time", int64(limits.MaxWallTime)},
	}
	for _, item := range positiveLimits {
		if item.value <= 0 {
			return fmt.Errorf("graph execution policy %s must be greater than zero", item.name)
		}
	}
	return policy.NodeDefaults.Validate()
}

func (policy ExecutionPolicy) Validate() error {
	if policy.Timeout <= 0 {
		return fmt.Errorf("node execution timeout must be greater than zero")
	}
	if policy.MaxConcurrency <= 0 {
		return fmt.Errorf("node max concurrency must be greater than zero")
	}
	retry := policy.Retry
	if retry.MaxAttempts <= 0 || retry.MaxAttempts > 101 {
		return fmt.Errorf("retry max attempts must be between 1 and 101")
	}
	if retry.InitialInterval < 0 || retry.MaxInterval < 0 {
		return fmt.Errorf("retry intervals cannot be negative")
	}
	if retry.MaxInterval > 0 && retry.InitialInterval > retry.MaxInterval {
		return fmt.Errorf("retry initial interval cannot exceed max interval")
	}
	if retry.BackoffMultiplier < 1 {
		return fmt.Errorf("retry backoff multiplier must be at least 1")
	}
	if retry.Jitter < 0 || retry.Jitter > 1 {
		return fmt.Errorf("retry jitter must be between 0 and 1")
	}
	if overlap := overlappingErrorClasses(retry.RetryableErrorClasses, retry.NonRetryableErrorClasses); overlap != "" {
		return fmt.Errorf("retry error class %q is both retryable and non-retryable", overlap)
	}
	return nil
}

func CloneGraphExecutionPolicy(policy GraphExecutionPolicy) GraphExecutionPolicy {
	policy.NodeDefaults.Retry.RetryableErrorClasses = append([]core.ErrorClass(nil), policy.NodeDefaults.Retry.RetryableErrorClasses...)
	policy.NodeDefaults.Retry.NonRetryableErrorClasses = append([]core.ErrorClass(nil), policy.NodeDefaults.Retry.NonRetryableErrorClasses...)
	return policy
}

func CloneExecutionPolicy(policy ExecutionPolicy) ExecutionPolicy {
	policy.Retry.RetryableErrorClasses = append([]core.ErrorClass(nil), policy.Retry.RetryableErrorClasses...)
	policy.Retry.NonRetryableErrorClasses = append([]core.ErrorClass(nil), policy.Retry.NonRetryableErrorClasses...)
	return policy
}

func overlappingErrorClasses(left, right []core.ErrorClass) string {
	seen := make(map[string]struct{}, len(left))
	for _, class := range left {
		value := strings.TrimSpace(string(class))
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, class := range right {
		value := strings.TrimSpace(string(class))
		if _, ok := seen[value]; ok {
			return value
		}
	}
	return ""
}
