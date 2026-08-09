// Package parts defines reusable content-part helpers for LLM messages.
package parts

import "github.com/tmc/langchaingo/llms"

// ReasoningPart carries chain-of-thought / reasoning text produced by a
// reasoning-capable LLM. It satisfies llms.ContentPart through the embedded
// TextContent (whose unexported isPart() method is promoted), but is a
// distinct named type so downstream consumers can route it separately from
// regular assistant content (e.g. send via reasoning_content instead of
// bleeding into assistant.content).
type ReasoningPart struct {
	llms.TextContent
}

func NewReasoningPart(s string) ReasoningPart {
	return ReasoningPart{TextContent: llms.TextContent{Text: s}}
}

var _ llms.ContentPart = ReasoningPart{}
