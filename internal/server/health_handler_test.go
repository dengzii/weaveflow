package server

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHealthEndpointDoesNotRequireManagementToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{managementToken: "management-token"}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group("/debug"))

	response := serveHTTP(engine, http.MethodGet, "/debug/healthz", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /debug/healthz status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Body.String() != `{"data":{"status":"ok"}}` {
		t.Fatalf("GET /debug/healthz body = %s", response.Body.String())
	}
}
