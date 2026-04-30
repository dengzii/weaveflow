package neo

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
)

type HistoryController struct {
	chatCtrl *ChatController
}

func NewHistoryController(chatCtrl *ChatController) *HistoryController {
	return &HistoryController{
		chatCtrl: chatCtrl,
	}
}

type HistoryMessage struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts,omitempty"`
}

type MessagePart struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Result string `json:"result,omitempty"`
}

func (ctrl *HistoryController) Get(c *gin.Context) {
	state := ctrl.chatCtrl.GetLastState()

	if state == nil {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": []HistoryMessage{}})
		return
	}

	conversation := state.Conversation(stateScope)
	messages := conversation.Messages()
	history := convertMessages(messages)

	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": history})
}

func convertMessages(messages []llms.MessageContent) []HistoryMessage {
	result := make([]HistoryMessage, 0, len(messages))
	for _, msg := range messages {
		hm := HistoryMessage{Role: string(msg.Role)}
		for _, part := range msg.Parts {
			hm.Parts = append(hm.Parts, convertPart(part))
		}
		result = append(result, hm)
	}
	return result
}

func convertPart(part llms.ContentPart) MessagePart {
	switch p := part.(type) {
	case llms.TextContent:
		return MessagePart{Type: "text", Text: p.Text}
	case llms.ToolCall:
		return MessagePart{Type: "tool_call", Name: p.FunctionCall.Name, Text: p.FunctionCall.Arguments}
	case llms.ToolCallResponse:
		return MessagePart{Type: "tool_result", Name: p.Name, Result: p.Content}
	default:
		return MessagePart{Type: "unknown"}
	}
}
