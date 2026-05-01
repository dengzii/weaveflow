package neo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
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

type ChatController struct {
	services  *fruntime.Services
	config    *Config
	allTools  map[string]tools.Tool
	toolFlags map[string]bool
	baseDir   string
	store     *HistoryStore

	mu          sync.RWMutex
	runner      *fruntime.GraphRunner
	runID       string
	cancelFn    context.CancelFunc
	lastState   fruntime.State
	graphCache  *weaveflow.Graph
	graphCfgKey Config
}

func NewChatController(services *fruntime.Services, cfg *Config, toolFlags map[string]bool, baseDir string, store *HistoryStore) *ChatController {
	allTools := make(map[string]tools.Tool, len(services.Tools))
	for name, tool := range services.Tools {
		allTools[name] = tool
	}
	return &ChatController{
		services:  services,
		config:    cfg,
		allTools:  allTools,
		toolFlags: toolFlags,
		baseDir:   baseDir,
		store:     store,
	}
}

type ChatRequest struct {
	Message string `json:"message"`
}

func (ctrl *ChatController) Handle(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "invalid request: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "message is required"})
		return
	}

	ctrl.mu.Lock()
	if ctrl.cancelFn != nil {
		ctrl.cancelFn()
		ctrl.cancelFn = nil
	}

	cfg := *ctrl.config
	services := ctrl.effectiveServices()

	var graph *weaveflow.Graph
	if ctrl.graphCache != nil && reflect.DeepEqual(ctrl.graphCfgKey, cfg) {
		graph = ctrl.graphCache
	} else {
		var buildErr error
		graph, buildErr = NewGraph(cfg)
		if buildErr != nil {
			ctrl.mu.Unlock()
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "build graph failed: " + buildErr.Error()})
			return
		}
		ctrl.graphCache = graph
		ctrl.graphCfgKey = cfg
		_ = graph.WriteToFile(filepath.Join(ctrl.baseDir, "graph.json"))
	}
	ctrl.mu.Unlock()

	log, _ := zap.NewDevelopment()
	fruntime.SetLogger(log)

	runDir := filepath.Join(ctrl.baseDir, fmt.Sprintf("run_%d", time.Now().UnixMilli()))
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

	baseCtx := fruntime.WithServices(c.Request.Context(), services)
	ctx, cancel := context.WithCancel(baseCtx)

	ctrl.mu.Lock()
	ctrl.runner = runner
	ctrl.cancelFn = cancel
	ctrl.mu.Unlock()

	type runResult struct {
		run   fruntime.RunRecord
		state fruntime.State
		err   error
	}
	done := make(chan runResult, 1)

	var loadedHistory []HistoryMessage
	var history []llms.MessageContent
	if ctrl.store != nil {
		loadedHistory, _ = ctrl.store.Load()
		history = make([]llms.MessageContent, len(loadedHistory))
		for i, hm := range loadedHistory {
			history[i] = historyToLLM(hm)
		}
	}
	historyLLMLen := len(history)
	initialState := NewInitialState(req.Message, history)
	store := ctrl.store
	go func() {
		defer channelSink.Close()
		run, state, runErr := runner.Start(ctx, initialState)
		if state != nil && store != nil {
			allMsgs := state.Conversation(stateScope).Messages()
			newMsgs := allMsgs
			if historyLLMLen <= len(allMsgs) {
				newMsgs = allMsgs[historyLLMLen:]
			}
			var reasoningBlocks []string
			if blocks, ok := state[fruntime.StateKeyReasoningBlocks].([]string); ok {
				reasoningBlocks = blocks
			}
			newHistory := convertMessages(newMsgs, reasoningBlocks)
			fullHistory := make([]HistoryMessage, len(loadedHistory), len(loadedHistory)+len(newHistory))
			copy(fullHistory, loadedHistory)
			fullHistory = append(fullHistory, newHistory...)
			runStatus := runStatusString(ctx, runErr)
			_ = store.Save(fullHistory, runStatus)
		}
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
			ctrl.mu.Lock()
			ctrl.cancelFn = nil
			ctrl.mu.Unlock()
			return
		case event, ok := <-channelSink.Events():
			if !ok {
				res := <-done
				ctrl.mu.Lock()
				ctrl.lastState = res.state
				if res.run.RunID != "" {
					ctrl.runID = res.run.RunID
				}
				ctrl.cancelFn = nil
				ctrl.mu.Unlock()

				if res.err != nil && res.state == nil {
					writeSSE(c, &ChatEvent{
						Type:    ChatEventTypeError,
						Content: res.err.Error(),
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

func (ctrl *ChatController) effectiveServices() *fruntime.Services {
	enabledTools := make(map[string]tools.Tool, len(ctrl.allTools))
	for name, tool := range ctrl.allTools {
		if ctrl.toolFlags[name] {
			enabledTools[name] = tool
		}
	}
	return &fruntime.Services{
		Model:  ctrl.services.Model,
		Memory: ctrl.services.Memory,
		Tools:  enabledTools,
	}
}

func (ctrl *ChatController) GetLastState() fruntime.State {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ctrl.lastState
}

func (ctrl *ChatController) GetHistory() ([]HistoryMessage, error) {
	if ctrl.store != nil {
		return ctrl.store.Load()
	}
	state := ctrl.GetLastState()
	if state == nil {
		return []HistoryMessage{}, nil
	}
	return convertMessages(state.Conversation(stateScope).Messages(), nil), nil
}

func writeSSE(c *gin.Context, event *ChatEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

// runStatusString determines the run outcome label for history persistence.
func runStatusString(ctx context.Context, runErr error) string {
	if runErr == nil {
		return "completed"
	}
	if ctx.Err() != nil {
		return "stopped"
	}
	return "failed"
}
