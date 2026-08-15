package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var errManagementUnauthorized = errors.New("management authentication is required")

func (s *Server) requireManagementAuth(c *gin.Context) {
	if s == nil || s.managementToken == "" {
		c.Next()
		return
	}
	provided := managementBearerToken(c)
	expectedHash := sha256.Sum256([]byte(s.managementToken))
	providedHash := sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
		c.Header("WWW-Authenticate", `Bearer realm="weaveflow-management"`)
		writeError(c, http.StatusUnauthorized, errManagementUnauthorized)
		c.Abort()
		return
	}
	c.Next()
}

func managementBearerToken(c *gin.Context) string {
	parts := strings.Fields(strings.TrimSpace(c.GetHeader("Authorization")))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
