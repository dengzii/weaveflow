// Package parts defines reusable content-part helpers for LLM messages.
package parts

import "github.com/dengzii/weaveflow/llms"

type ReasoningPart = llms.ReasoningContent

func NewReasoningPart(text string) ReasoningPart {
	return llms.ReasoningPart(text)
}
