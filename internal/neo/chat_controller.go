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
	store     *Store

	mu          sync.RWMutex
	runner      *fruntime.GraphRunner
	runID       string
	cancelFn    context.CancelFunc
	lastState   fruntime.State
	graphCache  *weaveflow.Graph
	graphCfgKey Config
}

func NewChatController(services *fruntime.Services, cfg *Config, toolFlags map[string]bool, baseDir string, store *Store) *ChatController {
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

	var history []llms.MessageContent
	if ctrl.store != nil {
		var loadErr error
		history, loadErr = ctrl.store.LoadLLMMessages(defaultSessionID)
		if loadErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "load history failed: " + loadErr.Error()})
			return
		}
	}

	log, _ := zap.NewDevelopment()
	fruntime.SetLogger(log)

	runDir := filepath.Join(ctrl.baseDir, fmt.Sprintf("run_%d", time.Now().UnixMilli()))
	channelSink := NewChannelEventSink()
	sinks := []fruntime.EventSink{
		channelSink,
		fruntime.NewLoggerEventSink(log),
		fruntime.NewFileEventSink(filepath.Join(runDir, "events")),
	}

	var turnWriter *TurnWriter
	if ctrl.store != nil {
		var storeErr error
		turnWriter, storeErr = ctrl.store.BeginTurn(defaultSessionID, req.Message)
		if storeErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "persist turn failed: " + storeErr.Error()})
			return
		}
		sinks = append(sinks, turnWriter)
	}

	combinedSink := fruntime.NewCombineEventSink(sinks...)

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

	initialState := NewInitialState(req.Message, history)
	store := ctrl.store
	go func() {
		defer channelSink.Close()
		run, state, runErr := runner.Start(ctx, initialState)
		runStatus := runStatusString(ctx, runErr)
		if turnWriter != nil {
			_ = turnWriter.Finalize(runStatus)
		}
		if state != nil && store != nil {
			fullHistory := convertMessages(state.Conversation(stateScope).Messages())
			_ = store.SaveRawHistory(defaultSessionID, fullHistory, runStatus)
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
	streamedReasoningByStep := make(map[string]string)
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

			chatEvent := attachEventIdentity(event, TranslateEvent(event))
			rememberStreamedReasoningText(event, chatEvent, streamedReasoningByStep)
			chatEvent = syncReasoningSummary(event, chatEvent, streamedReasoningByStep)
			if chatEvent != nil {
				writeSSE(c, chatEvent)
			}
		}
	}
}

func attachEventIdentity(event fruntime.Event, chatEvent *ChatEvent) *ChatEvent {
	if chatEvent == nil {
		return nil
	}
	if chatEvent.Type != ChatEventTypeThinking && chatEvent.Type != ChatEventTypeGenerating && chatEvent.Type != ChatEventTypeStep {
		return chatEvent
	}

	data := map[string]string{}
	if len(chatEvent.Data) > 0 {
		if err := json.Unmarshal(chatEvent.Data, &data); err != nil {
			data = map[string]string{}
		}
	}

	if nodeID := strings.TrimSpace(event.NodeID); nodeID != "" {
		data["node_id"] = nodeID
	}
	if stepID := strings.TrimSpace(event.StepID); stepID != "" {
		data["step_id"] = stepID
	}
	if len(data) == 0 {
		return chatEvent
	}

	cloned := *chatEvent
	cloned.Data = marshalData(data)
	return &cloned
}

func rememberStreamedReasoningText(event fruntime.Event, chatEvent *ChatEvent, streamedReasoningByStep map[string]string) {
	if event.Type != fruntime.EventLLMReasoningChunk || chatEvent == nil || chatEvent.Type != ChatEventTypeThinking {
		return
	}
	key := streamEventKey(event)
	if key == "" || chatEvent.Content == "" {
		return
	}
	streamedReasoningByStep[key] += chatEvent.Content
}

func syncReasoningSummary(event fruntime.Event, chatEvent *ChatEvent, streamedReasoningByStep map[string]string) *ChatEvent {
	if event.Type != fruntime.EventLLMReasoning || chatEvent == nil || chatEvent.Type != ChatEventTypeThinking {
		return chatEvent
	}

	key := streamEventKey(event)
	if key == "" {
		return chatEvent
	}

	streamed := streamedReasoningByStep[key]
	delete(streamedReasoningByStep, key)
	if streamed == "" {
		return chatEvent
	}

	full := chatEvent.Content
	if full == streamed {
		return nil
	}
	if strings.HasPrefix(full, streamed) {
		suffix := full[len(streamed):]
		if suffix == "" {
			return nil
		}
		cloned := *chatEvent
		cloned.Content = suffix
		return &cloned
	}
	return chatEvent
}

func streamEventKey(event fruntime.Event) string {
	if stepID := strings.TrimSpace(event.StepID); stepID != "" {
		return "step:" + stepID
	}
	if nodeID := strings.TrimSpace(event.NodeID); nodeID != "" {
		return "node:" + nodeID
	}
	return ""
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
		return ctrl.store.LoadHistory(defaultSessionID)
	}
	state := ctrl.GetLastState()
	if state == nil {
		return []HistoryMessage{}, nil
	}
	return convertMessages(state.Conversation(stateScope).Messages()), nil
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
