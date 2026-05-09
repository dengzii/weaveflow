package neo

import (
	"net/http"

	"weaveflow/memory"

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
	Role      string        `json:"role"`
	Parts     []MessagePart `json:"parts,omitempty"`
	Status    string        `json:"status,omitempty"`
	CreatedAt int64         `json:"created_at,omitempty"`
}

type MessagePart struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Result string `json:"result,omitempty"`
	ID     string `json:"id,omitempty"` // tool call id, ignored by frontend
}

func (ctrl *HistoryController) Get(c *gin.Context) {
	history, err := ctrl.chatCtrl.GetHistory()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": history})
}

func (ctrl *HistoryController) Clear(c *gin.Context) {
	if err := ctrl.chatCtrl.ClearHistory(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok"})
}

func (ctrl *HistoryController) GetMemory(c *gin.Context) {
	items, err := ctrl.chatCtrl.GetMemory()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": convertMemoryEntries(items)})
}

func (ctrl *HistoryController) ClearMemory(c *gin.Context) {
	if err := ctrl.chatCtrl.ClearMemory(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok"})
}

// convertMessages converts llms messages to HistoryMessages.
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
		mp := MessagePart{Type: "tool_call", ID: p.ID}
		if p.FunctionCall != nil {
			mp.Name = p.FunctionCall.Name
			mp.Text = p.FunctionCall.Arguments
		}
		return mp
	case llms.ToolCallResponse:
		return MessagePart{Type: "tool_result", ID: p.ToolCallID, Name: p.Name, Result: p.Content}
	default:
		return MessagePart{Type: "unknown"}
	}
}

func historyToLLM(hm HistoryMessage) llms.MessageContent {
	msg := llms.MessageContent{Role: llms.ChatMessageType(hm.Role)}
	for _, p := range hm.Parts {
		switch p.Type {
		case "text":
			msg.Parts = append(msg.Parts, llms.TextContent{Text: p.Text})
		case "thinking":
			// reasoning content is display-only; not sent back to the LLM
		case "tool_call":
			msg.Parts = append(msg.Parts, llms.ToolCall{
				ID:           p.ID,
				Type:         "function",
				FunctionCall: &llms.FunctionCall{Name: p.Name, Arguments: p.Text},
			})
		case "tool_result":
			msg.Parts = append(msg.Parts, llms.ToolCallResponse{
				ToolCallID: p.ID,
				Name:       p.Name,
				Content:    p.Result,
			})
		}
	}
	return msg
}

type MemoryEntry struct {
	ID        string         `json:"id"`
	Text      string         `json:"text"`
	Role      string         `json:"role"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt int64          `json:"created_at"`
	Type      string         `json:"type"`
	Tags      []string       `json:"tags,omitempty"`
}

func convertMemoryEntries(items []memory.Entry) []MemoryEntry {
	result := make([]MemoryEntry, 0, len(items))
	for _, item := range items {
		result = append(result, MemoryEntry{
			ID:        item.ID,
			Text:      item.Text,
			Role:      item.Role,
			Payload:   item.Payload,
			CreatedAt: item.CreatedAt.UnixMilli(),
			Type:      string(item.Type),
			Tags:      item.Tags,
		})
	}
	return result
}
