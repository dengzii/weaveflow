package neo

import (
	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
)

type Server struct {
	chatCtrl    *ChatController
	configCtrl  *ConfigController
	historyCtrl *HistoryController
}

func NewServer(services *fruntime.Services, cfg Config, baseDir string) *Server {
	allTools := make(map[string]tools.Tool, len(services.Tools))
	toolFlags := make(map[string]bool, len(services.Tools))
	for name, tool := range services.Tools {
		allTools[name] = tool
		toolFlags[name] = true
	}

	chatCtrl := NewChatController(services, &cfg, toolFlags, "auto", baseDir)
	configCtrl := NewConfigController(&cfg, allTools, toolFlags, "auto")
	historyCtrl := NewHistoryController(chatCtrl)

	return &Server{
		chatCtrl:    chatCtrl,
		configCtrl:  configCtrl,
		historyCtrl: historyCtrl,
	}
}

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/chat", s.chatCtrl.Handle)

	configGroup := group.Group("/config")
	{
		configGroup.GET("", s.configCtrl.Get)
		configGroup.PUT("", s.configCtrl.Update)
	}

	group.GET("/history", s.historyCtrl.Get)
}
