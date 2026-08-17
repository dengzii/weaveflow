package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dengzii/weaveflow/internal/trigger"
	"github.com/gin-gonic/gin"
)

func TestGraphCommitRecoveryRestoresTriggersWhenCandidateWasNotPublished(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseDir := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "old"))
	replaced := serveHTTP(engine, http.MethodPut, "/graphs/graph-a/triggers", `{"triggers":[{"id":"old-hook","type":"webhook","enabled":true,"webhook":{}}]}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace old trigger: status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	previous, err := srv.triggers.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.writeGraphCommitJournal(graphLoadResponse{Graph: graphInfo{
		ID: "graph-a", GraphSessionID: "candidate-session",
	}}, "recovery-test", previous); err != nil {
		t.Fatal(err)
	}
	_, err = srv.triggers.ReplaceGraph(context.Background(), "graph-a", []trigger.Trigger{{
		ID: "new-hook", Type: trigger.TypeWebhook, Enabled: true,
		Target:  trigger.Target{GraphID: "graph-a", GraphSessionID: "candidate-session"},
		Webhook: &trigger.WebhookSpec{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := New(context.Background(), Config{BaseDir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := recovered.Close(); err != nil {
			t.Errorf("close recovered server: %v", err)
		}
	})
	items, err := recovered.triggers.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "old-hook" {
		t.Fatalf("recovered triggers = %#v", items)
	}
}

func TestGraphCommitRemapsGlobalTriggerIDBeforePublish(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	putGraphForHashTest(t, engine, triggerGraphUploadBody("owner", "v1", "owner"))
	replaced := serveHTTP(engine, http.MethodPut, "/graphs/owner/triggers", `{"triggers":[{"id":"hook","type":"webhook","enabled":true,"webhook":{}}]}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace owner trigger: status=%d body=%s", replaced.Code, replaced.Body.String())
	}

	body := graphCommitBodyForTest(t, triggerGraphUploadBody("copy", "v1", "copy"), map[string]any{
		"mode":     "create",
		"triggers": []any{map[string]any{"id": "hook", "type": "webhook", "enabled": true, "webhook": map[string]any{}}},
	})
	response := serveHTTP(engine, http.MethodPost, "/graphs/copy/sessions", body)
	if response.Code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data graphLoadResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	committed := envelope.Data
	if committed.TriggerIDMap["hook"] != "hook_1" {
		t.Fatalf("trigger id mapping = %#v, want hook_1", committed.TriggerIDMap)
	}
	if len(committed.Triggers) != 1 || committed.Triggers[0].ID != "hook_1" {
		t.Fatalf("committed triggers = %#v", committed.Triggers)
	}
	if committed.Triggers[0].Target.GraphSessionID != committed.Graph.GraphSessionID {
		t.Fatalf("trigger session = %q, want %q", committed.Triggers[0].Target.GraphSessionID, committed.Graph.GraphSessionID)
	}
	retried := serveHTTP(engine, http.MethodPost, "/graphs/copy/sessions", body)
	if retried.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retried.Code, retried.Body.String())
	}
	var retriedEnvelope struct {
		Data graphLoadResponse `json:"data"`
	}
	if err := json.Unmarshal(retried.Body.Bytes(), &retriedEnvelope); err != nil {
		t.Fatal(err)
	}
	if retriedEnvelope.Data.Graph.GraphSessionID != committed.Graph.GraphSessionID || retriedEnvelope.Data.TriggerIDMap["hook"] != "hook_1" {
		t.Fatalf("retried commit = %#v", retriedEnvelope.Data)
	}
}

func TestGraphCommitRejectsChangedOverwriteHead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	current := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "old"))
	body := graphCommitBodyForTest(t, triggerGraphUploadBody("graph-a", "v2", "new"), map[string]any{
		"mode":                      "overwrite",
		"expected_graph_session_id": "stale-session",
		"triggers":                  []any{},
	})
	response := serveHTTP(engine, http.MethodPost, "/graphs/graph-a/sessions", body)
	if response.Code != http.StatusConflict {
		t.Fatalf("overwrite status=%d body=%s", response.Code, response.Body.String())
	}
	var conflictEnvelope struct {
		Data struct {
			CurrentHead graphSessionSummary `json:"current_head"`
		} `json:"data"`
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &conflictEnvelope); err != nil {
		t.Fatal(err)
	}
	if conflictEnvelope.Error.Code != "graph_head_conflict" || conflictEnvelope.Data.CurrentHead.ID != current.Graph.GraphSessionID {
		t.Fatalf("conflict response = %#v", conflictEnvelope)
	}
	detail := decodeGraphDetailResponse(t, serveHTTP(engine, http.MethodGet, "/graphs/graph-a", ""), http.StatusOK)
	if detail.Graph.GraphSessionID != current.Graph.GraphSessionID {
		t.Fatalf("head after conflict = %q, want %q", detail.Graph.GraphSessionID, current.Graph.GraphSessionID)
	}
}

func TestGraphCommitTriggerValidationFailureKeepsPreviousHead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))
	current := putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-a", "v1", "old"))
	body := graphCommitBodyForTest(t, triggerGraphUploadBody("graph-a", "v2", "new"), map[string]any{
		"mode":                      "overwrite",
		"expected_graph_session_id": current.Graph.GraphSessionID,
		"triggers": []any{map[string]any{
			"id": "bad-schedule", "type": "schedule", "enabled": true,
			"schedule": map[string]any{"cron": "not a cron"},
		}},
	})
	response := serveHTTP(engine, http.MethodPost, "/graphs/graph-a/sessions", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid trigger status=%d body=%s", response.Code, response.Body.String())
	}
	detail := decodeGraphDetailResponse(t, serveHTTP(engine, http.MethodGet, "/graphs/graph-a", ""), http.StatusOK)
	if detail.Graph.GraphSessionID != current.Graph.GraphSessionID {
		t.Fatalf("head after failed commit = %q, want %q", detail.Graph.GraphSessionID, current.Graph.GraphSessionID)
	}
}

func graphCommitBodyForTest(t *testing.T, base string, fields map[string]any) string {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(base), &envelope); err != nil {
		t.Fatal(err)
	}
	delete(envelope, "graph_id")
	for key, value := range fields {
		envelope[key] = value
	}
	envelope["request_id"] = graphRequestIDForTest(t, envelope)
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
