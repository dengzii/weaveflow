package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleHealth(c *gin.Context) {
	writeData(c, http.StatusOK, map[string]string{
		"status":     "ok",
		"version":    strings.TrimSpace(s.cfg.Version),
		"build_time": strings.TrimSpace(s.cfg.BuildTime),
	})
}
