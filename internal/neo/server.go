package neo

import (
	"context"
	"path/filepath"

	"weaveflow/builtin"
	"weaveflow/core"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
)

type Server struct {
	chatCtrl     *ChatController
	configCtrl   *ConfigController
	historyCtrl  *HistoryController
	registryCtrl *RegistryController
	hub          *LiveHub
}

func NewServer(baseCtx context.Context, cfg Config, baseDir string) (*Server, error) {
	store, err := NewStore(filepath.Join(baseDir, "history.db"))
	if err != nil {
		return nil, err
	}

	availableTools := core.ToolsFromContext(baseCtx)
	allTools := make(map[string]tools.Tool, len(availableTools))
	toolFlags := make(map[string]bool, len(availableTools))
	for name, tool := range availableTools {
		allTools[name] = tool
		toolFlags[name] = true
	}

	if persisted, ok, err := store.LoadConfig(); err != nil {
		return nil, err
	} else if ok {
		applyPersistedConfig(&cfg, toolFlags, persisted)
	}

	hub := NewLiveHub()
	chatCtrl := NewChatController(baseCtx, &cfg, toolFlags, baseDir, store, hub)
	configCtrl := NewConfigController(&cfg, allTools, toolFlags, store)
	historyCtrl := NewHistoryController(chatCtrl)
	registryCtrl := NewRegistryController(builtin.NewDefaultRegistry())

	return &Server{
		chatCtrl:     chatCtrl,
		configCtrl:   configCtrl,
		historyCtrl:  historyCtrl,
		registryCtrl: registryCtrl,
		hub:          hub,
	}, nil
}

// Hub returns the live event hub shared with the chat controller.
func (s *Server) Hub() *LiveHub {
	return s.hub
}

func applyPersistedConfig(cfg *Config, toolFlags map[string]bool, persisted PersistedConfig) {
	cfg.SystemPrompt = persisted.SystemPrompt
	cfg.MaxIterations = persisted.MaxIterations
	if persisted.RequestTimeoutSeconds > 0 {
		cfg.RequestTimeoutSeconds = persisted.RequestTimeoutSeconds
	}
	cfg.PlannerMaxSteps = persisted.PlannerMaxSteps
	cfg.MemoryRecallLimit = persisted.MemoryRecallLimit
	if persisted.HistoryRecentTurns > 0 {
		cfg.HistoryRecentTurns = persisted.HistoryRecentTurns
	}
	if persisted.HistorySummaryMaxChars > 0 {
		cfg.HistorySummaryMaxChars = persisted.HistorySummaryMaxChars
	}
	if persisted.PromptMaxChars > 0 {
		cfg.PromptMaxChars = persisted.PromptMaxChars
	}
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
	group.POST("/resume", s.chatCtrl.Resume)
	group.GET("/status", s.chatCtrl.Status)

	configGroup := group.Group("/config")
	{
		configGroup.GET("", s.configCtrl.Get)
		configGroup.PUT("", s.configCtrl.Update)
	}

	group.GET("/history", s.historyCtrl.Get)
	group.DELETE("/history", s.historyCtrl.Clear)
	group.GET("/memory", s.historyCtrl.GetMemory)
	group.DELETE("/memory", s.historyCtrl.ClearMemory)
	group.GET("/registry", s.registryCtrl.Get)
}
