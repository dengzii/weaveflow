package chatchannel

import "testing"

func TestInboundMessageValidate(t *testing.T) {
	if err := (InboundMessage{}).Validate(); err == nil {
		t.Fatal("empty chat message should fail validation")
	}
	message := (InboundMessage{ID: " id ", UserID: " user ", ConversationID: " chat ", Content: " hello "}).Normalize()
	if message.ID != "id" || message.UserID != "user" || message.ConversationID != "chat" || message.Content != "hello" {
		t.Fatalf("normalized message = %#v", message)
	}
}
