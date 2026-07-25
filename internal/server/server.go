package server

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

const defaultListenAddr = ":8080"

func (s *Server) Engine() *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery())
	s.RegisterRoutes(&engine.RouterGroup)
	return engine
}

func (s *Server) Run(addr string) error {
	if strings.TrimSpace(addr) == "" {
		addr = defaultListenAddr
	}
	if err := s.Start(context.Background()); err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	return s.Engine().Run(addr)
}
