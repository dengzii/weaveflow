package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHealthEndpointDoesNotRequireManagementToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{
		cfg: Config{
			Version:   "0.1.0",
			BuildTime: "2026-08-21T04:05:06Z",
		},
		managementToken: "management-token",
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group("/debug"))

	response := serveHTTP(engine, http.MethodGet, "/debug/healthz", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /debug/healthz status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET /debug/healthz body: %v", err)
	}
	if body.Data["status"] != "ok" || body.Data["version"] != "0.1.0" || body.Data["build_time"] != "2026-08-21T04:05:06Z" {
		t.Fatalf("GET /debug/healthz data = %#v", body.Data)
	}
}
