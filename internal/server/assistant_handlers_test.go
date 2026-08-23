package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/internal/assistant"
	"github.com/dengzii/weaveflow/llms"
	"github.com/gin-gonic/gin"
)

type assistantTestModel struct{}

func (assistantTestModel) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "assistant reply"}}}, nil
}

func TestAssistantRoutesSubmitAndStreamJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{
		BaseDir:   t.TempDir(),
		Assistant: &assistant.Config{Model: assistantTestModel{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	request := httptest.NewRequest(http.MethodPost, "/assistant/sessions/test/messages", strings.NewReader(`{"message":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data assistant.Job `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	streamRequest := httptest.NewRequest(http.MethodGet, "/assistant/jobs/"+envelope.Data.ID+"/stream", nil)
	streamRequest.Header.Set("Accept", "text/event-stream")
	streamResponse := httptest.NewRecorder()
	engine.ServeHTTP(streamResponse, streamRequest)
	if streamResponse.Code != http.StatusOK || streamResponse.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status = %d content-type = %q body = %s", streamResponse.Code, streamResponse.Header().Get("Content-Type"), streamResponse.Body.String())
	}
	if body := streamResponse.Body.String(); !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"reply":"assistant reply"`) {
		t.Fatalf("assistant job stream = %q", body)
	}

	jobResponse := httptest.NewRecorder()
	engine.ServeHTTP(jobResponse, httptest.NewRequest(http.MethodGet, "/assistant/jobs/"+envelope.Data.ID, nil))
	var jobEnvelope struct {
		Data assistant.Job `json:"data"`
	}
	if err := json.Unmarshal(jobResponse.Body.Bytes(), &jobEnvelope); err != nil {
		t.Fatal(err)
	}
	if jobEnvelope.Data.Status != "completed" || jobEnvelope.Data.Reply != "assistant reply" {
		t.Fatalf("job = %#v", jobEnvelope.Data)
	}
}
