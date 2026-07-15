package conversation

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/dengzii/weaveflow/llms/parts"

	"github.com/tmc/langchaingo/llms"
)

type Message struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts,omitempty"`
}

type MessagePart struct {
	Kind         string `json:"kind"`
	Text         string `json:"text,omitempty"`
	URL          string `json:"url,omitempty"`
	Detail       string `json:"detail,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Data         string `json:"data,omitempty"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	ToolType     string `json:"tool_type,omitempty"`
	FunctionName string `json:"function_name,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	Name         string `json:"name,omitempty"`
	Content      string `json:"content,omitempty"`
}

func SerializeMessages(messages []llms.MessageContent) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	result := make([]Message, 0, len(messages))
	for _, message := range messages {
		item := Message{Role: string(message.Role)}
		for _, part := range message.Parts {
			encoded, err := serializeMessagePart(part)
			if err != nil {
				return nil, err
			}
			item.Parts = append(item.Parts, encoded)
		}
		result = append(result, item)
	}
	return result, nil
}

func DeserializeMessages(messages []Message) ([]llms.MessageContent, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	result := make([]llms.MessageContent, 0, len(messages))
	for _, message := range messages {
		item := llms.MessageContent{
			Role:  llms.ChatMessageType(message.Role),
			Parts: []llms.ContentPart{},
		}
		for _, part := range message.Parts {
			decoded, err := deserializeMessagePart(part)
			if err != nil {
				return nil, err
			}
			item.Parts = append(item.Parts, decoded)
		}
		result = append(result, item)
	}
	return result, nil
}

func EncodeMessages(messages []llms.MessageContent) ([]any, error) {
	encoded, err := SerializeMessages(messages)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(encoded)
	if err != nil {
		return nil, err
	}
	var value []any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func DecodeMessages(value any) ([]llms.MessageContent, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []llms.MessageContent:
		return cloneMessages(typed), nil
	case []Message:
		return DeserializeMessages(typed)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var encoded []Message
	if err := json.Unmarshal(payload, &encoded); err != nil {
		return nil, err
	}
	return DeserializeMessages(encoded)
}

func serializeMessagePart(part llms.ContentPart) (MessagePart, error) {
	switch typed := part.(type) {
	case llms.TextContent:
		return MessagePart{Kind: "text", Text: typed.Text}, nil
	case parts.ReasoningPart:
		return MessagePart{Kind: "reasoning", Text: typed.Text}, nil
	case llms.ImageURLContent:
		return MessagePart{Kind: "image_url", URL: typed.URL, Detail: typed.Detail}, nil
	case llms.BinaryContent:
		return MessagePart{
			Kind:     "binary",
			MIMEType: typed.MIMEType,
			Data:     base64.StdEncoding.EncodeToString(typed.Data),
		}, nil
	case llms.ToolCall:
		part := MessagePart{
			Kind:       "tool_call",
			ToolCallID: typed.ID,
			ToolType:   typed.Type,
		}
		if typed.FunctionCall != nil {
			part.FunctionName = typed.FunctionCall.Name
			part.Arguments = typed.FunctionCall.Arguments
		}
		return part, nil
	case llms.ToolCallResponse:
		return MessagePart{
			Kind:       "tool_response",
			ToolCallID: typed.ToolCallID,
			Name:       typed.Name,
			Content:    typed.Content,
		}, nil
	default:
		return MessagePart{}, fmt.Errorf("unsupported message part type %T", part)
	}
}

func deserializeMessagePart(part MessagePart) (llms.ContentPart, error) {
	switch part.Kind {
	case "text":
		return llms.TextPart(part.Text), nil
	case "reasoning":
		return parts.NewReasoningPart(part.Text), nil
	case "image_url":
		return llms.ImageURLContent{URL: part.URL, Detail: part.Detail}, nil
	case "binary":
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			return nil, err
		}
		return llms.BinaryContent{MIMEType: part.MIMEType, Data: data}, nil
	case "tool_call":
		toolCall := llms.ToolCall{ID: part.ToolCallID, Type: part.ToolType}
		if part.FunctionName != "" {
			toolCall.FunctionCall = &llms.FunctionCall{Name: part.FunctionName, Arguments: part.Arguments}
		}
		return toolCall, nil
	case "tool_response":
		return llms.ToolCallResponse{
			ToolCallID: part.ToolCallID,
			Name:       part.Name,
			Content:    part.Content,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported conversation message part kind %q", part.Kind)
	}
}
