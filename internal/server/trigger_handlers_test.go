package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/chatchannel/wecom"
	"github.com/dengzii/weaveflow/internal/trigger"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/gin-gonic/gin"
)

type triggerTestStarter struct {
	initial *state.State
	ctx     context.Context
	done    <-chan struct{}
	calls   int
}

func (s *triggerTestStarter) Start(ctx context.Context, initial *state.State) (runtime.RunRecord, *state.State, error) {
	s.ctx = ctx
	s.initial = initial.Clone()
	return runtime.RunRecord{RunID: "run-1", Status: runtime.RunStatusCompleted}, initial, nil
}

func (s *triggerTestStarter) StartAsync(ctx context.Context, initial *state.State) (runtime.RunRecord, <-chan struct{}, error) {
	s.ctx = ctx
	s.initial = initial.Clone()
	s.calls++
	done := s.done
	if done == nil {
		closed := make(chan struct{})
		close(closed)
		done = closed
	}
	return runtime.RunRecord{RunID: "run-1", Status: runtime.RunStatusRunning}, done, nil
}

type triggerContextKey struct{}

type triggerCredentialResolver struct {
	value string
	calls int
}

func (resolver *triggerCredentialResolver) Resolve(_ context.Context, ref dsl.SecretRef) (string, error) {
	resolver.calls++
	if ref.Source != "env" || ref.Ref != "TRIGGER_TOKEN" {
		return "", fmt.Errorf("unexpected secret ref %#v", ref)
	}
	return resolver.value, nil
}

type authorizedSnapshotStore struct {
	trigger.Store
	authorized  trigger.Trigger
	replacement trigger.Trigger
	getCalls    int
}

func (store *authorizedSnapshotStore) Get(_ context.Context, _ string) (trigger.Trigger, error) {
	store.getCalls++
	if store.getCalls == 1 {
		return store.authorized, nil
	}
	return store.replacement, nil
}

func TestChatChannelConfigIsValidatedRedactedAndPreserved(t *testing.T) {
	channels := chatchannel.NewDefaultRegistry()
	if err := wecom.Register(channels); err != nil {
		t.Fatal(err)
	}
	triggerDirectory := t.TempDir()
	store, err := trigger.NewFileStore(triggerDirectory)
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
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph", "v1", "chat"))

	create := serveHTTP(engine, http.MethodPut, "/graphs/graph/triggers", `{"triggers":[{
		"id":"wecom-chat","type":"chat","enabled":true,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"bot","secret":"stored-secret"},"stream_updates":true}
	}]}`)
	if create.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "stored-secret") {
		t.Fatalf("create response leaked channel secret: %s", create.Body.String())
	}
	persisted, err := service.Get(context.Background(), "wecom-chat")
	if err != nil {
		t.Fatal(err)
	}
	secretRef, err := chatchannel.ParseSecretRef(persisted.Chat.ChannelConfig["secret"])
	if err != nil || secretRef.Source != managedSecretSource {
		t.Fatalf("persisted config = %#v, err = %v", persisted.Chat.ChannelConfig, err)
	}
	storedData, err := os.ReadFile(filepath.Join(triggerDirectory, "wecom-chat.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedData), "stored-secret") || !strings.Contains(string(storedData), `"source": "managed"`) {
		t.Fatalf("stored trigger contains an invalid channel credential: %s", storedData)
	}
	resolvedConfig, err := service.ResolveChatChannelConfig(context.Background(), persisted)
	if err != nil || resolvedConfig["secret"] != "stored-secret" {
		t.Fatalf("resolved channel config = %#v, err = %v", resolvedConfig, err)
	}

	update := serveHTTP(engine, http.MethodPut, "/graphs/graph/triggers", `{"triggers":[{
		"id":"wecom-chat","type":"chat","enabled":true,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"updated-bot"},"stream_updates":true}
	}]}`)
	if update.Code != http.StatusOK || strings.Contains(update.Body.String(), "stored-secret") {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}
	persisted, err = service.Get(context.Background(), "wecom-chat")
	if err != nil {
		t.Fatal(err)
	}
	preservedRef, err := chatchannel.ParseSecretRef(persisted.Chat.ChannelConfig["secret"])
	if err != nil || persisted.Chat.ChannelConfig["bot_id"] != "updated-bot" || preservedRef.Source != secretRef.Source || preservedRef.Ref != secretRef.Ref {
		t.Fatalf("updated config = %#v, err = %v", persisted.Chat.ChannelConfig, err)
	}

	forged := serveHTTP(engine, http.MethodPut, "/graphs/graph/triggers", `{"triggers":[{
		"id":"wecom-chat","type":"chat","enabled":false,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"updated-bot","secret":{"source":"managed","ref":"`+secretRef.Ref+`"}},"stream_updates":true}
	}]}`)
	if forged.Code != http.StatusBadRequest || !strings.Contains(forged.Body.String(), "unsupported secret source") {
		t.Fatalf("forged managed ref status = %d, body = %s", forged.Code, forged.Body.String())
	}
}

func requireTriggerContextValue(t *testing.T, starter *triggerTestStarter, want string) {
	t.Helper()
	if starter.ctx == nil {
		t.Fatal("trigger run context is nil")
	}
	if got, _ := starter.ctx.Value(triggerContextKey{}).(string); got != want {
		t.Fatalf("trigger run context value = %q, want %q", got, want)
	}
}

func TestTriggerRoutesReplaceListAndInvokeWebhook(t *testing.T) {
	triggerDirectory := t.TempDir()
	store, err := trigger.NewFileStore(triggerDirectory)
	if err != nil {
		t.Fatal(err)
	}
	starter := &triggerTestStarter{}
	var resolvedTarget trigger.Target
	service, err := trigger.NewService(store, trigger.RunnerResolverFunc(func(_ context.Context, target trigger.Target) (trigger.RunStarter, error) {
		resolvedTarget = target
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	baseCtx := context.WithValue(context.Background(), triggerContextKey{}, "injected")
	credentialResolver := &triggerCredentialResolver{value: "first-secret"}
	srv, err := New(baseCtx, Config{BaseDir: t.TempDir(), TriggerService: service, SecretResolver: credentialResolver})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-1", "v1", "webhook"))
	if err := srv.Start(baseCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	replaced := serveHTTP(engine, http.MethodPut, "/graphs/graph-1/triggers", `{"triggers":[{
		"id":"hook","name":"Incoming hook","type":"webhook","enabled":true,
		"credential":{"source":"env","ref":"TRIGGER_TOKEN"},
		"initial_state":{"shared":{"tenant":{"id":"tenant-1"}}},
		"webhook":{"state_bindings":{"input":"scopes.webhook.input"},"state_mappings":[{"parameter":"user.id","state_path":"shared.user.id"}]}
	}]}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body = %s", replaced.Code, replaced.Body.String())
	}
	if strings.Contains(replaced.Body.String(), "first-secret") || strings.Contains(replaced.Body.String(), "api_key") {
		t.Fatalf("replace response leaked credential value: %s", replaced.Body.String())
	}
	storedData, err := os.ReadFile(filepath.Join(triggerDirectory, "hook.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedData), "first-secret") || !strings.Contains(string(storedData), `"ref": "TRIGGER_TOKEN"`) {
		t.Fatalf("stored trigger credential = %s", storedData)
	}

	listed := serveHTTP(engine, http.MethodGet, "/graphs/graph-1/triggers", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}
	var listEnvelope struct {
		Data []trigger.Trigger `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(listEnvelope.Data) != 1 || listEnvelope.Data[0].ID != "hook" || listEnvelope.Data[0].Target.GraphID != "graph-1" {
		t.Fatalf("listed triggers = %#v", listEnvelope.Data)
	}
	if bindings := listEnvelope.Data[0].Webhook.StateBindings; bindings == nil || bindings.Input != "scopes.webhook.input" {
		t.Fatalf("listed webhook state bindings = %#v", bindings)
	}
	otherGraph := serveHTTP(engine, http.MethodGet, "/graphs/graph-2/triggers", "")
	if otherGraph.Code != http.StatusOK || !strings.Contains(otherGraph.Body.String(), `"data":[]`) {
		t.Fatalf("other graph list status = %d, body = %s", otherGraph.Code, otherGraph.Body.String())
	}

	invalid := serveHTTP(engine, http.MethodPut, "/graphs/graph-1/triggers", `{"triggers":[{"id":"bad:id","type":"webhook","webhook":{}}]}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid replacement status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	unsupportedCredential := serveHTTP(engine, http.MethodPut, "/graphs/graph-1/triggers", `{"triggers":[{
		"id":"unsupported-credential","type":"webhook","enabled":true,
		"credential":{"source":"vault","ref":"secret"},"webhook":{}
	}]}`)
	if unsupportedCredential.Code != http.StatusBadRequest || !strings.Contains(unsupportedCredential.Body.String(), "unsupported secret source") {
		t.Fatalf("unsupported credential status = %d, body = %s", unsupportedCredential.Code, unsupportedCredential.Body.String())
	}
	listed = serveHTTP(engine, http.MethodGet, "/graphs/graph-1/triggers", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"id":"hook"`) {
		t.Fatalf("failed replacement changed stored triggers: %s", listed.Body.String())
	}

	body := `{"user":{"id":"post-user"}}`
	unauthorized := serveHTTP(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/invocations?api_key=first-secret", body)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("query API key status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}
	unauthorizedWebhook := serveHTTP(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/webhook", body)
	if unauthorizedWebhook.Code != http.StatusUnauthorized {
		t.Fatalf("missing webhook bearer status = %d, body = %s", unauthorizedWebhook.Code, unauthorizedWebhook.Body.String())
	}
	wrong := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/invocations", body, "Authorization", "Bearer wrong")
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("wrong bearer status = %d, body = %s", wrong.Code, wrong.Body.String())
	}
	wrongWebhook := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/webhook", body, "Authorization", "Bearer wrong")
	if wrongWebhook.Code != http.StatusForbidden {
		t.Fatalf("wrong webhook bearer status = %d, body = %s", wrongWebhook.Code, wrongWebhook.Body.String())
	}
	if starter.calls != 0 {
		t.Fatalf("unauthorized requests started %d runs", starter.calls)
	}
	records, err := service.ListRecords(context.Background(), "hook", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("unauthorized requests created records: %#v", records)
	}
	invoked := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/invocations", body, "Authorization", "Bearer first-secret")
	if invoked.Code != http.StatusAccepted {
		t.Fatalf("invoke status = %d, body = %s", invoked.Code, invoked.Body.String())
	}
	if resolvedTarget.GraphID != "graph-1" {
		t.Fatalf("resolved target = %#v", resolvedTarget)
	}
	requireTriggerContextValue(t, starter, "injected")
	tenantID, ok := state.ReadPath(starter.initial, "shared.tenant.id")
	if !ok || tenantID != "tenant-1" {
		t.Fatalf("trigger initial tenant = %#v", tenantID)
	}
	mapped, ok := state.ReadPath(starter.initial, "shared.user.id")
	if !ok || mapped != "post-user" {
		t.Fatalf("mapped webhook state = %#v", mapped)
	}
	input, ok := state.ReadPath(starter.initial, "scopes.webhook.input")
	if !ok || input.(map[string]any)["user"].(map[string]any)["id"] != "post-user" {
		t.Fatalf("bound webhook input = %#v", input)
	}

	credentialResolver.value = "rotated-secret"
	stale := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/invocations", body, "Authorization", "Bearer first-secret")
	if stale.Code != http.StatusForbidden {
		t.Fatalf("stale bearer status = %d, body = %s", stale.Code, stale.Body.String())
	}
	staleWebhook := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/webhook", body, "Authorization", "Bearer first-secret")
	if staleWebhook.Code != http.StatusForbidden {
		t.Fatalf("stale webhook bearer status = %d, body = %s", staleWebhook.Code, staleWebhook.Body.String())
	}
	if starter.calls != 1 {
		t.Fatalf("stale credentials started %d runs, want 1 authorized run", starter.calls)
	}
	records, err = service.ListRecords(context.Background(), "hook", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("stale credentials changed trigger records: %#v", records)
	}
	rotated := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/invocations", body, "Authorization", "Bearer rotated-secret")
	if rotated.Code != http.StatusAccepted {
		t.Fatalf("rotated bearer status = %d, body = %s", rotated.Code, rotated.Body.String())
	}
	webhook := requestWithHeader(engine, http.MethodPost, "/graphs/graph-1/triggers/hook/webhook", body, "Authorization", "Bearer rotated-secret")
	if webhook.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d, body = %s", webhook.Code, webhook.Body.String())
	}
	if response := serveHTTP(engine, http.MethodGet, "/graphs/graph-1/triggers/hook/webhook", ""); response.Code != http.StatusNotFound {
		t.Fatalf("GET webhook status = %d, want 404", response.Code)
	}
	if response := requestWithHeader(engine, http.MethodPost, "/graphs/graph-2/triggers/hook/webhook", body, "Authorization", "Bearer rotated-secret"); response.Code != http.StatusNotFound {
		t.Fatalf("cross-graph webhook status = %d, want 404; body = %s", response.Code, response.Body.String())
	}
	if credentialResolver.calls < 5 {
		t.Fatalf("credential resolver calls = %d, want per-request resolution", credentialResolver.calls)
	}
}

func TestAuthenticatedWebhookExecutesAuthorizedTriggerSnapshot(t *testing.T) {
	fileStore, err := trigger.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &authorizedSnapshotStore{Store: fileStore}
	starter := &triggerTestStarter{}
	service, err := trigger.NewService(store, trigger.RunnerResolverFunc(func(context.Context, trigger.Target) (trigger.RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := service.Create(context.Background(), trigger.Trigger{
		ID: "snapshot-hook", Type: trigger.TypeWebhook, Enabled: true,
		Target:       trigger.Target{GraphID: "graph"},
		Credential:   &dsl.SecretRef{Source: "env", Ref: "TRIGGER_TOKEN"},
		InitialState: map[string]any{"shared": map[string]any{"snapshot": "authorized"}},
		Webhook:      &trigger.WebhookSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.authorized = authorized
	store.replacement = authorized
	store.replacement.Credential = &dsl.SecretRef{Source: "env", Ref: "ROTATED_TRIGGER_TOKEN"}
	store.replacement.InitialState = map[string]any{"shared": map[string]any{"snapshot": "replacement"}}

	credentialResolver := &triggerCredentialResolver{value: "authorized-secret"}
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), TriggerService: service, SecretResolver: credentialResolver})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	response := requestWithHeader(engine, http.MethodPost, "/graphs/graph/triggers/snapshot-hook/webhook", `{}`, "Authorization", "Bearer authorized-secret")
	if response.Code != http.StatusAccepted {
		t.Fatalf("snapshot invocation status = %d, body = %s", response.Code, response.Body.String())
	}
	if store.getCalls != 1 {
		t.Fatalf("trigger reads = %d, want one authenticated snapshot", store.getCalls)
	}
	value, ok := state.ReadPath(starter.initial, "shared.snapshot")
	if !ok || value != "authorized" {
		t.Fatalf("executed trigger snapshot = %#v, want authorized", value)
	}
}

func TestPublicTriggerInvocationRejectsUnconfiguredCredentialWithoutCreatingRun(t *testing.T) {
	store, err := trigger.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	starter := &triggerTestStarter{}
	service, err := trigger.NewService(store, trigger.RunnerResolverFunc(func(context.Context, trigger.Target) (trigger.RunStarter, error) {
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), trigger.Trigger{
		ID: "private-hook", Type: trigger.TypeWebhook, Enabled: true,
		Target: trigger.Target{GraphID: "graph"}, Webhook: &trigger.WebhookSpec{},
	}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	response := requestWithHeader(engine, http.MethodPost, "/graphs/graph/triggers/private-hook/invocations", `{}`, "Authorization", "Bearer any-value")
	if response.Code != http.StatusForbidden {
		t.Fatalf("unconfigured credential status = %d, body = %s", response.Code, response.Body.String())
	}
	if starter.calls != 0 {
		t.Fatalf("unconfigured credential started %d runs", starter.calls)
	}
	records, err := service.ListRecords(context.Background(), "private-hook", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("unconfigured credential created records: %#v", records)
	}
}

func TestTriggerReplacementValidatesEachEntryProvider(t *testing.T) {
	reg := wfregistry.NewRegistry()
	if err := reg.RegisterStateModule(builtin.ProtocolsStateModuleDefinition()); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterNodeType(wfregistry.NodeTypeDefinition{
		NodeTypeSchema: dsl.NodeTypeSchema{
			Type: "requires_input",
			StatePorts: []dsl.StatePortDefinition{{
				Name: "input", Required: true, Schema: dsl.JSONSchema{"type": "string"},
				Mode: dsl.StateAccessRead, MergeStrategy: dsl.StateMergeReplace,
			}},
		},
		Build: func(_ *wfregistry.BuildContext, resolved wfregistry.ResolvedNodeSpec) (core.Node, error) {
			return newContractTestNode(resolved.Spec), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	store, err := trigger.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := trigger.NewService(store, trigger.RunnerResolverFunc(func(context.Context, trigger.Target) (trigger.RunStarter, error) {
		return &triggerTestStarter{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), Registry: reg, TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, `{
		"graph_id":"required-graph",
		"graph_version":"v1",
		"definition":{
			"version":"2.0",
			"state_modules":[{"name":"weaveflow.protocols","version":"1"}],
			"name":"required-graph",
			"entry_point":"input",
			"finish_point":"input",
			"nodes":[{"id":"input","type":"requires_input","state":{"input":{"path":"shared.request.input"}}}]
		},
		"settings":{"environment":{},"models":[]}
	}`)

	missing := serveHTTP(engine, http.MethodPut, "/graphs/required-graph/triggers", `{"triggers":[
		{"id":"provided","type":"webhook","webhook":{"state_bindings":{"input":"shared.request.input"}}},
		{"id":"missing","type":"webhook","webhook":{}}
	]}`)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), `trigger \"missing\"`) || !strings.Contains(missing.Body.String(), "shared.request.input") {
		t.Fatalf("missing trigger status = %d, body = %s", missing.Code, missing.Body.String())
	}

	valid := serveHTTP(engine, http.MethodPut, "/graphs/required-graph/triggers", `{"triggers":[
		{"id":"first","type":"webhook","webhook":{"state_bindings":{"input":"shared.request.input"}}},
		{"id":"second","type":"webhook","webhook":{"state_bindings":{"input":"shared.request.input"}}}
	]}`)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid triggers status = %d, body = %s", valid.Code, valid.Body.String())
	}
}

func TestRemovedServerRoutesReturnNotFound(t *testing.T) {
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/graph"},
		{method: http.MethodPut, path: "/graph"},
		{method: http.MethodPost, path: "/runs"},
		{method: http.MethodGet, path: "/runs"},
		{method: http.MethodGet, path: "/runtime/events/stream"},
		{method: http.MethodGet, path: "/triggers"},
		{method: http.MethodPost, path: "/triggers"},
		{method: http.MethodGet, path: "/trigger-invocations"},
	} {
		response := serveHTTP(engine, request.method, request.path, "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", request.method, request.path, response.Code)
		}
	}
}

func requestWithHeader(engine *gin.Engine, method, path, body, name, value string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(name, value)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
