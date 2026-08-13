package codex

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Usage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
}

type eventParser struct {
	modelID    string
	onChunk    func(Chunk) error
	threadID   string
	output     string
	usage      Usage
	events     []json.RawMessage
	completed  bool
	failed     error
	diagnostic string
}

type Chunk struct {
	ModelID  string `json:"model_id"`
	ThreadID string `json:"thread_id,omitempty"`
	Channel  string `json:"channel"`
	Text     string `json:"text"`
}

type eventEnvelope struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id,omitempty"`
	Item     json.RawMessage `json:"item,omitempty"`
	Delta    string          `json:"delta,omitempty"`
	Usage    Usage           `json:"usage,omitempty"`
	Message  string          `json:"message,omitempty"`
	Error    json.RawMessage `json:"error,omitempty"`
}

type eventItem struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Delta string `json:"delta,omitempty"`
}

func newCodexEventParser(modelID string, onChunk func(Chunk) error) *eventParser {
	return &eventParser{modelID: modelID, onChunk: onChunk}
}

func (parser *eventParser) parse(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	var event eventEnvelope
	if err := json.Unmarshal(line, &event); err != nil {
		return fmt.Errorf("decode Codex JSONL event: %w", err)
	}
	if strings.TrimSpace(event.Type) == "" {
		return fmt.Errorf("decode Codex JSONL event: type is required")
	}
	parser.events = append(parser.events, append(json.RawMessage(nil), line...))

	switch event.Type {
	case "thread.started":
		parser.threadID = strings.TrimSpace(event.ThreadID)
	case "item.updated", "item.completed":
		var item eventItem
		if len(event.Item) == 0 || json.Unmarshal(event.Item, &item) != nil {
			return nil
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			text = strings.TrimSpace(item.Delta)
		}
		if text == "" {
			text = strings.TrimSpace(event.Delta)
		}
		channel := ""
		switch item.Type {
		case "agent_message":
			channel = "content"
			if event.Type == "item.completed" && text != "" {
				parser.output = text
			}
		case "reasoning":
			channel = "reasoning"
		}
		if channel != "" && text != "" && parser.onChunk != nil {
			if err := parser.onChunk(Chunk{
				ModelID:  parser.modelID,
				ThreadID: parser.threadID,
				Channel:  channel,
				Text:     text,
			}); err != nil {
				return err
			}
		}
	case "turn.completed":
		parser.usage = event.Usage
		parser.completed = true
	case "turn.failed":
		message := strings.TrimSpace(event.Message)
		if message == "" {
			message = errorMessage(event.Error)
		}
		if message == "" {
			message = event.Type
		}
		parser.failed = fmt.Errorf("Codex reported %s: %s", event.Type, message)
	case "error":
		message := strings.TrimSpace(event.Message)
		if message == "" {
			message = errorMessage(event.Error)
		}
		if message == "" {
			message = event.Type
		}
		parser.diagnostic = message
	}
	return nil
}

func errorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		return strings.TrimSpace(payload.Message)
	}
	return ""
}
