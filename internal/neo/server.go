package neo

import (
	"embed"
	"io/fs"
	"net/http"

	"weaveflow"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	chatCtrl    *ChatController
	configCtrl  *ConfigController
	historyCtrl *HistoryController
}

func NewServer(buildCtx *weaveflow.BuildContext, cfg Config, baseDir string) *Server {
	allTools := make(map[string]tools.Tool, len(buildCtx.Tools))
	toolFlags := make(map[string]bool, len(buildCtx.Tools))
	for name, tool := range buildCtx.Tools {
		allTools[name] = tool
		toolFlags[name] = true
	}

	chatCtrl := NewChatController(buildCtx, &cfg, toolFlags, "auto", baseDir)
	configCtrl := NewConfigController(&cfg, allTools, toolFlags, "auto")
	historyCtrl := NewHistoryController(chatCtrl)

	return &Server{
		chatCtrl:    chatCtrl,
		configCtrl:  configCtrl,
		historyCtrl: historyCtrl,
	}
}

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	// Chat routes
	group.POST("/chat", s.chatCtrl.Handle)

	// Config routes
	configGroup := group.Group("/config")
	{
		configGroup.GET("", s.configCtrl.Get)
		configGroup.PUT("", s.configCtrl.Update)
	}

	// History routes
	group.GET("/history", s.historyCtrl.Get)

	// Static files
	staticSub, _ := fs.Sub(staticFiles, "static")
	group.StaticFS("/static", http.FS(staticSub))
	group.GET("/", func(c *gin.Context) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
}
