package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Usage struct {
	InputTokens              int64 `json:"input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	OutputTokens             int64 `json:"output_tokens,omitempty"`
}

type Chunk struct {
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Channel   string `json:"channel"`
	Text      string `json:"text"`
}

type eventParser struct {
	onChunk         func(Chunk) error
	model           string
	sessionID       string
	output          string
	usage           Usage
	costUSD         float64
	numTurns        int
	events          []json.RawMessage
	completed       bool
	failed          error
	streamedContent bool
	streamedThought bool
}

type eventEnvelope struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype,omitempty"`
	SessionID        string          `json:"session_id,omitempty"`
	Model            string          `json:"model,omitempty"`
	Message          json.RawMessage `json:"message,omitempty"`
	Event            json.RawMessage `json:"event,omitempty"`
	Result           string          `json:"result,omitempty"`
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	IsError          bool            `json:"is_error,omitempty"`
	Usage            Usage           `json:"usage,omitempty"`
	TotalCostUSD     float64         `json:"total_cost_usd,omitempty"`
	NumTurns         int             `json:"num_turns,omitempty"`
}

type assistantMessage struct {
	Model   string           `json:"model,omitempty"`
	Content []messageContent `json:"content,omitempty"`
}

type messageContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

type streamEvent struct {
	Type         string         `json:"type"`
	Delta        streamDelta    `json:"delta,omitempty"`
	ContentBlock messageContent `json:"content_block,omitempty"`
}

type streamDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

func newClaudeEventParser(onChunk func(Chunk) error) *eventParser {
	return &eventParser{onChunk: onChunk}
}

func (parser *eventParser) parse(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	var envelope eventEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return fmt.Errorf("decode Claude stream-json event: %w", err)
	}
	if strings.TrimSpace(envelope.Type) == "" {
		return fmt.Errorf("decode Claude stream-json event: type is required")
	}
	parser.events = append(parser.events, append(json.RawMessage(nil), line...))
	if sessionID := strings.TrimSpace(envelope.SessionID); sessionID != "" {
		parser.sessionID = sessionID
	}
	if model := strings.TrimSpace(envelope.Model); model != "" {
		parser.model = model
	}

	switch envelope.Type {
	case "system":
		return nil
	case "stream_event":
		return parser.parseStreamEvent(envelope.Event)
	case "assistant":
		return parser.parseAssistantMessage(envelope.Message)
	case "result":
		parser.usage = envelope.Usage
		parser.costUSD = envelope.TotalCostUSD
		parser.numTurns = envelope.NumTurns
		parser.output = strings.TrimSpace(envelope.Result)
		if parser.output == "" && len(envelope.StructuredOutput) > 0 && string(envelope.StructuredOutput) != "null" {
			parser.output = strings.TrimSpace(string(envelope.StructuredOutput))
		}
		if envelope.IsError || (envelope.Subtype != "" && envelope.Subtype != "success") {
			message := parser.output
			if message == "" {
				message = strings.TrimSpace(envelope.Subtype)
			}
			if message == "" {
				message = "unknown provider error"
			}
			parser.failed = fmt.Errorf("Claude reported %s: %s", firstNonEmpty(envelope.Subtype, "error"), message)
			return nil
		}
		parser.completed = true
	}
	return nil
}

func (parser *eventParser) parseStreamEvent(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var event streamEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil
	}
	switch event.Type {
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			parser.streamedContent = true
			return parser.publish("content", event.Delta.Text)
		case "thinking_delta":
			parser.streamedThought = true
			return parser.publish("reasoning", event.Delta.Thinking)
		}
	case "content_block_start":
		switch event.ContentBlock.Type {
		case "text":
			if text := strings.TrimSpace(event.ContentBlock.Text); text != "" {
				parser.streamedContent = true
				return parser.publish("content", text)
			}
		case "thinking":
			if thinking := strings.TrimSpace(event.ContentBlock.Thinking); thinking != "" {
				parser.streamedThought = true
				return parser.publish("reasoning", thinking)
			}
		}
	}
	return nil
}

func (parser *eventParser) parseAssistantMessage(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	streamedContent := parser.streamedContent
	streamedThought := parser.streamedThought
	parser.streamedContent = false
	parser.streamedThought = false
	var message assistantMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil
	}
	if model := strings.TrimSpace(message.Model); model != "" {
		parser.model = model
	}
	for _, content := range message.Content {
		switch content.Type {
		case "text":
			if !streamedContent {
				if err := parser.publish("content", content.Text); err != nil {
					return err
				}
			}
		case "thinking":
			if !streamedThought {
				if err := parser.publish("reasoning", content.Thinking); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (parser *eventParser) publish(channel, value string) error {
	if parser.onChunk == nil || value == "" {
		return nil
	}
	return parser.onChunk(Chunk{
		Model:     parser.model,
		SessionID: parser.sessionID,
		Channel:   channel,
		Text:      value,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
