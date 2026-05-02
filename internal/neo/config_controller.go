package neo

import (
	"net/http"
	"strings"
	"sync"

	"weaveflow/tools"

	"github.com/gin-gonic/gin"
)

type ConfigController struct {
	config    *Config
	allTools  map[string]tools.Tool
	toolFlags map[string]bool
	mode      string
	store     *Store

	mu sync.RWMutex
}

func NewConfigController(cfg *Config, allTools map[string]tools.Tool, toolFlags map[string]bool, store *Store) *ConfigController {
	return &ConfigController{
		config:    cfg,
		allTools:  allTools,
		toolFlags: toolFlags,
		mode:      cfg.Mode,
		store:     store,
	}
}

type ConfigResponse struct {
	SystemPrompt      string          `json:"system_prompt"`
	MaxIterations     int             `json:"max_iterations"`
	PlannerMaxSteps   int             `json:"planner_max_steps"`
	MemoryRecallLimit int             `json:"memory_recall_limit"`
	Tools             map[string]bool `json:"tools"`
	Mode              string          `json:"mode"`
}

type UpdateConfigRequest struct {
	SystemPrompt      *string         `json:"system_prompt,omitempty"`
	MaxIterations     *int            `json:"max_iterations,omitempty"`
	PlannerMaxSteps   *int            `json:"planner_max_steps,omitempty"`
	MemoryRecallLimit *int            `json:"memory_recall_limit,omitempty"`
	Tools             map[string]bool `json:"tools,omitempty"`
	Mode              *string         `json:"mode,omitempty"`
}

func (ctrl *ConfigController) Get(c *gin.Context) {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "ok",
		"data": ConfigResponse{
			SystemPrompt:      ctrl.config.SystemPrompt,
			MaxIterations:     ctrl.config.MaxIterations,
			PlannerMaxSteps:   ctrl.config.PlannerMaxSteps,
			MemoryRecallLimit: ctrl.config.MemoryRecallLimit,
			Tools:             ctrl.toolFlags,
			Mode:              ctrl.mode,
		},
	})
}

func (ctrl *ConfigController) Update(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "invalid request: " + err.Error()})
		return
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	if req.SystemPrompt != nil {
		ctrl.config.SystemPrompt = *req.SystemPrompt
	}
	if req.MaxIterations != nil && *req.MaxIterations > 0 {
		ctrl.config.MaxIterations = *req.MaxIterations
	}
	if req.PlannerMaxSteps != nil && *req.PlannerMaxSteps > 0 {
		ctrl.config.PlannerMaxSteps = *req.PlannerMaxSteps
	}
	if req.MemoryRecallLimit != nil && *req.MemoryRecallLimit >= 0 {
		ctrl.config.MemoryRecallLimit = *req.MemoryRecallLimit
	}
	if req.Mode != nil {
		mode := strings.TrimSpace(*req.Mode)
		if mode == "auto" || mode == "direct" || mode == "planner" {
			ctrl.mode = mode
			ctrl.config.Mode = mode
		}
	}
	for name, enabled := range req.Tools {
		if _, exists := ctrl.allTools[name]; exists {
			ctrl.toolFlags[name] = enabled
		}
	}

	if ctrl.store != nil {
		if err := ctrl.store.SaveConfig(PersistedConfig{
			SystemPrompt:      ctrl.config.SystemPrompt,
			MaxIterations:     ctrl.config.MaxIterations,
			PlannerMaxSteps:   ctrl.config.PlannerMaxSteps,
			MemoryRecallLimit: ctrl.config.MemoryRecallLimit,
			ToolFlags:         cloneToolFlags(ctrl.toolFlags),
			Mode:              ctrl.mode,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "save config failed: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "ok",
		"data": ConfigResponse{
			SystemPrompt:      ctrl.config.SystemPrompt,
			MaxIterations:     ctrl.config.MaxIterations,
			PlannerMaxSteps:   ctrl.config.PlannerMaxSteps,
			MemoryRecallLimit: ctrl.config.MemoryRecallLimit,
			Tools:             ctrl.toolFlags,
			Mode:              ctrl.mode,
		},
	})
}

func (ctrl *ConfigController) GetMode() string {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ctrl.mode
}

func (ctrl *ConfigController) GetToolFlags() map[string]bool {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ctrl.toolFlags
}

func cloneToolFlags(flags map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(flags))
	for name, enabled := range flags {
		cloned[name] = enabled
	}
	return cloned
}
