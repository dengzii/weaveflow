package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSMiddlewareAllowsConfiguredOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(corsMiddleware("https://web.example.com"))
	engine.GET("/graph", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/graph", nil)
	req.Header.Set("Origin", "https://web.example.com")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://web.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestCORSMiddlewareHandlesPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(corsMiddleware("https://web.example.com"))

	req := httptest.NewRequest(http.MethodOptions, "/graph", nil)
	req.Header.Set("Origin", "https://web.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); !stringsContainToken(got, http.MethodPut) {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
}

func TestCORSMiddlewareRejectsUnknownPreflightOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(corsMiddleware("https://web.example.com"))

	req := httptest.NewRequest(http.MethodOptions, "/graph", nil)
	req.Header.Set("Origin", "https://other.example.com")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func stringsContainToken(value, token string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.TrimSpace(item) == token {
			return true
		}
	}
	return false
}
