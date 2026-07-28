package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/runtime"
	"github.com/dengzii/weaveflow/state"
	"github.com/dengzii/weaveflow/internal/trigger"
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

func requireTriggerContextValue(t *testing.T, starter *triggerTestStarter, want string) {
	t.Helper()
	if starter.ctx == nil {
		t.Fatal("trigger run context is nil")
	}
	if got, _ := starter.ctx.Value(triggerContextKey{}).(string); got != want {
		t.Fatalf("trigger run context value = %q, want %q", got, want)
	}
}

func TestTriggerRoutesCreateAndInvokeWebhook(t *testing.T) {
	store, err := trigger.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan struct{})
	starter := &triggerTestStarter{done: runDone}
	var resolvedTarget trigger.Target
	service, err := trigger.NewService(store, trigger.RunnerResolverFunc(func(_ context.Context, target trigger.Target) (trigger.RunStarter, error) {
		resolvedTarget = target
		return starter, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	baseCtx := context.WithValue(context.Background(), triggerContextKey{}, "injected")
	srv, err := New(baseCtx, Config{BaseDir: t.TempDir(), TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(baseCtx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(runDone)
		_ = srv.Close()
	}()
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	create := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/triggers", strings.NewReader(`{"id":"hook","type":"webhook","target":{"graph_id":"graph-1"},"webhook":{"secret":"secret"}}`))
	engine.ServeHTTP(create, req)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "secret") {
		t.Fatalf("secret leaked in create response: %s", create.Body.String())
	}

	invalid := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers", strings.NewReader(`{"id":"invalid","type":"webhook","target":{"graph_id":"graph-1"}}`))
	engine.ServeHTTP(invalid, req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid trigger status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	invalidID := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers", strings.NewReader(`{"id":"bad:id","type":"webhook","target":{"graph_id":"graph-1"},"webhook":{}}`))
	engine.ServeHTTP(invalidID, req)
	if invalidID.Code != http.StatusBadRequest {
		t.Fatalf("invalid trigger id status = %d, body = %s", invalidID.Code, invalidID.Body.String())
	}
	oversized := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers", strings.NewReader(strings.Repeat(" ", int(maxTriggerPayloadBodyBytes)+1)))
	engine.ServeHTTP(oversized, req)
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized trigger status = %d, body = %s", oversized.Code, oversized.Body.String())
	}

	legacyTarget := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers", strings.NewReader(`{"id":"legacy","type":"webhook","target":{"graph_id":"graph-1","graph_session_id":"session-1"},"webhook":{}}`))
	engine.ServeHTTP(legacyTarget, req)
	if legacyTarget.Code != http.StatusBadRequest || !strings.Contains(legacyTarget.Body.String(), "unknown field") {
		t.Fatalf("legacy target status = %d, body = %s", legacyTarget.Code, legacyTarget.Body.String())
	}

	body := []byte(`{"ok":true}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	webhook := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers/hook", strings.NewReader(string(body)))
	req.Header.Set(trigger.DefaultSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	engine.ServeHTTP(webhook, req)
	if webhook.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, body = %s", webhook.Code, webhook.Body.String())
	}
	var invoked struct {
		Data struct {
			Run runtime.RunRecord `json:"run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(webhook.Body.Bytes(), &invoked); err != nil {
		t.Fatal(err)
	}
	if invoked.Data.Run.Status != runtime.RunStatusRunning {
		t.Fatalf("trigger response status = %q, want running", invoked.Data.Run.Status)
	}
	if starter.initial == nil {
		t.Fatal("webhook did not start a run")
	}
	requireTriggerContextValue(t, starter, "injected")
	select {
	case <-starter.ctx.Done():
		t.Fatal("trigger run context was canceled with the HTTP request")
	default:
	}
	if resolvedTarget.GraphID != "graph-1" {
		t.Fatalf("initial resolved target = %#v", resolvedTarget)
	}

	rawQuery := "input=hello&tag=a&tag=b"
	mac = hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(rawQuery))
	webhook = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/triggers/hook/webhook?"+rawQuery, nil)
	req.Header.Set(trigger.DefaultSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	engine.ServeHTTP(webhook, req)
	if webhook.Code != http.StatusOK {
		t.Fatalf("GET webhook status = %d, body = %s", webhook.Code, webhook.Body.String())
	}
	requireTriggerContextValue(t, starter, "injected")
	input, ok := state.ReadPath(starter.initial, "shared.request.input")
	if !ok {
		t.Fatal("GET webhook input is missing")
	}
	values, ok := input.(map[string]any)
	if !ok || values["input"] != "hello" {
		t.Fatalf("GET webhook input = %#v", input)
	}
	tags, ok := values["tag"].([]string)
	if !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("GET webhook tags = %#v", values["tag"])
	}
	records, err := service.ListRecords(context.Background(), "hook", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("trigger record count after GET = %d, want 2", len(records))
	}

	legacyInvoke := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers/hook/webhook", strings.NewReader(string(body)))
	engine.ServeHTTP(legacyInvoke, req)
	if legacyInvoke.Code != http.StatusNotFound {
		t.Fatalf("legacy trigger path status = %d, want 404", legacyInvoke.Code)
	}
	calls := starter.calls
	getTrigger := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/triggers/hook", nil)
	engine.ServeHTTP(getTrigger, req)
	if getTrigger.Code != http.StatusOK || starter.calls != calls {
		t.Fatalf("GET trigger status = %d, calls = %d, want management response without invocation", getTrigger.Code, starter.calls)
	}

	invalidUpdate := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/triggers/hook", strings.NewReader(`{"type":"webhook","webhook":{"state_mappings":[{"parameter":"user.id","state_path":"runtime.user.id"}]}}`))
	engine.ServeHTTP(invalidUpdate, req)
	if invalidUpdate.Code != http.StatusBadRequest {
		t.Fatalf("invalid update status = %d, body = %s", invalidUpdate.Code, invalidUpdate.Body.String())
	}

	update := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/triggers/hook", strings.NewReader(`{"name":"Updated hook","type":"webhook","target":{"graph_id":"graph-2"},"webhook":{"state_mappings":[{"parameter":"user.id","state_path":"shared.user.id"}]}}`))
	engine.ServeHTTP(update, req)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}
	if strings.Contains(update.Body.String(), "secret") {
		t.Fatalf("secret leaked in update response: %s", update.Body.String())
	}
	var updated struct {
		Data trigger.Trigger `json:"data"`
	}
	if err := json.Unmarshal(update.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Data.Target.GraphID != "graph-2" {
		t.Fatalf("updated target = %#v", updated.Data.Target)
	}
	if updated.Data.Webhook == nil || len(updated.Data.Webhook.StateMappings) != 1 {
		t.Fatalf("updated webhook = %#v", updated.Data.Webhook)
	}
	stored, err := service.Get(context.Background(), "hook")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Webhook == nil || stored.Webhook.Secret != "secret" {
		t.Fatalf("stored webhook secret was not preserved: %#v", stored.Webhook)
	}

	body = []byte(`{"user":{"id":"post-user"}}`)
	mac = hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	webhook = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers/hook", strings.NewReader(string(body)))
	req.Header.Set(trigger.DefaultSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	engine.ServeHTTP(webhook, req)
	if webhook.Code != http.StatusOK {
		t.Fatalf("updated webhook status = %d, body = %s", webhook.Code, webhook.Body.String())
	}
	requireTriggerContextValue(t, starter, "injected")
	if resolvedTarget.GraphID != "graph-2" {
		t.Fatalf("updated resolved target = %#v", resolvedTarget)
	}
	mapped, ok := state.ReadPath(starter.initial, "shared.user.id")
	if !ok || mapped != "post-user" {
		t.Fatalf("POST mapped state = %#v", mapped)
	}

	rawQuery = "user.id=query-user"
	mac = hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(rawQuery))
	webhook = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/triggers/hook/webhook?"+rawQuery, nil)
	req.Header.Set(trigger.DefaultSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	engine.ServeHTTP(webhook, req)
	if webhook.Code != http.StatusOK {
		t.Fatalf("updated GET webhook status = %d, body = %s", webhook.Code, webhook.Body.String())
	}
	requireTriggerContextValue(t, starter, "injected")
	mapped, ok = state.ReadPath(starter.initial, "shared.user.id")
	if !ok || mapped != "query-user" {
		t.Fatalf("GET mapped state = %#v", mapped)
	}

	schedule, err := service.Create(context.Background(), trigger.Trigger{
		ID:      "timer",
		Type:    trigger.TypeSchedule,
		Enabled: true,
		Target:  trigger.Target{GraphID: "graph-2"},
		Schedule: &trigger.ScheduleSpec{
			Cron: "0 12 * * *",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.ID != "timer" {
		t.Fatalf("schedule id = %q", schedule.ID)
	}
	scheduledRun := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/triggers/timer", nil)
	engine.ServeHTTP(scheduledRun, req)
	if scheduledRun.Code != http.StatusOK {
		t.Fatalf("schedule run status = %d, body = %s", scheduledRun.Code, scheduledRun.Body.String())
	}
	requireTriggerContextValue(t, starter, "injected")
}

func TestTriggerRecordRouteListsInvocations(t *testing.T) {
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
	if _, err := service.Create(context.Background(), trigger.Trigger{
		ID:      "recorded-hook",
		Type:    trigger.TypeWebhook,
		Enabled: true,
		Target:  trigger.Target{GraphID: "graph-1"},
		Webhook: &trigger.WebhookSpec{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvokeWebhook(context.Background(), "recorded-hook", []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}

	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/trigger-records?trigger_id=recorded-hook&limit=5", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("record list status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Data []trigger.Record `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("record count = %d, want 1", len(result.Data))
	}
	record := result.Data[0]
	if record.TriggerID != "recorded-hook" || record.Status != runtime.RunStatusRunning || record.Run == nil || record.Run.RunID != "run-1" {
		t.Fatalf("record = %#v", record)
	}

	invalid := httptest.NewRecorder()
	engine.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/trigger-records?limit=zero", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
}
