package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const defaultCORSOrigins = "http://localhost:3031,http://127.0.0.1:3031"

func corsMiddleware(configuredOrigins string) gin.HandlerFunc {
	allowedOrigins := make(map[string]struct{})
	allowAll := false
	for _, item := range strings.Split(configuredOrigins, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		if origin == "*" {
			allowAll = true
			continue
		}
		allowedOrigins[strings.TrimRight(origin, "/")] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := strings.TrimRight(strings.TrimSpace(c.GetHeader("Origin")), "/")
		if origin == "" {
			c.Next()
			return
		}

		_, allowed := allowedOrigins[origin]
		if !allowAll && !allowed {
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		responseOrigin := origin
		if allowAll {
			responseOrigin = "*"
		}
		c.Header("Access-Control-Allow-Origin", responseOrigin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
		c.Header("Access-Control-Max-Age", "600")
		c.Header("Vary", "Origin")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
