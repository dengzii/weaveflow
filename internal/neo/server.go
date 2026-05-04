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
	hub         *LiveHub
}

func NewServer(services *fruntime.Services, cfg Config, baseDir string) (*Server, error) {
	store, err := NewStore(filepath.Join(baseDir, "history.db"))
	if err != nil {
		return nil, err
	}

	allTools := make(map[string]tools.Tool, len(services.Tools))
	toolFlags := make(map[string]bool, len(services.Tools))
	for name, tool := range services.Tools {
		allTools[name] = tool
		toolFlags[name] = true
	}

	if persisted, ok, err := store.LoadConfig(); err != nil {
		return nil, err
	} else if ok {
		applyPersistedConfig(&cfg, toolFlags, persisted)
	}

	hub := NewLiveHub()
	chatCtrl := NewChatController(services, &cfg, toolFlags, baseDir, store, hub)
	configCtrl := NewConfigController(&cfg, allTools, toolFlags, store)
	historyCtrl := NewHistoryController(chatCtrl)

	return &Server{
		chatCtrl:    chatCtrl,
		configCtrl:  configCtrl,
		historyCtrl: historyCtrl,
		hub:         hub,
	}, nil
}

// Hub returns the live event hub shared with the chat controller.
func (s *Server) Hub() *LiveHub {
	return s.hub
}

func applyPersistedConfig(cfg *Config, toolFlags map[string]bool, persisted PersistedConfig) {
	cfg.SystemPrompt = persisted.SystemPrompt
	cfg.MaxIterations = persisted.MaxIterations
	cfg.PlannerMaxSteps = persisted.PlannerMaxSteps
	cfg.MemoryRecallLimit = persisted.MemoryRecallLimit
	if persisted.Mode != "" {
		cfg.Mode = persisted.Mode
	}
	for name, enabled := range persisted.ToolFlags {
		if _, ok := toolFlags[name]; ok {
			toolFlags[name] = enabled
		}
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
