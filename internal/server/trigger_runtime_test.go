package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	chatcap "github.com/dengzii/weaveflow/capability/chat"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
)

type triggerRuntimeTestModel struct {
	id string
}

func (*triggerRuntimeTestModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return &llms.ContentResponse{}, nil
}

func (*triggerRuntimeTestModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
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
	if runner := resolvedGraphRunner(t, resolved); runner.GraphSessionID != first.Graph.GraphSessionID {
		t.Fatalf("resolved first graph session = %q, want %q", runner.GraphSessionID, first.Graph.GraphSessionID)
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
	if runner.GraphVersion != "v2" || runner.GraphSessionID != second.Graph.GraphSessionID {
		t.Fatalf("resolved graph = version %q session %q, want version v2 session %q", runner.GraphVersion, runner.GraphSessionID, second.Graph.GraphSessionID)
	}

	third := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v3", "third"))
	resolved, err = resolver.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	runner = resolvedGraphRunner(t, resolved)
	if runner.GraphVersion != "v3" || runner.GraphSessionID != third.Graph.GraphSessionID {
		t.Fatalf("resolved graph after upload = version %q session %q, want version v3 session %q", runner.GraphVersion, runner.GraphSessionID, third.Graph.GraphSessionID)
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
		"definition":{"version":"2.0","name":"invalid","nodes":[]},
		"settings":{"environment":{},"models":[],"memory":{"enabled":false}}
	}`)
	if failed.Code != 400 {
		t.Fatalf("failed upload status = %d, body = %s", failed.Code, failed.Body.String())
	}

	resolved, err := srv.resolveTriggerRunner(context.Background(), trigger.Target{GraphID: "graph-a"})
	if err != nil {
		t.Fatal(err)
	}
	runner := resolvedGraphRunner(t, resolved)
	if runner.GraphSessionID != previous.Graph.GraphSessionID {
		t.Fatalf("session after failed upload = %q, want %q", runner.GraphSessionID, previous.Graph.GraphSessionID)
	}
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
		`{"environment":{},"models":[],"memory":{"enabled":false}}`,
	)
}

func graphUploadBodyWithSettings(graphID, graphVersion, content, settings string) string {
	return fmt.Sprintf(`{
		"graph_id": %q,
		"graph_version": %q,
		"definition": {
			"version": "2.0",
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
