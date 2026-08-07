package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

const setupHandlerTestChannelID = "setup-test"

type setupHandlerTestFactory struct{}

func (*setupHandlerTestFactory) Definition() chatchannel.Definition {
	return chatchannel.Definition{
		ID:    setupHandlerTestChannelID,
		Title: "Setup Test",
		Setup: &chatchannel.SetupDefinition{Kind: chatchannel.SetupKindQRCode},
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bot_token": map[string]any{"type": "string", "writeOnly": true},
				"base_url":  map[string]any{"type": "string"},
			},
			"required":             []any{"bot_token"},
			"additionalProperties": false,
		},
	}
}

func (*setupHandlerTestFactory) ValidateConfig(config map[string]any) error {
	if token, _ := config["bot_token"].(string); strings.TrimSpace(token) == "" {
		return errors.New("bot_token is required")
	}
	return nil
}

func (*setupHandlerTestFactory) New(chatchannel.InstanceConfig) (chatchannel.Instance, error) {
	return setupHandlerTestInstance{}, nil
}

func (*setupHandlerTestFactory) StartSetup(context.Context, chatchannel.SetupStartConfig) (chatchannel.SetupSession, chatchannel.SetupResult, error) {
	return &setupHandlerTestSession{}, chatchannel.SetupResult{
		Status:        chatchannel.SetupStatusWaiting,
		QRCodeContent: "setup://qr-content",
		ExpiresAt:     time.Now().Add(time.Minute),
	}, nil
}

func (*setupHandlerTestFactory) CredentialID(config map[string]any) string {
	value, _ := config["bot_token"].(string)
	return strings.TrimSpace(value)
}

type setupHandlerTestSession struct{}

func (*setupHandlerTestSession) Poll(_ context.Context, input chatchannel.SetupPollInput) (chatchannel.SetupResult, error) {
	if strings.TrimSpace(input.VerificationCode) == "" {
		return chatchannel.SetupResult{Status: chatchannel.SetupStatusVerificationRequired}, nil
	}
	if input.VerificationCode != "123456" {
		return chatchannel.SetupResult{}, fmt.Errorf("%w: invalid verification code", chatchannel.ErrInvalidSetupInput)
	}
	return chatchannel.SetupResult{
		Status:           chatchannel.SetupStatusConfirmed,
		Account:          &chatchannel.SetupAccount{ID: "bot-a", Label: "Bot A"},
		CredentialConfig: map[string]any{"bot_token": "setup-secret", "base_url": "https://example.test"},
	}, nil
}

type setupHandlerTestInstance struct{}

func (setupHandlerTestInstance) Run(context.Context) error { return nil }

func TestChatChannelSetupCreatesTriggerWithoutExposingCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channels := chatchannel.NewDefaultRegistry()
	if err := channels.Register(&setupHandlerTestFactory{}); err != nil {
		t.Fatal(err)
	}
	store, err := trigger.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := trigger.NewService(
		store,
		trigger.RunnerResolverFunc(func(context.Context, trigger.Target) (trigger.RunStarter, error) {
			return &triggerTestStarter{}, nil
		}),
		trigger.WithChatChannels(channels),
	)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(context.Background(), Config{
		Graph:          wfgraph.NewGraph(),
		BaseDir:        t.TempDir(),
		GraphID:        "graph",
		GraphVersion:   "v1",
		GraphSessionID: "setup-session",
		TriggerService: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	start := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/chat-channels/setup-test/setup-sessions", nil)
	engine.ServeHTTP(start, request)
	if start.Code != http.StatusCreated || strings.Contains(start.Body.String(), "setup-secret") {
		t.Fatalf("start status = %d, body = %s", start.Code, start.Body.String())
	}
	var startResponse struct {
		Data chatSetupPublicResult `json:"data"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startResponse); err != nil {
		t.Fatal(err)
	}
	if startResponse.Data.SessionID == "" || startResponse.Data.QRCodeContent != "setup://qr-content" {
		t.Fatalf("start response = %#v", startResponse.Data)
	}
	sessionID := startResponse.Data.SessionID

	verification := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/chat-channels/setup-test/setup-sessions/"+sessionID, nil)
	engine.ServeHTTP(verification, request)
	if verification.Code != http.StatusOK || !strings.Contains(verification.Body.String(), string(chatchannel.SetupStatusVerificationRequired)) {
		t.Fatalf("verification status = %d, body = %s", verification.Code, verification.Body.String())
	}

	confirmed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/chat-channels/setup-test/setup-sessions/"+sessionID+"/verification", strings.NewReader(`{"verification_code":"123456"}`))
	engine.ServeHTTP(confirmed, request)
	if confirmed.Code != http.StatusOK || strings.Contains(confirmed.Body.String(), "setup-secret") {
		t.Fatalf("confirmed status = %d, body = %s", confirmed.Code, confirmed.Body.String())
	}
	if !strings.Contains(confirmed.Body.String(), `"status":"confirmed"`) || !strings.Contains(confirmed.Body.String(), `"id":"bot-a"`) {
		t.Fatalf("confirmed body = %s", confirmed.Body.String())
	}

	invalidCreate := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/graphs/graph/triggers", strings.NewReader(`{"triggers":[{
		"id":"invalid trigger id","type":"chat","enabled":false,
		"chat":{"channel":"setup-test","channel_config":{},"stream_updates":true},
		"chat_setup_session_id":"`+sessionID+`"
	}]}`))
	engine.ServeHTTP(invalidCreate, request)
	if invalidCreate.Code != http.StatusBadRequest || strings.Contains(invalidCreate.Body.String(), "setup-secret") {
		t.Fatalf("invalid create status = %d, body = %s", invalidCreate.Code, invalidCreate.Body.String())
	}

	create := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/graphs/graph/triggers", strings.NewReader(`{"triggers":[{
		"id":"setup-chat","type":"chat","enabled":false,
		"chat":{"channel":"setup-test","channel_config":{},"stream_updates":true},
		"chat_setup_session_id":"`+sessionID+`"
	}]}`))
	engine.ServeHTTP(create, request)
	if create.Code != http.StatusOK || strings.Contains(create.Body.String(), "setup-secret") || strings.Contains(create.Body.String(), "chat_setup_session_id") {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	persisted, err := service.Get(context.Background(), "setup-chat")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Chat.ChannelConfig["bot_token"] != "setup-secret" || persisted.Chat.ChannelConfig["base_url"] != "https://example.test" {
		t.Fatalf("persisted config = %#v", persisted.Chat.ChannelConfig)
	}

	reuse := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/graphs/graph/triggers", strings.NewReader(`{"triggers":[{
		"id":"setup-chat-reuse","type":"chat","enabled":false,
		"chat":{"channel":"setup-test","channel_config":{},"stream_updates":true},
		"chat_setup_session_id":"`+sessionID+`"
	}]}`))
	engine.ServeHTTP(reuse, request)
	if reuse.Code != http.StatusNotFound {
		t.Fatalf("reuse status = %d, body = %s", reuse.Code, reuse.Body.String())
	}

	duplicateStart := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/chat-channels/setup-test/setup-sessions", nil)
	engine.ServeHTTP(duplicateStart, request)
	if duplicateStart.Code != http.StatusCreated || strings.Contains(duplicateStart.Body.String(), "setup-secret") {
		t.Fatalf("duplicate start status = %d, body = %s", duplicateStart.Code, duplicateStart.Body.String())
	}
	var duplicateStartResponse struct {
		Data chatSetupPublicResult `json:"data"`
	}
	if err := json.Unmarshal(duplicateStart.Body.Bytes(), &duplicateStartResponse); err != nil {
		t.Fatal(err)
	}
	duplicateSessionID := duplicateStartResponse.Data.SessionID
	if duplicateSessionID == "" {
		t.Fatalf("duplicate start response = %#v", duplicateStartResponse.Data)
	}

	duplicateConfirmed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/chat-channels/setup-test/setup-sessions/"+duplicateSessionID+"/verification", strings.NewReader(`{"verification_code":"123456"}`))
	engine.ServeHTTP(duplicateConfirmed, request)
	if duplicateConfirmed.Code != http.StatusOK || !strings.Contains(duplicateConfirmed.Body.String(), `"status":"confirmed"`) || strings.Contains(duplicateConfirmed.Body.String(), "setup-secret") {
		t.Fatalf("duplicate confirmed status = %d, body = %s", duplicateConfirmed.Code, duplicateConfirmed.Body.String())
	}

	duplicateCreate := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/graphs/graph/triggers", strings.NewReader(`{"triggers":[{
		"id":"setup-chat-duplicate","type":"chat","enabled":false,
		"chat":{"channel":"setup-test","channel_config":{},"stream_updates":true},
		"chat_setup_session_id":"`+duplicateSessionID+`"
	}]}`))
	engine.ServeHTTP(duplicateCreate, request)
	if duplicateCreate.Code != http.StatusConflict || strings.Contains(duplicateCreate.Body.String(), "setup-secret") || strings.Contains(duplicateCreate.Body.String(), duplicateSessionID) {
		t.Fatalf("duplicate create status = %d, body = %s", duplicateCreate.Code, duplicateCreate.Body.String())
	}
}
