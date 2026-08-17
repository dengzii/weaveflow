package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/llms"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
)

type triggerRuntimeTestModel struct {
	id string
}

type rotatingRuntimeSecretResolver struct {
	values map[string]string
}

func (resolver *rotatingRuntimeSecretResolver) Resolve(_ context.Context, ref dsl.SecretRef) (string, error) {
	if ref.Source != "env" {
		return "", fmt.Errorf("unexpected secret source %q", ref.Source)
	}
	value, ok := resolver.values[ref.Ref]
	if !ok {
		return "", fmt.Errorf("secret %q is not configured", ref.Ref)
	}
	return value, nil
}

func (*triggerRuntimeTestModel) Generate(context.Context, llms.ModelRequest) (*llms.ModelResponse, error) {
	return &llms.ModelResponse{}, nil
}

func TestResolveTriggerRunnerUsesLatestGraphSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	uploader, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	uploader.RegisterRoutes(engine.Group(""))

	first := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "first"))
	resolved, err := uploader.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID() != first.Graph.GraphSessionID {
		t.Fatalf("resolved first graph session = %q, want %q", runner.GraphSessionID(), first.Graph.GraphSessionID)
	}
	second := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v2", "second"))

	resolver, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = resolver.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	runner := resolvedGraphRunner(t, resolved)
	if runner.GraphVersion() != "v2" || runner.GraphSessionID() != second.Graph.GraphSessionID {
		t.Fatalf("resolved graph = version %q session %q, want version v2 session %q", runner.GraphVersion(), runner.GraphSessionID(), second.Graph.GraphSessionID)
	}

	third := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v3", "third"))
	resolved, err = resolver.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	runner = resolvedGraphRunner(t, resolved)
	if runner.GraphVersion() != "v3" || runner.GraphSessionID() != third.Graph.GraphSessionID {
		t.Fatalf("resolved graph after upload = version %q session %q, want version v3 session %q", runner.GraphVersion(), runner.GraphSessionID(), third.Graph.GraphSessionID)
	}
}

func TestResolveTriggerRunnerRefreshesCachedSessionSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OPENAI_API_KEY", "")
	secretResolver := &rotatingRuntimeSecretResolver{values: map[string]string{
		"SERVICE_TOKEN": "service-token-1",
	}}
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), SecretResolver: secretResolver})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	settings := `{
		"environment": {},
		"environment_secrets": {
			"SERVICE_TOKEN": {"source":"env","ref":"SERVICE_TOKEN"}
		},
		"models": [{
			"id":"default",
			"enabled":true,
			"provider":"openai",
			"credential_value":"model-token-1",
			"model":"gpt-test",
			"base_url":"http://127.0.0.1:9999/v1"
		}]
	}`
	uploaded := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("graph-a", "v1", "secret-refresh", settings))

	first, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	firstStarter, ok := first.(*triggerRunStarter)
	if !ok {
		t.Fatalf("first resolved runner = %T, want *triggerRunStarter", first)
	}
	assertTriggerRuntimeSecrets(t, firstStarter.baseContext, "model-token-1", "service-token-1")

	secretResolver.values["SERVICE_TOKEN"] = "service-token-2"
	rotatedSettings := strings.Replace(settings, "model-token-1", "model-token-2", 1)
	rotated := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("graph-a", "v1", "secret-refresh", rotatedSettings))
	if rotated.Graph.GraphSessionID != uploaded.Graph.GraphSessionID {
		t.Fatalf("model credential rotation created session %q, want %q", rotated.Graph.GraphSessionID, uploaded.Graph.GraphSessionID)
	}
	second, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	secondStarter, ok := second.(*triggerRunStarter)
	if !ok {
		t.Fatalf("second resolved runner = %T, want *triggerRunStarter", second)
	}
	assertTriggerRuntimeSecrets(t, secondStarter.baseContext, "model-token-2", "service-token-2")
	if secondStarter.runner != firstStarter.runner || secondStarter.graph != firstStarter.graph {
		t.Fatal("secret refresh replaced the cached graph session")
	}
	if runner := resolvedGraphRunner(t, second); runner.GraphSessionID() != uploaded.Graph.GraphSessionID {
		t.Fatalf("secret refresh session = %q, want %q", runner.GraphSessionID(), uploaded.Graph.GraphSessionID)
	}
}

func assertTriggerRuntimeSecrets(t *testing.T, ctx context.Context, modelToken string, serviceToken string) {
	t.Helper()
	modelConfig, ok := core.ModelConfigByIDFromContext(ctx, core.DefaultModelID)
	if !ok || modelConfig.APIKey != modelToken {
		t.Fatalf("trigger model config = %#v, want API key %q", modelConfig, modelToken)
	}
	if got := core.EnvironmentVariableFromContext(ctx, "SERVICE_TOKEN"); got != serviceToken {
		t.Fatalf("trigger SERVICE_TOKEN = %q, want %q", got, serviceToken)
	}
}

func TestFailedGraphUploadKeepsPreviousSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	previous := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "previous"))

	failed := serveHTTP(engine, "POST", "/graphs/graph-a/sessions", `{
		"graph_version":"v2",
		"definition":{"version":"1.0","name":"invalid","nodes":[]},
		"settings":{"environment":{},"models":[]}
	}`)
	if failed.Code != 400 {
		t.Fatalf("failed upload status = %d, body = %s", failed.Code, failed.Body.String())
	}

	resolved, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	runner := resolvedGraphRunner(t, resolved)
	if runner.GraphSessionID() != previous.Graph.GraphSessionID {
		t.Fatalf("session after failed upload = %q, want %q", runner.GraphSessionID(), previous.Graph.GraphSessionID)
	}
}

func TestTriggerRunOriginIsReturnedByRunList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	credentialResolver := &triggerCredentialResolver{value: "origin-secret"}
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), SecretResolver: credentialResolver})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	uploaded := putGraphForHashTest(t, engine, triggerGraphUploadBody("origin-graph", "v1", "origin"))

	replaced := serveHTTP(engine, http.MethodPut, "/graphs/origin-graph/triggers", `{"triggers":[{
		"id":"hook","type":"webhook","enabled":true,
		"credential":{"source":"env","ref":"TRIGGER_TOKEN"},"webhook":{}
	}]}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace triggers status = %d, body = %s", replaced.Code, replaced.Body.String())
	}

	invoked := requestWithHeader(engine, http.MethodPost, "/graphs/origin-graph/triggers/hook/invocations", `{}`, "Authorization", "Bearer origin-secret")
	if invoked.Code != http.StatusAccepted {
		t.Fatalf("invoke trigger status = %d, body = %s", invoked.Code, invoked.Body.String())
	}
	var invocationEnvelope struct {
		Data triggerInvocationResponse `json:"data"`
	}
	if err := json.Unmarshal(invoked.Body.Bytes(), &invocationEnvelope); err != nil {
		t.Fatalf("decode trigger invocation: %v", err)
	}
	runID := invocationEnvelope.Data.Run.RunID
	if runID == "" {
		t.Fatal("trigger invocation run id is empty")
	}

	listed := serveHTTP(engine, http.MethodGet, "/graphs/origin-graph/runs", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list runs status = %d, body = %s", listed.Code, listed.Body.String())
	}
	var listEnvelope struct {
		Data runListPage `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatalf("decode run list: %v", err)
	}
	for _, run := range listEnvelope.Data.Items {
		if run.RunID != runID {
			continue
		}
		if run.Origin == nil || run.Origin.Type != "webhook" || run.Origin.TriggerID != "hook" {
			t.Fatalf("listed run origin = %#v, want webhook trigger hook", run.Origin)
		}
		waitForRunTerminalStatus(t, srv.runtime.session("origin-graph", uploaded.Graph.GraphSessionID).runner, runID)
		return
	}
	t.Fatalf("trigger run %q was not returned by run list", runID)
}

func TestTriggerRunStarterUsesSessionRuntimeContext(t *testing.T) {
	sessionModel := &triggerRuntimeTestModel{id: "session"}
	starter := &triggerTestStarter{}
	sessionContext := core.WithModels(
		context.WithValue(context.Background(), triggerContextKey{}, "session"),
		map[string]llms.Model{core.DefaultModelID: sessionModel},
	)
	wrapped := &triggerRunStarter{baseContext: sessionContext, runner: starter}

	if _, _, err := wrapped.Start(context.Background(), state.NewState()); err != nil {
		t.Fatal(err)
	}
	if got := core.ModelByIDFromContext(starter.ctx, core.DefaultModelID); got != sessionModel {
		t.Fatalf("trigger model = %T %p, want session model %p", got, got, sessionModel)
	}
	requireTriggerContextValue(t, starter, "session")
}

func TestTriggerRunStarterPreservesChatReplySink(t *testing.T) {
	starter := &triggerTestStarter{}
	wrapped := &triggerRunStarter{baseContext: context.Background(), runner: starter}
	sink := chatcap.ReplySinkFunc(func(context.Context, chatcap.Reply) error { return nil })
	ctx := chatcap.WithReplySink(context.Background(), sink)

	if _, _, err := wrapped.Start(ctx, state.NewState()); err != nil {
		t.Fatal(err)
	}
	if chatcap.ReplySinkFromContext(starter.ctx) == nil {
		t.Fatal("chat reply sink was dropped while deriving the runtime context")
	}
}

func TestTriggerRunStarterPreservesRuntimeEventObserver(t *testing.T) {
	starter := &triggerTestStarter{}
	wrapped := &triggerRunStarter{baseContext: context.Background(), runner: starter}
	observer := runtime.EventObserverFunc(func(context.Context, runtime.Event) error { return nil })
	ctx := runtime.WithRunnerEventObserver(context.Background(), observer)

	if _, _, err := wrapped.Start(ctx, state.NewState()); err != nil {
		t.Fatal(err)
	}
	if runtime.RunnerEventObserverFromContext(starter.ctx) == nil {
		t.Fatal("runtime event observer was dropped while deriving the runtime context")
	}
}

func TestTriggerRunStarterPreservesRunOrigin(t *testing.T) {
	origin := runtime.RunOrigin{Type: "webhook", TriggerID: "hook"}

	t.Run("sync", func(t *testing.T) {
		starter := &triggerTestStarter{}
		wrapped := &triggerRunStarter{baseContext: context.Background(), runner: starter}
		ctx := runtime.WithRunOrigin(context.Background(), origin)

		if _, _, err := wrapped.Start(ctx, state.NewState()); err != nil {
			t.Fatal(err)
		}
		assertTriggerRunOrigin(t, starter.ctx, origin)
	})

	t.Run("async", func(t *testing.T) {
		starter := &triggerTestStarter{}
		wrapped := &triggerRunStarter{baseContext: context.Background(), runner: starter}
		ctx := runtime.WithRunOrigin(context.Background(), origin)

		if _, done, err := wrapped.StartAsync(ctx, state.NewState()); err != nil {
			t.Fatal(err)
		} else {
			waitForSignal(t, done, "trigger run completion")
		}
		assertTriggerRunOrigin(t, starter.ctx, origin)
	})
}

func assertTriggerRunOrigin(t *testing.T, ctx context.Context, want runtime.RunOrigin) {
	t.Helper()
	got, ok := runtime.RunOriginFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("trigger run origin = %#v, present = %t, want %#v", got, ok, want)
	}
}

func TestTriggerRunStarterAsyncKeepsSessionContextUntilCompletion(t *testing.T) {
	sessionModel := &triggerRuntimeTestModel{id: "session"}
	runDone := make(chan struct{})
	starter := &triggerTestStarter{done: runDone}
	sessionContext := core.WithModels(
		context.WithValue(context.Background(), triggerContextKey{}, "session"),
		map[string]llms.Model{core.DefaultModelID: sessionModel},
	)
	wrapped := &triggerRunStarter{baseContext: sessionContext, runner: starter}

	run, done, err := wrapped.StartAsync(context.Background(), state.NewState())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != runtime.RunStatusRunning {
		t.Fatalf("run status = %q, want running", run.Status)
	}
	if got := core.ModelByIDFromContext(starter.ctx, core.DefaultModelID); got != sessionModel {
		t.Fatalf("trigger model = %T %p, want session model %p", got, got, sessionModel)
	}
	requireTriggerContextValue(t, starter, "session")
	select {
	case <-starter.ctx.Done():
		t.Fatal("async trigger context canceled before completion")
	default:
	}

	close(runDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("async trigger wrapper did not finish")
	}
	select {
	case <-starter.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("async trigger context was not released")
	}
}

func TestResolveTriggerRunnerRejectsMissingGraphID(t *testing.T) {
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{}); err == nil {
		t.Fatal("empty graph id was accepted")
	}
}

func resolvedGraphRunner(t *testing.T, starter trigger.RunStarter) *runtime.GraphRunner {
	t.Helper()
	wrapped, ok := starter.(*triggerRunStarter)
	if !ok {
		t.Fatalf("resolved runner = %T, want *triggerRunStarter", starter)
	}
	runner, ok := wrapped.runner.(*runtime.GraphRunner)
	if !ok {
		t.Fatalf("wrapped runner = %T, want *runtime.GraphRunner", wrapped.runner)
	}
	return runner
}

func triggerGraphUploadBody(graphID, graphVersion, content string) string {
	return graphUploadBodyWithSettings(
		graphID,
		graphVersion,
		content,
		`{"environment":{},"models":[]}`,
	)
}

func graphUploadBodyWithSettings(graphID, graphVersion, content, settings string) string {
	return fmt.Sprintf(`{
		"graph_id": %q,
		"graph_version": %q,
		"definition": {
			"version": "1.0",
			"state_modules": [{"name":"weaveflow.protocols","version":"1"}],
			"name": %q,
			"entry_point": "input",
			"finish_point": "input",
			"nodes": [
				{
					"id": "input",
					"type": "conversation_message",
					"config": {"content": %q},
					"state": {"conversation": {"path": "scopes.input.conversation"}}
				}
			]
		},
		"settings": %s
	}`, graphID, graphVersion, graphID, content, settings)
}
