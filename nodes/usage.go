package nodes

import (
	"context"
	"strings"
	"weaveflow/runtime"

	"github.com/tmc/langchaingo/llms"
)

const TokenUsageStateKey = StateKey

const StateKey = "token_usage"

type Usage struct {
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	ReasoningTokens    int
	PromptCachedTokens int
}

type Record struct {
	NodeID             string
	Model              string
	StateScope         string
	StopReason         string
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	ReasoningTokens    int
	PromptCachedTokens int
}

func Extract(choice *llms.ContentChoice) Usage {
	if choice == nil {
		return Usage{}
	}

	usage := Usage{}
	if len(choice.GenerationInfo) == 0 {
		return usage
	}

	usage.PromptTokens, _ = intValueFromKeys(choice.GenerationInfo,
		"PromptTokens",
		"prompt_tokens",
		"input_tokens",
	)
	usage.CompletionTokens, _ = intValueFromKeys(choice.GenerationInfo,
		"CompletionTokens",
		"completion_tokens",
		"output_tokens",
	)
	usage.TotalTokens, _ = intValueFromKeys(choice.GenerationInfo,
		"TotalTokens",
		"total_tokens",
	)
	usage.ReasoningTokens, _ = intValueFromKeys(choice.GenerationInfo,
		"ReasoningTokens",
		"ThinkingTokens",
		"CompletionReasoningTokens",
		"reasoning_tokens",
	)
	usage.PromptCachedTokens, _ = intValueFromKeys(choice.GenerationInfo,
		"PromptCachedTokens",
		"prompt_cached_tokens",
		"cached_input_tokens",
	)

	return usage.normalized()
}

func RecordState(state runtime.State, record Record) Record {
	record = record.normalized()
	if state == nil || record.IsZero() {
		return record
	}

	root := ensureMap(state, StateKey)
	accumulateMetrics(ensureMap(root, "totals"), record)
	root["last"] = record.StateValue()

	if record.NodeID != "" {
		accumulateMetrics(ensureMap(ensureMap(root, "by_node"), record.NodeID), record)
	}
	if record.Model != "" {
		accumulateMetrics(ensureMap(ensureMap(root, "by_model"), record.Model), record)
	}
	if record.StateScope != "" {
		accumulateMetrics(ensureMap(ensureMap(root, "by_scope"), record.StateScope), record)
	}

	return record
}

func PublishUsageEvent(ctx context.Context, record Record) error {
	record = record.normalized()
	if record.IsZero() {
		return nil
	}
	return runtime.PublishRunnerContextEvent(ctx, runtime.EventLLMUsage, record.EventPayload())
}

func (u Usage) IsZero() bool {
	return u.PromptTokens == 0 &&
		u.CompletionTokens == 0 &&
		u.TotalTokens == 0 &&
		u.ReasoningTokens == 0 &&
		u.PromptCachedTokens == 0
}

func (u Usage) Artifact() map[string]any {
	u1 := u.normalized()
	return map[string]any{
		"prompt_tokens":        u1.PromptTokens,
		"completion_tokens":    u1.CompletionTokens,
		"total_tokens":         u1.TotalTokens,
		"reasoning_tokens":     u1.ReasoningTokens,
		"prompt_cached_tokens": u1.PromptCachedTokens,
	}
}

func (r Record) IsZero() bool {
	return r.PromptTokens == 0 &&
		r.CompletionTokens == 0 &&
		r.TotalTokens == 0 &&
		r.ReasoningTokens == 0 &&
		r.PromptCachedTokens == 0
}

func (r Record) StateValue() map[string]any {
	r1 := r.normalized()
	return map[string]any{
		"node_id":              r1.NodeID,
		"model":                r1.Model,
		"state_scope":          r1.StateScope,
		"stop_reason":          r1.StopReason,
		"prompt_tokens":        r1.PromptTokens,
		"completion_tokens":    r1.CompletionTokens,
		"total_tokens":         r1.TotalTokens,
		"reasoning_tokens":     r1.ReasoningTokens,
		"prompt_cached_tokens": r1.PromptCachedTokens,
	}
}

func (r Record) EventPayload() map[string]any {
	payload := r.StateValue()
	payload["calls"] = 1
	return payload
}

func (r Record) ArtifactPayload() map[string]any {
	return r.StateValue()
}

func (r Record) normalized() Record {
	usage := Usage{
		PromptTokens:       r.PromptTokens,
		CompletionTokens:   r.CompletionTokens,
		TotalTokens:        r.TotalTokens,
		ReasoningTokens:    r.ReasoningTokens,
		PromptCachedTokens: r.PromptCachedTokens,
	}.normalized()

	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Model = strings.TrimSpace(r.Model)
	r.StateScope = strings.TrimSpace(r.StateScope)
	r.StopReason = strings.TrimSpace(r.StopReason)
	r.PromptTokens = usage.PromptTokens
	r.CompletionTokens = usage.CompletionTokens
	r.TotalTokens = usage.TotalTokens
	r.ReasoningTokens = usage.ReasoningTokens
	r.PromptCachedTokens = usage.PromptCachedTokens
	return r
}

func (u Usage) normalized() Usage {
	if u.TotalTokens <= 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if u.CompletionTokens <= 0 && u.TotalTokens > 0 && u.TotalTokens >= u.PromptTokens {
		u.CompletionTokens = u.TotalTokens - u.PromptTokens
	}
	if total := u.PromptTokens + u.CompletionTokens; total > u.TotalTokens {
		u.TotalTokens = total
	}
	return u
}

func intValueFromKeys(values map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if parsed, ok := intValue(value); ok {
			return parsed, true
		}
	}
	return 0, false
}

func ensureMap(values map[string]any, key string) map[string]any {
	if values == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	switch typed := values[key].(type) {
	case map[string]any:
		return typed
	case runtime.State:
		return typed
	}
	nested := map[string]any{}
	values[key] = nested
	return nested
}

func accumulateMetrics(target map[string]any, record Record) {
	if target == nil {
		return
	}
	target["calls"] = metric(target, "calls") + 1
	target["prompt_tokens"] = metric(target, "prompt_tokens") + record.PromptTokens
	target["completion_tokens"] = metric(target, "completion_tokens") + record.CompletionTokens
	target["total_tokens"] = metric(target, "total_tokens") + record.TotalTokens
	target["reasoning_tokens"] = metric(target, "reasoning_tokens") + record.ReasoningTokens
	target["prompt_cached_tokens"] = metric(target, "prompt_cached_tokens") + record.PromptCachedTokens
}

func metric(values map[string]any, key string) int {
	if values == nil {
		return 0
	}
	value, ok := values[key]
	if !ok {
		return 0
	}
	parsed, _ := intValue(value)
	return parsed
}
