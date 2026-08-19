package server

import "github.com/gin-gonic/gin"

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	if s == nil || group == nil {
		return
	}

	management := group.Group("")
	management.Use(s.requireManagementAuth)
	s.registerGraphRoutes(management)
	s.registerRuntimeRoutes(management)
	s.registerRegistryRoutes(management)
	s.registerChatChannelRoutes(management)
	s.registerTriggerManagementRoutes(management)
	s.registerRunRoutes(management)
	s.registerPublicTriggerRoutes(group)
}

func (s *Server) registerGraphRoutes(group *gin.RouterGroup) {
	group.GET("/graphs", s.handleListGraphs)
	group.GET("/graphs/:graph_id", s.handleGetGraphDetail)
	group.DELETE("/graphs/:graph_id", s.handleDeleteGraph)
	group.GET("/graphs/:graph_id/retention-audit", s.handleGetRetentionAudit)
	group.POST("/graphs/:graph_id/sessions", s.handleCreateGraphSession)
	group.GET("/graphs/:graph_id/sessions/:session_id", s.handleGetGraphSessionDetail)
	group.POST("/graphs/:graph_id/analysis/initial-state-requirements", s.handleAnalyzeGraphInitialStateRequirements)
}

func (s *Server) registerRuntimeRoutes(group *gin.RouterGroup) {
	group.GET("/runtime/tools", s.handleListRuntimeTools)
}

func (s *Server) registerRegistryRoutes(group *gin.RouterGroup) {
	group.GET("/registry", s.handleGetRegistry)
}

func (s *Server) registerChatChannelRoutes(group *gin.RouterGroup) {
	group.POST("/chat-channels/:channel_id/setup-sessions", s.handleStartChatChannelSetup)
	group.GET("/chat-channels/:channel_id/setup-sessions/:session_id", s.handleGetChatChannelSetup)
	group.POST("/chat-channels/:channel_id/setup-sessions/:session_id/verification", s.handleSubmitChatChannelSetupVerification)
	group.DELETE("/chat-channels/:channel_id/setup-sessions/:session_id", s.handleCancelChatChannelSetup)
}

func (s *Server) registerTriggerManagementRoutes(group *gin.RouterGroup) {
	group.GET("/graphs/:graph_id/triggers", s.handleListTriggers)
	group.PUT("/graphs/:graph_id/triggers", s.handleReplaceTriggers)
}

func (s *Server) registerPublicTriggerRoutes(group *gin.RouterGroup) {
	group.POST("/graphs/:graph_id/triggers/:trigger_id/invocations", s.handleCreateTriggerInvocation)
	group.POST("/graphs/:graph_id/triggers/:trigger_id/webhook", s.handleWebhookTrigger)
	group.POST("/graphs/:graph_id/triggers/:trigger_id/chat", s.handleChatTrigger)
}

func (s *Server) registerRunRoutes(group *gin.RouterGroup) {
	group.POST("/graphs/:graph_id/sessions/:session_id/runs", s.handleStartRun)
	group.GET("/graphs/:graph_id/runs", s.handleListRuns)
	group.GET("/graphs/:graph_id/runs/:run_id/inspection", s.handleGetRunInspection)
	group.DELETE("/graphs/:graph_id/runs/:run_id", s.handleDeleteRun)
	group.POST("/graphs/:graph_id/runs/:run_id/pause", s.handlePauseRun)
	group.POST("/graphs/:graph_id/runs/:run_id/resume", s.handleResumeRun)
	group.POST("/graphs/:graph_id/runs/:run_id/steps/:step_id/effect-resolution", s.handleResolveEffect)
	group.POST("/graphs/:graph_id/runs/:run_id/cancel", s.handleCancelRun)
	group.GET("/graphs/:graph_id/runs/:run_id/events", s.handleListEvents)
	group.GET("/graphs/:graph_id/runs/:run_id/artifacts", s.handleListArtifacts)
	group.GET("/graphs/:graph_id/runs/:run_id/artifacts/:artifact_id", s.handleGetArtifact)
	group.GET("/graphs/:graph_id/runs/:run_id/checkpoints/:checkpoint_id", s.handleGetCheckpoint)
	group.GET("/graphs/:graph_id/events/stream", s.handleRuntimeEventStream)
}
