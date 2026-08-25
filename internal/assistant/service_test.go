package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/llms"
)

type fakeModel struct {
	mu       sync.Mutex
	requests []llms.ModelRequest
	respond  func(llms.ModelRequest, int) *llms.ModelResponse
}

func (m *fakeModel) Generate(_ context.Context, request llms.ModelRequest) (*llms.ModelResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	return m.respond(request, len(m.requests)), nil
}

func waitForJob(t *testing.T, service *Service, id string) Job {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err := service.GetJob(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" || job.Status == "failed" {
			return job
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("timed out waiting for assistant job")
	return Job{}
}

func waitForJobUpdate(t *testing.T, updates <-chan Job, match func(Job) bool) Job {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case job, ok := <-updates:
			if !ok {
				t.Fatal("assistant job updates closed before the expected state")
			}
			if match(job) {
				return job
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for assistant job update")
		}
	}
}

func TestServiceProcessesJobAndKeepsRecentRounds(t *testing.T) {
	model := &fakeModel{respond: func(_ llms.ModelRequest, _ int) *llms.ModelResponse {
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "ok"}}}
	}}
	service, err := New(Config{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	service.maxHistory = 1
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, input := range []string{"one", "two"} {
		job, submitErr := service.Submit(SubmitRequest{SessionID: "session", Message: input})
		if submitErr != nil {
			t.Fatal(submitErr)
		}
		if result := waitForJob(t, service, job.ID); result.Reply != "ok" {
			t.Fatalf("job = %#v", result)
		}
	}
	session := service.GetSession("session")
	if len(session.Messages) != 2 || session.Messages[0].Content != "two" {
		t.Fatalf("recent session messages = %#v", session.Messages)
	}
}

func TestServiceUsesServerAPITool(t *testing.T) {
	var got APICall
	model := &fakeModel{respond: func(_ llms.ModelRequest, callNumber int) *llms.ModelResponse {
		if callNumber == 1 {
			arguments, _ := json.Marshal(map[string]any{"method": "GET", "path": "/registry"})
			return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "Inspecting the Registry.", ToolCalls: []llms.ToolCall{{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "server_api", Arguments: arguments}}}}}}
		}
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "registry inspected"}}}
	}}
	service, err := New(Config{Model: model, APICaller: func(_ context.Context, call APICall) (APIResult, error) {
		got = call
		return APIResult{Status: 200, Body: []byte(`{"data":{}}`)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	job, err := service.Submit(SubmitRequest{SessionID: "tools", Message: "inspect registry"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForJob(t, service, job.ID)
	if result.Status != "completed" || result.Reply != "registry inspected" {
		t.Fatalf("job = %#v", result)
	}
	if got.Method != "GET" || got.Path != "/registry" {
		t.Fatalf("API call = %#v", got)
	}
	if len(result.Activities) != 1 || result.Activities[0].Content != "Inspecting the Registry." || result.Activities[0].APICallCount != 1 {
		t.Fatalf("activities = %#v", result.Activities)
	}
	model.mu.Lock()
	secondRequest := model.requests[1]
	model.mu.Unlock()
	assistantMessage := secondRequest.Messages[len(secondRequest.Messages)-2]
	text, ok := assistantMessage.Parts[0].(llms.TextContent)
	if !ok || text.Text != "Inspecting the Registry." {
		t.Fatalf("assistant tool-call message = %#v", assistantMessage)
	}
}

func TestServicePublishesFallbackActivityBeforeAPICallsComplete(t *testing.T) {
	firstArguments, _ := json.Marshal(map[string]any{"method": "GET", "path": "/registry"})
	secondArguments, _ := json.Marshal(map[string]any{"method": "GET", "path": "/graphs"})
	model := &fakeModel{respond: func(_ llms.ModelRequest, callNumber int) *llms.ModelResponse {
		if callNumber == 1 {
			return &llms.ModelResponse{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{
				{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "server_api", Arguments: firstArguments}},
				{ID: "call-2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "server_api", Arguments: secondArguments}},
			}}}}
		}
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "done"}}}
	}}
	apiEntered := make(chan struct{}, 1)
	releaseAPI := make(chan struct{})
	service, err := New(Config{Model: model, APICaller: func(_ context.Context, _ APICall) (APIResult, error) {
		select {
		case apiEntered <- struct{}{}:
		default:
		}
		<-releaseAPI
		return APIResult{Status: 200, Body: []byte(`{"data":{}}`)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseAPI) }) }
	defer func() {
		release()
		_ = service.Close()
	}()

	job, err := service.Submit(SubmitRequest{SessionID: "fallback", Message: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe, err := service.WatchJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	select {
	case <-apiEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server API call")
	}
	running := waitForJobUpdate(t, updates, func(update Job) bool { return len(update.Activities) == 1 })
	if running.Status != "running" || len(running.Activities) != 1 {
		t.Fatalf("running job = %#v", running)
	}
	activity := running.Activities[0]
	if activity.Round != 1 || activity.Content != "Calling 2 server APIs..." || activity.APICallCount != 2 {
		t.Fatalf("activity = %#v", activity)
	}
	running.Activities[0].Content = "mutated copy"
	current, err := service.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Activities[0].Content != "Calling 2 server APIs..." {
		t.Fatalf("GetJob returned shared activity state: %#v", current.Activities)
	}

	release()
	completed := waitForJobUpdate(t, updates, func(update Job) bool { return update.Status == "completed" })
	if completed.Reply != "done" {
		t.Fatalf("completed job = %#v", completed)
	}
}

func TestServiceSynthesizesAfterExtendedToolCallRounds(t *testing.T) {
	arguments, _ := json.Marshal(map[string]any{"method": "GET", "path": "/registry"})
	model := &fakeModel{respond: func(request llms.ModelRequest, _ int) *llms.ModelResponse {
		if len(request.Tools) == 0 {
			return &llms.ModelResponse{Choices: []*llms.ModelChoice{{
				Content: "registry inspected after extended analysis",
				ToolCalls: []llms.ToolCall{{
					ID: "stale-call", Type: "function", FunctionCall: &llms.FunctionCall{Name: "server_api", Arguments: arguments},
				}},
			}}}
		}
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{ToolCalls: []llms.ToolCall{{
			ID: "call", Type: "function", FunctionCall: &llms.FunctionCall{Name: "server_api", Arguments: arguments},
		}}}}}
	}}
	service, err := New(Config{Model: model, APICaller: func(context.Context, APICall) (APIResult, error) {
		return APIResult{Status: 200, Body: []byte(`{"data":{}}`)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	job, err := service.Submit(SubmitRequest{SessionID: "extended", Message: "inspect deeply"})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForJob(t, service, job.ID)
	if completed.Status != "completed" || completed.Reply != "registry inspected after extended analysis" {
		t.Fatalf("job = %#v", completed)
	}
	if len(completed.Activities) != maxToolCallRounds {
		t.Fatalf("activities = %d, want %d", len(completed.Activities), maxToolCallRounds)
	}
	model.mu.Lock()
	requests := append([]llms.ModelRequest(nil), model.requests...)
	model.mu.Unlock()
	if len(requests) != maxToolCallRounds+1 || len(requests[len(requests)-1].Tools) != 0 {
		t.Fatalf("model requests = %d, final tools = %d", len(requests), len(requests[len(requests)-1].Tools))
	}
}

func TestExecuteAPICallRejectsAssistantAndTraversalPaths(t *testing.T) {
	caller := func(context.Context, APICall) (APIResult, error) { return APIResult{}, nil }
	for _, path := range []string{"/assistant/status", "/graphs/../registry", "registry"} {
		if _, err := executeAPICall(context.Background(), caller, serverAPIRequest{Method: "GET", Path: path}); err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func TestDefaultSystemPromptDefinesOperatingProtocol(t *testing.T) {
	for _, rule := range []string{
		"Read before write",
		"server_api is the only server-operation entry point",
		"Treat explicit Graph mutation verbs",
		"design, recommendation, JSON only",
		"unsaved Workbench draft",
		"effect_status=unknown",
		"read-only",
		"user-facing status sentence",
		"Finish within the available server_api rounds",
		"Response format",
	} {
		if !strings.Contains(defaultSystemPrompt, rule) {
			t.Fatalf("default system prompt is missing rule %q", rule)
		}
	}
}

func TestNewUsesLargeContextDefaults(t *testing.T) {
	service, err := New(Config{Model: &fakeModel{respond: func(_ llms.ModelRequest, _ int) *llms.ModelResponse {
		return &llms.ModelResponse{Choices: []*llms.ModelChoice{{Content: "ok"}}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if service.maxContextBytes != defaultMaxContextBytes || service.maxAPIResultBytes != defaultMaxAPIResultBytes {
		t.Fatalf("budgets = context %d, API result %d", service.maxContextBytes, service.maxAPIResultBytes)
	}
	if defaultContextWindowTokens != 256*1024 || service.maxTokens != defaultMaxTokens {
		t.Fatalf("token defaults = context window %d, output %d", defaultContextWindowTokens, service.maxTokens)
	}
	if service.jobTimeout != 10*time.Minute {
		t.Fatalf("job timeout = %s", service.jobTimeout)
	}
}

func TestMarshalWorkbenchContextNeverReturnsBrokenJSON(t *testing.T) {
	encoded, err := marshalWorkbenchContext(Context{GraphID: "graph", Definition: map[string]any{"large": strings.Repeat("x", 1000)}}, 64)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("context JSON = %s: %v", encoded, err)
	}
	if decoded["definition_truncated"] != true {
		t.Fatalf("context = %#v", decoded)
	}
}
