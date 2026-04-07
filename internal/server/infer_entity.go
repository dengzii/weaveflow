package server

import "time"

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model            string        `json:"model"`
	Message          []ChatMessage `json:"messages"`
	Stream           bool          `json:"stream"`
	ReasoningEffort  string        `json:"reasoning_effort"`
	MaxTokens        int           `json:"max_tokens"`
	Temperature      float32       `json:"temperature"`
	TopP             float32       `json:"top_p"`
	TopK             float32       `json:"top_k"`
	Stop             []string      `json:"stop"`
	FrequencyPenalty float32       `json:"frequency_penalty"`
	PresencePenalty  float32       `json:"presence_penalty"`
	PenaltyDecay     float32       `json:"penalty_decay"`
}

type LoadModelRequest struct {
	Path    string `json:"path"`
	Backend string `json:"backend"`
}

type ModelInfo struct {
	Id          string    `json:"id"`
	OwnedBy     string    `json:"owned_by"`
	Object      string    `json:"object"`
	Backend     string    `json:"backend"`
	LastUpdated time.Time `json:"last_updated"`
}

type ChatChoice struct {
	Delta string `json:"content"`
}

type ChatSSEEvent struct {
	StopReason string       `json:"stop_reason"`
	Object     string       `json:"object"`
	Choices    []ChatChoice `json:"choices"`
}
