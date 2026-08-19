package llms

import (
	"context"
	"strings"

	"github.com/dengzii/weaveflow/state"
)

type Model interface {
	Generate(context.Context, ModelRequest) (*ModelResponse, error)
}

type NamedModel interface {
	Name() string
}

type ReasoningModel interface {
	SupportsReasoning() bool
}

type ModelMode string

const (
	ModelModeChat       ModelMode = "chat"
	ModelModeCompletion ModelMode = "completion"
)

type ThinkingMode string

const (
	ThinkingModeNone    ThinkingMode = "none"
	ThinkingModeMinimal ThinkingMode = "minimal"
	ThinkingModeLow     ThinkingMode = "low"
	ThinkingModeMedium  ThinkingMode = "medium"
	ThinkingModeHigh    ThinkingMode = "high"
	ThinkingModeXHigh   ThinkingMode = "xhigh"
	ThinkingModeMax     ThinkingMode = "max"
	ThinkingModeAuto    ThinkingMode = "auto"
)

type ModelRequest struct {
	CallID                    string             `json:"call_id,omitempty"`
	ModelID                   string             `json:"model_id,omitempty"`
	Model                     string             `json:"model,omitempty"`
	Mode                      ModelMode          `json:"mode,omitempty"`
	Prompt                    string             `json:"prompt,omitempty"`
	Messages                  []MessageContent   `json:"-"`
	Tools                     []ToolDefinition   `json:"tools,omitempty"`
	ToolChoice                any                `json:"tool_choice,omitempty"`
	MaxTokens                 int                `json:"max_tokens,omitempty"`
	Temperature               *float64           `json:"temperature,omitempty"`
	TopP                      *float64           `json:"top_p,omitempty"`
	CandidateCount            int                `json:"candidate_count,omitempty"`
	StopWords                 []string           `json:"stop_words,omitempty"`
	Seed                      *int               `json:"seed,omitempty"`
	FrequencyPenalty          *float64           `json:"frequency_penalty,omitempty"`
	PresencePenalty           *float64           `json:"presence_penalty,omitempty"`
	Thinking                  ThinkingMode       `json:"thinking,omitempty"`
	ResponseName              string             `json:"response_name,omitempty"`
	ResponseSchema            state.JSONSchema   `json:"response_schema,omitempty"`
	StrictResponse            bool               `json:"strict_response,omitempty"`
	ResponseJSON              bool               `json:"response_json,omitempty"`
	ResponseJSONCompatibility bool               `json:"response_json_compatibility,omitempty"`
	Metadata                  map[string]any     `json:"metadata,omitempty"`
	ProviderOptions           map[string]any     `json:"provider_options,omitempty"`
	Stream                    ModelStreamHandler `json:"-"`
}

type ModelResponse struct {
	ID       string         `json:"id,omitempty"`
	Model    string         `json:"model,omitempty"`
	Choices  []*ModelChoice `json:"choices,omitempty"`
	Usage    ModelUsage     `json:"usage"`
	Cost     *ModelCost     `json:"cost,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ModelChoice struct {
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	StopReason       string         `json:"stop_reason,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type ModelUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	ReasoningTokens   int `json:"reasoning_tokens"`
}

func (usage ModelUsage) Normalized() ModelUsage {
	if usage.TotalTokens <= 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.OutputTokens <= 0 && usage.TotalTokens >= usage.InputTokens {
		usage.OutputTokens = usage.TotalTokens - usage.InputTokens
	}
	if usage.InputTokens+usage.OutputTokens > usage.TotalTokens {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

type ModelPricing struct {
	Currency              string  `json:"currency,omitempty"`
	InputPerMillion       float64 `json:"input_per_million,omitempty"`
	CachedInputPerMillion float64 `json:"cached_input_per_million,omitempty"`
	OutputPerMillion      float64 `json:"output_per_million,omitempty"`
}

func (pricing ModelPricing) IsZero() bool {
	return pricing.InputPerMillion == 0 && pricing.CachedInputPerMillion == 0 && pricing.OutputPerMillion == 0
}

type ModelCost struct {
	Currency    string  `json:"currency"`
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	Output      float64 `json:"output"`
	Total       float64 `json:"total"`
}

func CalculateModelCost(usage ModelUsage, pricing ModelPricing) *ModelCost {
	if pricing.IsZero() {
		return nil
	}
	usage = usage.Normalized()
	currency := strings.ToUpper(strings.TrimSpace(pricing.Currency))
	if currency == "" {
		currency = "USD"
	}
	nonCachedInput := max(usage.InputTokens-usage.CachedInputTokens, 0)
	cost := &ModelCost{
		Currency:    currency,
		Input:       float64(nonCachedInput) * pricing.InputPerMillion / 1_000_000,
		CachedInput: float64(usage.CachedInputTokens) * pricing.CachedInputPerMillion / 1_000_000,
		Output:      float64(usage.OutputTokens) * pricing.OutputPerMillion / 1_000_000,
	}
	cost.Total = cost.Input + cost.CachedInput + cost.Output
	return cost
}

type ModelStreamType string

const (
	ModelStreamContent   ModelStreamType = "content"
	ModelStreamReasoning ModelStreamType = "reasoning"
)

type ModelStreamEvent struct {
	CallID string          `json:"call_id,omitempty"`
	Model  string          `json:"model,omitempty"`
	Type   ModelStreamType `json:"type"`
	Text   string          `json:"text"`
}

type ModelStreamHandler func(context.Context, ModelStreamEvent) error

type ToolDefinition struct {
	Type     string              `json:"type"`
	Function *FunctionDefinition `json:"function,omitempty"`
}

type FunctionDefinition struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Parameters   state.JSONSchema `json:"parameters,omitempty"`
	OutputSchema state.JSONSchema `json:"output_schema,omitempty"`
	Strict       bool             `json:"strict,omitempty"`
}
