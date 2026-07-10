package server

import "github.com/gin-gonic/gin"

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	if s == nil || group == nil {
		return
	}

	group.GET("/graph", s.handleGraph)
	group.POST("/graph", s.handleSetGraph)
	group.PUT("/graph", s.handleSetGraph)
	group.GET("/graph/settings", s.handleGraphSettings)
	group.PUT("/graph/settings", s.handleSetGraphSettings)
	group.GET("/graph/definition", s.handleGraphDefinition)
	group.GET("/graph/nodes", s.handleGraphNodes)
	group.GET("/graph/initial-state-requirements", s.handleGraphInitialStateRequirements)
	group.POST("/graph/initial-state-requirements", s.handleAnalyzeGraphInitialStateRequirements)
	group.GET("/graph/mermaid", s.handleGraphMermaid)

	group.GET("/registry", s.handleRegistry)
	group.GET("/tools", s.handleTools)

	group.POST("/runs", s.handleStartRun)
	group.POST("/runs/:run_id/resume", s.handleResumeRun)
	group.POST("/checkpoints/:checkpoint_id/resume", s.handleResumeCheckpoint)
	group.POST("/runs/:run_id/pause", s.handlePauseRun)
	group.POST("/runs/:run_id/cancel", s.handleCancelRun)

	group.GET("/runs", s.handleListRuns)
	group.GET("/runs/:run_id", s.handleGetRun)
	group.DELETE("/runs/:run_id", s.handleDeleteRun)
	group.GET("/runs/:run_id/detail", s.handleGetRunDetail)
	group.GET("/runs/:run_id/steps", s.handleListSteps)
	group.GET("/runs/:run_id/checkpoints", s.handleListCheckpoints)
	group.GET("/checkpoints/:checkpoint_id", s.handleGetCheckpoint)
	group.GET("/runs/:run_id/events", s.handleListEvents)
	group.GET("/runs/:run_id/artifacts", s.handleListArtifacts)
	group.GET("/runs/:run_id/artifacts/:artifact_id", s.handleGetArtifact)

	group.GET("/events/stream", s.handleEventStream)
	group.GET("/graphs", s.handleListGraphs)
}
