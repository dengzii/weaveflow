package llms

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

type ChatMessageType string

const (
	ChatMessageTypeSystem  ChatMessageType = "system"
	ChatMessageTypeHuman   ChatMessageType = "human"
	ChatMessageTypeAI      ChatMessageType = "assistant"
	ChatMessageTypeTool    ChatMessageType = "tool"
	ChatMessageTypeGeneric ChatMessageType = "generic"
)

type MessageContent struct {
	Role  ChatMessageType `json:"role"`
	Parts []ContentPart   `json:"-"`
}

func (message MessageContent) MarshalJSON() ([]byte, error) {
	if len(message.Parts) == 1 {
		if text, ok := message.Parts[0].(TextContent); ok {
			return json.Marshal(struct {
				Role ChatMessageType `json:"role"`
				Text string          `json:"text"`
			}{Role: message.Role, Text: text.Text})
		}
	}
	return json.Marshal(struct {
		Role  ChatMessageType `json:"role"`
		Parts []ContentPart   `json:"parts"`
	}{Role: message.Role, Parts: message.Parts})
}

type ContentPart interface {
	isContentPart()
}

type TextContent struct {
	Text string `json:"text"`
}

func (content TextContent) String() string { return content.Text }
func (TextContent) isContentPart()         {}

func (content TextContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"type": "text", "text": content.Text})
}

type ReasoningContent struct {
	Text string `json:"text"`
}

func (content ReasoningContent) String() string { return content.Text }
func (ReasoningContent) isContentPart()         {}

func (content ReasoningContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"type": "reasoning", "text": content.Text})
}

type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (content ImageURLContent) String() string { return content.URL }
func (ImageURLContent) isContentPart()         {}

func (content ImageURLContent) MarshalJSON() ([]byte, error) {
	payload := map[string]string{"url": content.URL}
	if content.Detail != "" {
		payload["detail"] = content.Detail
	}
	return json.Marshal(map[string]any{"type": "image_url", "image_url": payload})
}

type BinaryContent struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

func (content BinaryContent) String() string {
	return "data:" + content.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(content.Data)
}

func (BinaryContent) isContentPart() {}

func (content BinaryContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "binary",
		"binary": map[string]string{
			"mime_type": content.MIMEType,
			"data":      base64.StdEncoding.EncodeToString(content.Data),
		},
	})
}

type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolCall struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	FunctionCall *FunctionCall `json:"function,omitempty"`
}

func (ToolCall) isContentPart() {}

type ToolResult struct {
	ToolCallID   string `json:"tool_call_id"`
	Name         string `json:"name"`
	Content      string `json:"content,omitempty"`
	Value        any    `json:"value,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func (ToolResult) isContentPart() {}

func TextPart(text string) TextContent {
	return TextContent{Text: text}
}

func ReasoningPart(text string) ReasoningContent {
	return ReasoningContent{Text: text}
}

func BinaryPart(mimeType string, data []byte) BinaryContent {
	return BinaryContent{MIMEType: mimeType, Data: append([]byte(nil), data...)}
}

func ImageURLPart(url string) ImageURLContent {
	return ImageURLContent{URL: url}
}

func ImageURLWithDetailPart(url, detail string) ImageURLContent {
	return ImageURLContent{URL: url, Detail: detail}
}

func TextParts(role ChatMessageType, parts ...string) MessageContent {
	message := MessageContent{Role: role, Parts: make([]ContentPart, 0, len(parts))}
	for _, part := range parts {
		message.Parts = append(message.Parts, TextPart(part))
	}
	return message
}

func CloneMessages(messages []MessageContent) []MessageContent {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]MessageContent, len(messages))
	for index, message := range messages {
		cloned[index] = MessageContent{Role: message.Role, Parts: append([]ContentPart(nil), message.Parts...)}
	}
	return cloned
}

func ToolResultText(result ToolResult) string {
	if result.Content != "" {
		return result.Content
	}
	if result.Value == nil {
		return ""
	}
	payload, err := json.Marshal(result.Value)
	if err != nil {
		return fmt.Sprint(result.Value)
	}
	return string(payload)
}

func ShowMessageContents(writer io.Writer, messages []MessageContent) {
	fmt.Fprintf(writer, "MessageContent (len=%v)\n", len(messages))
	for messageIndex, message := range messages {
		fmt.Fprintf(writer, "[%d]: Role=%s\n", messageIndex, message.Role)
		for partIndex, part := range message.Parts {
			fmt.Fprintf(writer, "  Parts[%d]: %T %v\n", partIndex, part, part)
		}
	}
}
