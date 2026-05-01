package neo

import (
	"path/filepath"

	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
)

type Server struct {
	chatCtrl    *ChatController
	configCtrl  *ConfigController
	historyCtrl *HistoryController
}

func NewServer(services *fruntime.Services, cfg Config, baseDir string) (*Server, error) {
	store, err := NewHistoryStore(filepath.Join(baseDir, "history.db"))
	if err != nil {
		return nil, err
	}

	allTools := make(map[string]tools.Tool, len(services.Tools))
	toolFlags := make(map[string]bool, len(services.Tools))
	for name, tool := range services.Tools {
		allTools[name] = tool
		toolFlags[name] = true
	}

	chatCtrl := NewChatController(services, &cfg, toolFlags, baseDir, store)
	configCtrl := NewConfigController(&cfg, allTools, toolFlags)
	historyCtrl := NewHistoryController(chatCtrl)

	return &Server{
		chatCtrl:    chatCtrl,
		configCtrl:  configCtrl,
		historyCtrl: historyCtrl,
	}, nil
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
