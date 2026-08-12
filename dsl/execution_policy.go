package dsl

import "encoding/json"

type RetryPolicy struct {
	MaxAttempts              int      `json:"max_attempts,omitempty"`
	InitialInterval          string   `json:"initial_interval,omitempty"`
	MaxInterval              string   `json:"max_interval,omitempty"`
	BackoffMultiplier        *float64 `json:"backoff_multiplier,omitempty"`
	Jitter                   *float64 `json:"jitter,omitempty"`
	RetryableErrorClasses    []string `json:"retryable_error_classes,omitempty"`
	NonRetryableErrorClasses []string `json:"non_retryable_error_classes,omitempty"`
}

func (policy RetryPolicy) MarshalJSON() ([]byte, error) {
	type retryPolicyJSON struct {
		MaxAttempts              int       `json:"max_attempts,omitempty"`
		InitialInterval          string    `json:"initial_interval,omitempty"`
		MaxInterval              string    `json:"max_interval,omitempty"`
		BackoffMultiplier        *float64  `json:"backoff_multiplier,omitempty"`
		Jitter                   *float64  `json:"jitter,omitempty"`
		RetryableErrorClasses    *[]string `json:"retryable_error_classes,omitempty"`
		NonRetryableErrorClasses *[]string `json:"non_retryable_error_classes,omitempty"`
	}
	encoded := retryPolicyJSON{
		MaxAttempts:       policy.MaxAttempts,
		InitialInterval:   policy.InitialInterval,
		MaxInterval:       policy.MaxInterval,
		BackoffMultiplier: policy.BackoffMultiplier,
		Jitter:            policy.Jitter,
	}
	if policy.RetryableErrorClasses != nil {
		classes := make([]string, len(policy.RetryableErrorClasses))
		copy(classes, policy.RetryableErrorClasses)
		encoded.RetryableErrorClasses = &classes
	}
	if policy.NonRetryableErrorClasses != nil {
		classes := make([]string, len(policy.NonRetryableErrorClasses))
		copy(classes, policy.NonRetryableErrorClasses)
		encoded.NonRetryableErrorClasses = &classes
	}
	return json.Marshal(encoded)
}

type ExecutionPolicy struct {
	Timeout        string       `json:"timeout,omitempty"`
	MaxConcurrency int          `json:"max_concurrency,omitempty"`
	Retry          *RetryPolicy `json:"retry,omitempty"`
}

type GraphLimits struct {
	MaxSuperSteps      int    `json:"max_super_steps,omitempty"`
	MaxNodeExecutions  int    `json:"max_node_executions,omitempty"`
	MaxFanOut          int    `json:"max_fan_out,omitempty"`
	MaxConcurrentRuns  int    `json:"max_concurrent_runs,omitempty"`
	MaxConcurrentNodes int    `json:"max_concurrent_nodes,omitempty"`
	MaxConcurrentTools int    `json:"max_concurrent_tools,omitempty"`
	MaxStateBytes      int64  `json:"max_state_bytes,omitempty"`
	MaxWallTime        string `json:"max_wall_time,omitempty"`
}

type GraphExecutionPolicy struct {
	Limits       GraphLimits     `json:"limits,omitempty"`
	NodeDefaults ExecutionPolicy `json:"node_defaults,omitempty"`
}
