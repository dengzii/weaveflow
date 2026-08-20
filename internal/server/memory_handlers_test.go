package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/internal/memory"

	"github.com/gin-gonic/gin"
)

func TestMemoryRoutesProvideExplicitNamespacedCASAndSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := memory.NewInMemoryStore()
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), MemoryStore: store})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	defer srv.Close()
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	created := serveHTTP(engine, http.MethodPut, "/memory/user/profile", `{"content":"graph runtime"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("PUT memory status = %d, body = %s", created.Code, created.Body.String())
	}
	var createdEnvelope struct {
		Data memory.MemoryRecord `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatalf("decode PUT memory response: %v", err)
	}
	if createdEnvelope.Data.Namespace != memory.Namespace("user") || createdEnvelope.Data.Version != "1" {
		t.Fatalf("created memory = %#v", createdEnvelope.Data)
	}

	conflict := serveHTTP(engine, http.MethodPut, "/memory/user/profile", `{"content":"stale","expected_version":"0"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("stale PUT status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
	search := serveHTTP(engine, http.MethodGet, "/memory/user/search?text=graph", "")
	if search.Code != http.StatusOK || !containsResponseText(search.Body.Bytes(), "profile") {
		t.Fatalf("memory search status = %d, body = %s", search.Code, search.Body.String())
	}
	deleted := serveHTTP(engine, http.MethodDelete, "/memory/user/profile?expected_version=1", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE memory status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	missing := serveHTTP(engine, http.MethodGet, "/memory/user/profile", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET deleted memory status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func containsResponseText(data []byte, text string) bool {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	encoded, err := json.Marshal(decoded)
	return err == nil && strings.Contains(string(encoded), text)
}
