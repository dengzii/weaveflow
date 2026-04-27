package neo

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"weaveflow"
	fruntime "weaveflow/runtime"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	buildCtx  *weaveflow.BuildContext
	config    Config
	allTools  map[string]tools.Tool
	toolFlags map[string]bool
	mode      string // "auto", "direct", "planner"
	baseDir   string

	mu        sync.RWMutex
	runner    *fruntime.GraphRunner
	runID     string
	cancelFn  context.CancelFunc
	lastState fruntime.State
}

func NewServer(buildCtx *weaveflow.BuildContext, cfg Config, baseDir string) *Server {
	allTools := make(map[string]tools.Tool, len(buildCtx.Tools))
	toolFlags := make(map[string]bool, len(buildCtx.Tools))
	for name, tool := range buildCtx.Tools {
		allTools[name] = tool
		toolFlags[name] = true
	}
	return &Server{
		buildCtx:  buildCtx,
		config:    cfg,
		allTools:  allTools,
		toolFlags: toolFlags,
		mode:      "auto",
		baseDir:   baseDir,
	}
}

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/chat", s.Chat)
	group.GET("/config", s.GetConfig)
	group.PUT("/config", s.UpdateConfig)
	group.GET("/history", s.GetHistory)

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

// --- Chat SSE ---

type ChatRequest struct {
	Message string `json:"message"`
}

func (s *Server) Chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "invalid request: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "message is required"})
		return
	}

	s.mu.Lock()
	if s.cancelFn != nil {
		s.cancelFn()
		s.cancelFn = nil
	}

	cfg := s.config
	buildCtx := s.effectiveBuildContext()
	s.mu.Unlock()

	graph, err := NewGraph(buildCtx, cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "build graph failed: " + err.Error()})
		return
	}

	log, _ := zap.NewDevelopment()
	fruntime.SetLogger(log)

	runDir := filepath.Join(s.baseDir, fmt.Sprintf("run_%d", time.Now().UnixMilli()))
	channelSink := NewChannelEventSink()
	combinedSink := fruntime.NewCombineEventSink(
		channelSink,
		fruntime.NewLoggerEventSink(log),
		fruntime.NewFileEventSink(filepath.Join(runDir, "events")),
	)

	executionStore := fruntime.NewFileExecutionStore(filepath.Join(runDir, "execution"))
	checkpointStore := fruntime.NewFileCheckpointStore(filepath.Join(runDir, "checkpoints"))
	stateCodec := fruntime.NewJSONStateCodec(fruntime.DefaultStateVersion)

	runner := weaveflow.NewGraphRunner(graph, executionStore, checkpointStore, stateCodec, combinedSink)
	runner.ArtifactStore = fruntime.NewFileArtifactStore(filepath.Join(runDir, "artifacts"))

	ctx, cancel := context.WithCancel(c.Request.Context())

	s.mu.Lock()
	s.runner = runner
	s.cancelFn = cancel
	s.mu.Unlock()

	type runResult struct {
		run   fruntime.RunRecord
		state fruntime.State
		err   error
	}
	done := make(chan runResult, 1)

	initialState := NewInitialState(req.Message)
	go func() {
		defer channelSink.Close()
		run, state, runErr := runner.Start(ctx, initialState)
		done <- runResult{run: run, state: state, err: runErr}
	}()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			cancel()
			s.mu.Lock()
			s.cancelFn = nil
			s.mu.Unlock()
			return
		case event, ok := <-channelSink.Events():
			if !ok {
				res := <-done
				s.mu.Lock()
				s.lastState = res.state
				if res.run.RunID != "" {
					s.runID = res.run.RunID
				}
				s.cancelFn = nil
				s.mu.Unlock()

				if res.err != nil && res.state == nil {
					writeSSE(c, &ChatEvent{
						Type:      ActionError,
						Message:   res.err.Error(),
						Timestamp: time.Now(),
					})
				}
				return
			}

			chatEvent := TranslateEvent(event)
			if chatEvent != nil {
				writeSSE(c, chatEvent)
			}
		}
	}
}

func writeSSE(c *gin.Context, event *ChatEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

// --- Config ---

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

func (s *Server) GetConfig(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "ok",
		"data": ConfigResponse{
			SystemPrompt:      s.config.SystemPrompt,
			MaxIterations:     s.config.MaxIterations,
			PlannerMaxSteps:   s.config.PlannerMaxSteps,
			MemoryRecallLimit: s.config.MemoryRecallLimit,
			Tools:             s.toolFlags,
			Mode:              s.mode,
		},
	})
}

func (s *Server) UpdateConfig(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "invalid request: " + err.Error()})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.SystemPrompt != nil {
		s.config.SystemPrompt = *req.SystemPrompt
	}
	if req.MaxIterations != nil && *req.MaxIterations > 0 {
		s.config.MaxIterations = *req.MaxIterations
	}
	if req.PlannerMaxSteps != nil && *req.PlannerMaxSteps > 0 {
		s.config.PlannerMaxSteps = *req.PlannerMaxSteps
	}
	if req.MemoryRecallLimit != nil && *req.MemoryRecallLimit >= 0 {
		s.config.MemoryRecallLimit = *req.MemoryRecallLimit
	}
	if req.Mode != nil {
		mode := strings.TrimSpace(*req.Mode)
		if mode == "auto" || mode == "direct" || mode == "planner" {
			s.mode = mode
		}
	}
	for name, enabled := range req.Tools {
		if _, exists := s.allTools[name]; exists {
			s.toolFlags[name] = enabled
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "ok",
		"data": ConfigResponse{
			SystemPrompt:      s.config.SystemPrompt,
			MaxIterations:     s.config.MaxIterations,
			PlannerMaxSteps:   s.config.PlannerMaxSteps,
			MemoryRecallLimit: s.config.MemoryRecallLimit,
			Tools:             s.toolFlags,
			Mode:              s.mode,
		},
	})
}

// --- History ---

type HistoryMessage struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts,omitempty"`
}

type MessagePart struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Result string `json:"result,omitempty"`
}

func (s *Server) GetHistory(c *gin.Context) {
	s.mu.RLock()
	state := s.lastState
	s.mu.RUnlock()

	if state == nil {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": []HistoryMessage{}})
		return
	}

	conversation := fruntime.Conversation(state, stateScope)
	messages := conversation.Messages()
	history := convertMessages(messages)

	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": history})
}

func convertMessages(messages []llms.MessageContent) []HistoryMessage {
	result := make([]HistoryMessage, 0, len(messages))
	for _, msg := range messages {
		hm := HistoryMessage{Role: string(msg.Role)}
		for _, part := range msg.Parts {
			hm.Parts = append(hm.Parts, convertPart(part))
		}
		result = append(result, hm)
	}
	return result
}

func convertPart(part llms.ContentPart) MessagePart {
	switch p := part.(type) {
	case llms.TextContent:
		return MessagePart{Type: "text", Text: p.Text}
	case llms.ToolCall:
		return MessagePart{Type: "tool_call", Name: p.FunctionCall.Name, Text: p.FunctionCall.Arguments}
	case llms.ToolCallResponse:
		return MessagePart{Type: "tool_result", Name: p.Name, Result: p.Content}
	default:
		return MessagePart{Type: "unknown"}
	}
}

func (s *Server) effectiveBuildContext() *weaveflow.BuildContext {
	enabledTools := make(map[string]tools.Tool, len(s.allTools))
	for name, tool := range s.allTools {
		if s.toolFlags[name] {
			enabledTools[name] = tool
		}
	}
	return &weaveflow.BuildContext{
		Model:  s.buildCtx.Model,
		Memory: s.buildCtx.Memory,
		Tools:  enabledTools,
	}
}
