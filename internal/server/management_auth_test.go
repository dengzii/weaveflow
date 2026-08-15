package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestManagementAuthenticationProtectsControlPlane(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, err := New(context.Background(), Config{BaseDir: t.TempDir(), ManagementToken: "management-secret"})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	srv.RegisterRoutes(engine.Group(""))

	unauthorized := httptest.NewRecorder()
	engine.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/graphs", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/graphs", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer management-secret")
	authorized := httptest.NewRecorder()
	engine.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", authorized.Code, authorized.Body.String())
	}

	public := httptest.NewRecorder()
	engine.ServeHTTP(public, httptest.NewRequest(http.MethodPost, "/graphs/missing/triggers/missing/webhook", nil))
	if public.Code == http.StatusUnauthorized {
		t.Fatalf("public trigger unexpectedly required management authentication: %s", public.Body.String())
	}
}
