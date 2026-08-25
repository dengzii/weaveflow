package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/chatchannel/wecom"
	"github.com/dengzii/weaveflow/internal/trigger"
)

type replaceGraphFailureStore struct {
	trigger.Store
	err      error
	failures int
}

func (store *replaceGraphFailureStore) ReplaceGraph(ctx context.Context, graphID string, items []trigger.Trigger) error {
	if store.failures > 0 {
		store.failures--
		return store.Store.ReplaceGraph(ctx, graphID, items)
	}
	return store.err
}

type cancelAfterReplaceStore struct {
	trigger.Store
	cancel context.CancelFunc
}

func (store *cancelAfterReplaceStore) ReplaceGraph(ctx context.Context, graphID string, items []trigger.Trigger) error {
	if err := store.Store.ReplaceGraph(ctx, graphID, items); err != nil {
		return err
	}
	if store.cancel != nil {
		store.cancel()
	}
	return nil
}

func TestManagedSecretSweepPreservesPendingWrites(t *testing.T) {
	secretDirectory := t.TempDir()
	secretStore, err := newManagedSecretStore(secretDirectory)
	if err != nil {
		t.Fatal(err)
	}
	ref, release, err := secretStore.Put(context.Background(), "pending-secret")
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsFound <- secretStore.sweep(context.Background(), nil)
		}()
	}
	wait.Wait()
	close(errorsFound)
	for sweepErr := range errorsFound {
		if sweepErr != nil {
			t.Fatal(sweepErr)
		}
	}
	secretPath := filepath.Join(secretDirectory, ref.Ref)
	if _, err := os.Stat(secretPath); err != nil {
		t.Fatalf("pending managed secret was removed: %v", err)
	}

	release(true)
	if err := secretStore.sweep(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secretPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced managed secret still exists: %v", err)
	}
}

func TestGraphModelCredentialLifecycle(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	baseDirectory := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := srv.Engine()

	first := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-model", "v1", "credential-lifecycle", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"openai","model":"gpt-test","credential_value":"first-key"}]
	}`))
	if !first.Settings.Models[0].CredentialConfigured {
		t.Fatal("first credential is not reported as configured")
	}
	credentialPath := modelCredentialTestPath(t, baseDirectory, "default")
	firstData, err := srv.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if firstData != "first-key" {
		t.Fatalf("first managed credential = %q", firstData)
	}
	assertGraphSessionOmitsCredentialValue(t, srv, first, "first-key")

	preserved := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-model", "v1", "credential-lifecycle", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"openai","model":"gpt-test","credential_value":"   "}]
	}`))
	if preserved.Graph.GraphSessionID != first.Graph.GraphSessionID {
		t.Fatalf("blank credential created session %q, want %q", preserved.Graph.GraphSessionID, first.Graph.GraphSessionID)
	}
	if !preserved.Settings.Models[0].CredentialConfigured {
		t.Fatal("blank credential value cleared the configured status")
	}
	preservedData, err := srv.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if preservedData != "first-key" {
		t.Fatalf("blank credential value changed stored value to %q", preservedData)
	}

	rotated := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-model", "v1", "credential-lifecycle", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"openai","model":"gpt-test","credential_value":"second-key"}]
	}`))
	if rotated.Graph.GraphSessionID != first.Graph.GraphSessionID {
		t.Fatalf("credential rotation created session %q, want %q", rotated.Graph.GraphSessionID, first.Graph.GraphSessionID)
	}
	if !rotated.Settings.Models[0].CredentialConfigured {
		t.Fatal("rotated credential is not reported as configured")
	}
	assertGraphSessionOmitsCredentialValue(t, srv, rotated, "second-key")
	rotatedData, err := srv.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if rotatedData != "second-key" {
		t.Fatalf("rotated credential = %q, want second-key", rotatedData)
	}
	modelConfig, ok := core.ModelConfigByIDFromContext(srv.runtime.runtimeContext(), core.DefaultModelID)
	if !ok || modelConfig.APIKey != "second-key" {
		t.Fatalf("runtime model config = %#v, want rotated credential", modelConfig)
	}

	cleared := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-model", "v1", "credential-lifecycle", `{
		"environment":{},
		"models":[{"id":"default","enabled":false,"provider":"openai","model":"gpt-test","credential_clear":true}]
	}`))
	if cleared.Settings.Models[0].CredentialConfigured {
		t.Fatal("cleared credential is still reported as configured")
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatalf("cleared model credential still exists: %v", err)
	}
}

func TestGraphModelCredentialUsesModelIDOnly(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	baseDirectory := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := srv.Engine()

	first := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-rebind", "v1", "initial", `{
		"environment":{},
		"models":[{"id":"jt","enabled":true,"provider":"openai","model":"gpt-test","base_url":"https://old.example.test/v1","credential_value":"jt-key"}]
	}`))
	if !first.Settings.Models[0].CredentialConfigured {
		t.Fatal("initial credential is not reported as configured")
	}
	credentialData, err := os.ReadFile(modelCredentialTestPath(t, baseDirectory, "jt"))
	if err != nil {
		t.Fatal(err)
	}
	var storedCredential map[string]any
	if err := json.Unmarshal(credentialData, &storedCredential); err != nil {
		t.Fatal(err)
	}
	if storedCredential["model_id"] != "jt" || storedCredential["version"] != float64(managedModelCredentialFormatVersion) {
		t.Fatalf("stored credential identity = %#v", storedCredential)
	}
	if _, exists := storedCredential["provider"]; exists {
		t.Fatal("stored credential still contains provider binding")
	}
	if _, exists := storedCredential["base_url"]; exists {
		t.Fatal("stored credential still contains Base URL binding")
	}

	baseURLChanged := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-rebind", "v1", "base-url-changed", `{
		"environment":{},
		"models":[{"id":"jt","enabled":true,"provider":"openai","model":"gpt-test","base_url":"https://new.example.test/v1"}]
	}`))
	if !baseURLChanged.Settings.Models[0].CredentialConfigured {
		t.Fatal("credential is not configured after Base URL change")
	}
	value, err := srv.managedSecrets.ResolveModel(context.Background(), "jt")
	if err != nil {
		t.Fatal(err)
	}
	if value != "jt-key" {
		t.Fatalf("credential after Base URL change = %q, want jt-key", value)
	}
	providerChanged := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-rebind", "v1", "provider-changed", `{
		"environment":{},
		"models":[{"id":"jt","enabled":true,"provider":"deepseek","model":"gpt-test","base_url":"https://new.example.test/v1"}]
	}`))
	if !providerChanged.Settings.Models[0].CredentialConfigured {
		t.Fatal("credential is not configured after provider change")
	}
	value, err = srv.managedSecrets.ResolveModel(context.Background(), "jt")
	if err != nil {
		t.Fatal(err)
	}
	if value != "jt-key" {
		t.Fatalf("credential after provider change = %q, want jt-key", value)
	}
	modelConfig, ok := core.ModelConfigByIDFromContext(srv.runtime.runtimeContext(), "jt")
	if !ok || modelConfig.APIKey != "jt-key" || modelConfig.Provider != "deepseek" || modelConfig.BaseURL != "https://new.example.test/v1" {
		t.Fatalf("runtime model config after provider change = %#v", modelConfig)
	}
}

func TestManagedModelCredentialReplacementRollsBackAndKeepsModelIDsIsolated(t *testing.T) {
	store, err := newManagedSecretStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jtRelease, err := store.SetModel(context.Background(), "jt", "jt-key")
	if err != nil {
		t.Fatal(err)
	}
	jtRelease(true)
	otherRelease, err := store.SetModel(context.Background(), "other", "other-key")
	if err != nil {
		t.Fatal(err)
	}
	otherRelease(true)

	release, err := store.SetModel(context.Background(), "jt", "new-jt-key")
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.ResolveModel(context.Background(), "jt")
	if err != nil {
		t.Fatal(err)
	}
	if value != "new-jt-key" {
		t.Fatalf("replaced credential = %q, want new-jt-key", value)
	}
	otherValue, err := store.ResolveModel(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if otherValue != "other-key" {
		t.Fatalf("other model credential = %q, want other-key", otherValue)
	}

	release(false)
	value, err = store.ResolveModel(context.Background(), "jt")
	if err != nil {
		t.Fatal(err)
	}
	if value != "jt-key" {
		t.Fatalf("rolled back credential = %q, want jt-key", value)
	}
}

func TestManagedModelCredentialRejectsEndpointBoundFormat(t *testing.T) {
	store, err := newManagedSecretStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name, err := modelSecretFileName("jt")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, name)
	if err := os.WriteFile(path, []byte(`{"version":1,"model_id":"jt","provider":"openai","base_url":"https://old.example.test/v1","value":"jt-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveModel(context.Background(), "jt"); err == nil {
		t.Fatal("endpoint-bound credential format was accepted")
	}
}

func TestGraphModelCredentialPersistsAcrossRestart(t *testing.T) {
	baseDirectory := t.TempDir()
	firstServer, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	putGraphForHashTest(t, firstServer.Engine(), graphUploadBodyWithSettings("managed-restart", "v1", "valid", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"openai","credential_value":"persistent-key"}]
	}`))
	if err := firstServer.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	value, err := restarted.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "persistent-key" {
		t.Fatalf("restarted model credential = %q", value)
	}
}

func TestGraphModelCredentialFailureRollsBackChanges(t *testing.T) {
	baseDirectory := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	engine := srv.Engine()

	first := putGraphForHashTest(t, engine, graphUploadBodyWithSettings("managed-rollback", "v1", "valid", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"openai","credential_value":"existing-key"}]
	}`))
	requestBody := graphCommitBodyForTest(t, graphUploadBodyWithSettings("managed-rollback", "v1", "invalid", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"unsupported","credential_value":"failed-key"}]
	}`), map[string]any{
		"mode":                      "overwrite",
		"expected_graph_session_id": first.Graph.GraphSessionID,
		"triggers":                  []any{},
	})
	response := serveHTTP(engine, http.MethodPost, "/graphs/managed-rollback/sessions", requestBody)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("failed rotation status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "failed-key") {
		t.Fatalf("failed rotation response leaked credential: %s", response.Body.String())
	}
	value, err := srv.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "existing-key" {
		t.Fatalf("credential after failed rotation = %q, want existing-key", value)
	}

	clearBody := graphCommitBodyForTest(t, graphUploadBodyWithSettings("managed-rollback", "v1", "invalid-clear", `{
		"environment":{},
		"models":[{"id":"default","enabled":true,"provider":"unsupported","credential_clear":true}]
	}`), map[string]any{
		"mode":                      "overwrite",
		"expected_graph_session_id": first.Graph.GraphSessionID,
		"triggers":                  []any{},
	})
	cleared := serveHTTP(engine, http.MethodPost, "/graphs/managed-rollback/sessions", clearBody)
	if cleared.Code != http.StatusBadRequest {
		t.Fatalf("failed clear status = %d, body = %s", cleared.Code, cleared.Body.String())
	}
	value, err = srv.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "existing-key" {
		t.Fatalf("credential after failed clear = %q, want existing-key", value)
	}

	duplicateBody := graphCommitBodyForTest(t, graphUploadBodyWithSettings("managed-rollback", "v1", "duplicate", `{
		"environment":{},
		"models":[
			{"id":"default","enabled":true,"provider":"openai","credential_value":"first-duplicate-key"},
			{"id":"default","enabled":true,"provider":"openai","credential_value":"second-duplicate-key"}
		]
	}`), map[string]any{
		"mode":                      "overwrite",
		"expected_graph_session_id": first.Graph.GraphSessionID,
		"triggers":                  []any{},
	})
	duplicate := serveHTTP(engine, http.MethodPost, "/graphs/managed-rollback/sessions", duplicateBody)
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("duplicate model status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}
	value, err = srv.managedSecrets.ResolveModel(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "existing-key" {
		t.Fatalf("credential after duplicate model request = %q, want existing-key", value)
	}
}

func modelCredentialTestPath(t *testing.T, baseDirectory string, modelID string) string {
	t.Helper()
	name, err := modelSecretFileName(modelID)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(baseDirectory, "managed-secrets", name)
}

func assertGraphSessionOmitsCredentialValue(t *testing.T, srv *Server, response graphLoadResponse, credential string) {
	t.Helper()
	data, err := os.ReadFile(graphRuntimeSettingsPath(srv.uploadedGraphBaseDir(response.Graph.ID, response.Graph.GraphSessionID)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), credential) {
		t.Fatalf("Graph Session settings contain credential value: %s", data)
	}
	if strings.Contains(string(data), `"credential"`) || strings.Contains(string(data), "credential_configured") {
		t.Fatalf("Graph Session settings contain model credential metadata: %s", data)
	}
	responseData, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(responseData), credential) {
		t.Fatalf("Graph response contains credential value: %s", responseData)
	}
}

func TestServerStartSweepsOnlyOrphanedManagedSecrets(t *testing.T) {
	baseDirectory := t.TempDir()
	secretStore, err := newManagedSecretStore(filepath.Join(baseDirectory, "managed-secrets"))
	if err != nil {
		t.Fatal(err)
	}
	referenced, releaseReferenced, err := secretStore.Put(context.Background(), "referenced-secret")
	if err != nil {
		t.Fatal(err)
	}
	releaseReferenced(true)
	orphaned, releaseOrphaned, err := secretStore.Put(context.Background(), "orphaned-secret")
	if err != nil {
		t.Fatal(err)
	}
	releaseOrphaned(true)
	modelRelease, err := secretStore.SetModel(context.Background(), "default", "model-secret")
	if err != nil {
		t.Fatal(err)
	}
	modelRelease(true)

	channels := managedSecretTestChannels(t)
	triggerStore, err := trigger.NewFileStore(filepath.Join(baseDirectory, "triggers"))
	if err != nil {
		t.Fatal(err)
	}
	service := managedSecretTestService(t, triggerStore, channels)
	if _, err := service.Create(context.Background(), trigger.Trigger{
		ID:      "persisted-chat",
		Type:    trigger.TypeChat,
		Enabled: false,
		Target:  trigger.Target{GraphID: "graph"},
		Chat: &trigger.ChatSpec{
			Channel: "wecom",
			ChannelConfig: map[string]any{
				"bot_id": "bot",
				"secret": referenced,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory, TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if _, err := os.Stat(filepath.Join(baseDirectory, "managed-secrets", referenced.Ref)); err != nil {
		t.Fatalf("referenced managed secret was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDirectory, "managed-secrets", orphaned.Ref)); !os.IsNotExist(err) {
		t.Fatalf("orphaned managed secret still exists: %v", err)
	}
	if _, err := os.Stat(modelCredentialTestPath(t, baseDirectory, "default")); err != nil {
		t.Fatalf("stable model credential was removed by trigger cleanup: %v", err)
	}
}

func TestReplaceTriggersSweepsUnreferencedManagedSecrets(t *testing.T) {
	baseDirectory := t.TempDir()
	channels := managedSecretTestChannels(t)
	triggerStore, err := trigger.NewFileStore(filepath.Join(baseDirectory, "triggers"))
	if err != nil {
		t.Fatal(err)
	}
	lifecycleStore := &cancelAfterReplaceStore{Store: triggerStore}
	service := managedSecretTestService(t, lifecycleStore, channels)
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory, TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := srv.Engine()
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph-one", "v1", "chat one"))

	created := serveHTTP(engine, http.MethodPut, "/graphs/graph-one/triggers", `{"triggers":[{
		"id":"chat-one","type":"chat","enabled":false,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"bot-one","secret":"first-secret"}}
	}]}`)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	first := managedSecretRefForTrigger(t, service, "chat-one")
	firstPath := filepath.Join(baseDirectory, "managed-secrets", first.Ref)

	if _, err := service.Create(context.Background(), trigger.Trigger{
		ID:      "shared-chat",
		Type:    trigger.TypeChat,
		Enabled: false,
		Target:  trigger.Target{GraphID: "graph-two"},
		Chat: &trigger.ChatSpec{
			Channel: "wecom",
			ChannelConfig: map[string]any{
				"bot_id": "bot-two",
				"secret": first,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rotated := serveHTTP(engine, http.MethodPut, "/graphs/graph-one/triggers", `{"triggers":[{
		"id":"chat-one","type":"chat","enabled":false,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"bot-one","secret":"rotated-secret"}}
	}]}`)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rotated.Code, rotated.Body.String())
	}
	second := managedSecretRefForTrigger(t, service, "chat-one")
	if second.Ref == first.Ref {
		t.Fatal("rotated chat secret reused the previous managed ref")
	}
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("shared managed secret was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDirectory, "managed-secrets", second.Ref)); err != nil {
		t.Fatalf("rotated managed secret is missing: %v", err)
	}

	if err := service.Delete(context.Background(), "shared-chat"); err != nil {
		t.Fatal(err)
	}
	cleaned := serveHTTP(engine, http.MethodPut, "/graphs/graph-one/triggers", `{"triggers":[{
		"id":"chat-one","type":"chat","enabled":false,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"updated-bot"}}
	}]}`)
	if cleaned.Code != http.StatusOK {
		t.Fatalf("cleanup status = %d, body = %s", cleaned.Code, cleaned.Body.String())
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced shared managed secret still exists: %v", err)
	}
	if got := managedSecretRefForTrigger(t, service, "chat-one"); got.Ref != second.Ref {
		t.Fatalf("preserved managed ref = %#v, want %#v", got, second)
	}

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	lifecycleStore.cancel = cancelRequest
	removed := serveHTTPWithContext(requestContext, engine, http.MethodPut, "/graphs/graph-one/triggers", `{"triggers":[]}`)
	if removed.Code != http.StatusOK {
		t.Fatalf("remove status = %d, body = %s", removed.Code, removed.Body.String())
	}
	if !errors.Is(requestContext.Err(), context.Canceled) {
		t.Fatalf("replacement request context error = %v, want canceled", requestContext.Err())
	}
	if _, err := os.Stat(filepath.Join(baseDirectory, "managed-secrets", second.Ref)); !os.IsNotExist(err) {
		t.Fatalf("managed secret survived a committed replacement with a canceled request: %v", err)
	}
}

func TestReplaceTriggersFailureRollsBackManagedSecretWrites(t *testing.T) {
	baseDirectory := t.TempDir()
	channels := managedSecretTestChannels(t)
	fileStore, err := trigger.NewFileStore(filepath.Join(baseDirectory, "triggers"))
	if err != nil {
		t.Fatal(err)
	}
	failingStore := &replaceGraphFailureStore{Store: fileStore, err: errors.New("replace failed"), failures: 1}
	service := managedSecretTestService(t, failingStore, channels)
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory, TriggerService: service})
	if err != nil {
		t.Fatal(err)
	}
	engine := srv.Engine()
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph", "v1", "chat"))

	response := serveHTTP(engine, http.MethodPut, "/graphs/graph/triggers", `{"triggers":[{
		"id":"chat","type":"chat","enabled":false,
		"chat":{"channel":"wecom","channel_config":{"bot_id":"bot","secret":"new-secret"}}
	}]}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("replace status = %d, body = %s", response.Code, response.Body.String())
	}
	if paths := managedSecretTestPaths(t, baseDirectory); len(paths) != 0 {
		t.Fatalf("failed replacement left managed secrets: %v", paths)
	}
}

func TestReplaceTriggersDoesNotReportCommittedReplacementAsCleanupFailure(t *testing.T) {
	baseDirectory := t.TempDir()
	srv, err := New(context.Background(), Config{BaseDir: baseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	engine := srv.Engine()
	putGraphForHashTest(t, engine, triggerGraphUploadBody("graph", "v1", "webhook"))

	managedDirectory := filepath.Join(baseDirectory, "managed-secrets")
	if err := os.Remove(managedDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedDirectory, []byte("blocks cleanup"), 0o600); err != nil {
		t.Fatal(err)
	}

	response := serveHTTP(engine, http.MethodPut, "/graphs/graph/triggers", `{"triggers":[{
		"id":"webhook","type":"webhook","enabled":false,
		"credential":{"source":"env","ref":"TRIGGER_TOKEN"},"webhook":{}
	}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := srv.TriggerService().Get(context.Background(), "webhook"); err != nil {
		t.Fatalf("committed trigger is missing: %v", err)
	}
}

func managedSecretTestChannels(t *testing.T) *chatchannel.Registry {
	t.Helper()
	channels := chatchannel.NewDefaultRegistry()
	if err := wecom.Register(channels); err != nil {
		t.Fatal(err)
	}
	return channels
}

func managedSecretTestService(t *testing.T, triggerStore trigger.Store, channels *chatchannel.Registry) *trigger.Service {
	t.Helper()
	service, err := trigger.NewService(
		triggerStore,
		trigger.RunnerResolverFunc(func(context.Context, trigger.Target) (trigger.RunStarter, error) {
			return nil, errors.New("runner should not be resolved")
		}),
		trigger.WithChatChannels(channels),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func managedSecretRefForTrigger(t *testing.T, service *trigger.Service, triggerID string) dsl.SecretRef {
	t.Helper()
	item, err := service.Get(context.Background(), triggerID)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := chatchannel.ParseSecretRef(item.Chat.ChannelConfig["secret"])
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func managedSecretTestPaths(t *testing.T, baseDirectory string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(baseDirectory, "managed-secrets"))
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && isManagedSecretID(entry.Name()) {
			paths = append(paths, entry.Name())
		}
	}
	return paths
}
