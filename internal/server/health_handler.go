package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleHealth(c *gin.Context) {
	writeData(c, http.StatusOK, map[string]string{"status": "ok"})
}
