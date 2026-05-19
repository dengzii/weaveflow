package nodes

import (
	"context"
	"errors"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const exploreSummarizerSystemPrompt = "" +
	"You are summarizing a codebase exploration session. " +
	"Produce a structured answer with four short sections in this order:\n" +
	"  1. Direct answer — answer the user's question in 1-3 sentences.\n" +
	"  2. Key files — bullet list, each entry `path` followed by a one-line role.\n" +
	"  3. Important locations — bullet list of `path:line` references the user can jump to.\n" +
	"  4. Open questions — list anything you could not determine; empty if none.\n" +
	"Be terse. Never paste raw file contents. Never invent facts that did not appear in the exploration."

// summarizeExploration condenses the transcript of an explore sub-conversation
// into a structured answer to be written into the parent scope's final_answer.
// Pass the transcript already excluding the system prompt to keep prompts tight.
func summarizeExploration(ctx context.Context, model llms.Model, transcript []llms.MessageContent) (string, error) {
	if model == nil {
		return "", errors.New("explore summarizer: model is nil")
	}

	body := buildReducerTranscript(stripExploreSystemMessages(transcript))
	if strings.TrimSpace(body) == "" {
		return "", errors.New("explore summarizer: transcript is empty")
	}

	resp, err := model.GenerateContent(
		ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, exploreSummarizerSystemPrompt),
			llms.TextParts(
				llms.ChatMessageTypeHuman,
				"Summarize this codebase exploration for the user.\n\n"+body,
			),
		},
		llms.WithTemperature(0),
	)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return "", errors.New("explore summarizer returned no choices")
	}

	summary := strings.TrimSpace(resp.Choices[0].Content)
	if summary == "" {
		return "", errors.New("explore summarizer returned empty summary")
	}
	return summary, nil
}

func stripExploreSystemMessages(messages []llms.MessageContent) []llms.MessageContent {
	out := make([]llms.MessageContent, 0, len(messages))
	for _, m := range messages {
		if m.Role == llms.ChatMessageTypeSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}
