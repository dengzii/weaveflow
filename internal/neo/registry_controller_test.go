package neo

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegistryControllerGet(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, router := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/registry", nil)
	ctx.Request = req

	ctrl := NewRegistryController(nil)
	router.GET("/registry", ctrl.Get)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if body == "" {
		t.Fatal("expected non-empty body")
	}
	if !contains(body, "\"node_types\"") || !contains(body, "\"graph_schema\"") {
		t.Fatalf("expected registry payload, got %s", body)
	}
}

func contains(text string, needle string) bool {
	return len(text) >= len(needle) && stringIndex(text, needle) >= 0
}

func stringIndex(text string, needle string) int {
	for i := 0; i+len(needle) <= len(text); i++ {
		if text[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
