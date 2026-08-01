package server

import "github.com/gin-gonic/gin"

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	if s == nil || group == nil {
		return
	}

	s.registerGraphRoutes(group)
	s.registerRuntimeRoutes(group)
	s.registerRegistryRoutes(group)
	s.registerChatChannelRoutes(group)
	s.registerTriggerRoutes(group)
	s.registerRunRoutes(group)
}

func (s *Server) registerGraphRoutes(group *gin.RouterGroup) {
	group.GET("/graph", s.handleGetGraph)
	group.PUT("/graph", s.handleUpdateGraph)
	group.POST("/graph/publish", s.handlePublishGraph)
	group.GET("/graph/definition", s.handleGetGraphDefinition)
	group.GET("/graph/nodes", s.handleGetGraphNodes)
	group.GET("/graph/initial-state-requirements", s.handleGetGraphInitialStateRequirements)
	group.POST("/graph/initial-state-requirements", s.handleAnalyzeGraphInitialStateRequirements)
	group.GET("/graph/mermaid", s.handleGetGraphMermaid)
	group.GET("/graphs", s.handleListGraphs)
}

func (s *Server) registerRuntimeRoutes(group *gin.RouterGroup) {
	group.GET("/runtime/settings", s.handleGetRuntimeSettings)
	group.PUT("/runtime/settings", s.handleUpdateRuntimeSettings)
	group.GET("/runtime/tools", s.handleListRuntimeTools)
	group.GET("/runtime/events/stream", s.handleRuntimeEventStream)
}

func (s *Server) registerRegistryRoutes(group *gin.RouterGroup) {
	group.GET("/registry", s.handleGetRegistry)
}

func (s *Server) registerChatChannelRoutes(group *gin.RouterGroup) {
	group.POST("/chat-channels/:channel_id/setup-sessions", s.handleStartChatChannelSetup)
	group.POST("/chat-channels/:channel_id/setup-sessions/:session_id/poll", s.handlePollChatChannelSetup)
	group.DELETE("/chat-channels/:channel_id/setup-sessions/:session_id", s.handleCancelChatChannelSetup)
}

func (s *Server) registerTriggerRoutes(group *gin.RouterGroup) {
	group.POST("/triggers", s.handleCreateTrigger)
	group.GET("/triggers", s.handleListTriggers)
	group.GET("/triggers/:trigger_id", s.handleGetTrigger)
	group.PUT("/triggers/:trigger_id", s.handleUpdateTrigger)
	group.DELETE("/triggers/:trigger_id", s.handleDeleteTrigger)
	group.POST("/triggers/:trigger_id/invocations", s.handleCreateTriggerInvocation)
	group.GET("/triggers/:trigger_id/invocations", s.handleListTriggerInvocations)
	group.GET("/triggers/:trigger_id/webhook", s.handleWebhookTrigger)
	group.POST("/triggers/:trigger_id/chat", s.handleChatTrigger)
	group.GET("/trigger-invocations", s.handleListTriggerInvocations)
}

func (s *Server) registerRunRoutes(group *gin.RouterGroup) {
	group.POST("/runs", s.handleStartRun)
	group.GET("/runs", s.handleListRuns)
	group.GET("/runs/:run_id", s.handleGetRun)
	group.DELETE("/runs/:run_id", s.handleDeleteRun)
	group.POST("/runs/:run_id/pause", s.handlePauseRun)
	group.POST("/runs/:run_id/resume", s.handleResumeRun)
	group.POST("/runs/:run_id/cancel", s.handleCancelRun)
	group.GET("/runs/:run_id/interrupt", s.handleGetRunInterrupt)
	group.GET("/runs/:run_id/steps", s.handleListSteps)
	group.GET("/runs/:run_id/checkpoints", s.handleListCheckpoints)
	group.GET("/runs/:run_id/events", s.handleListEvents)
	group.GET("/runs/:run_id/artifacts", s.handleListArtifacts)
	group.GET("/runs/:run_id/artifacts/:artifact_id", s.handleGetArtifact)
	group.GET("/checkpoints/:checkpoint_id", s.handleGetCheckpoint)
	group.POST("/checkpoints/:checkpoint_id/resume", s.handleResumeCheckpoint)
}
