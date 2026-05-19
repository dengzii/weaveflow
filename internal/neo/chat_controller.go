package neo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"weaveflow"
	"weaveflow/core"
	"weaveflow/memory"
	"weaveflow/nodes"
	fruntime "weaveflow/runtime"
	wfstate "weaveflow/state"
	"weaveflow/tools"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

type ChatController struct {
	services  *core.Services
	config    *Config
	allTools  map[string]tools.Tool
	toolFlags map[string]bool
	baseDir   string
	store     *Store
	hub       *LiveHub

	mu            sync.RWMutex
	runner        *fruntime.GraphRunner
	runID         string
	runDir        string
	paused        bool
	resumable     bool
	lastRunStatus string
	cancelFn      context.CancelFunc
	lastState     wfstate.State
	graphCache    *weaveflow.Graph
	graphCfgKey   Config
}

type turnKind int

const (
	turnStart turnKind = iota
	turnResumeClarification
	turnResumeStopped
)

func NewChatController(services *core.Services, cfg *Config, toolFlags map[string]bool, baseDir string, store *Store, hub *LiveHub) *ChatController {
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
		hub:       hub,
	}
}

func newChatRunner(graph *weaveflow.Graph, graphID string, runDir string, sink fruntime.EventSink) *fruntime.GraphRunner {
	runner := weaveflow.NewGraphRunner(
		graph,
		fruntime.NewFileExecutionStore(filepath.Join(runDir, "execution")),
		fruntime.NewFileCheckpointStore(filepath.Join(runDir, "checkpoints")),
		wfstate.NewJSONStateCodec(wfstate.DefaultStateVersion),
		sink,
	)
	runner.GraphID = graphID
	runner.ArtifactStore = fruntime.NewFileArtifactStore(filepath.Join(runDir, "artifacts"))
	runner.ContractValidation = core.ContractValidationStrict
	return runner
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
	kind := turnStart
	if ctrl.paused && ctrl.runDir != "" && ctrl.runID != "" {
		kind = turnResumeClarification
	}
	if ctrl.cancelFn != nil && kind == turnStart {
		fmt.Fprintf(os.Stderr, "[cancel-diag] new turn preempted previous run runID=%q\n", ctrl.runID)
		ctrl.cancelFn()
		ctrl.cancelFn = nil
	}

	ctrl.executeTurnLocked(c, req, kind)
}

func (ctrl *ChatController) Resume(c *gin.Context) {
	ctrl.mu.Lock()
	if !ctrl.resumable || ctrl.runDir == "" || ctrl.runID == "" {
		ctrl.mu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "no resumable run"})
		return
	}
	if ctrl.cancelFn != nil {
		fmt.Fprintf(os.Stderr, "[cancel-diag] resume cancelled previous run runID=%q\n", ctrl.runID)
		ctrl.cancelFn()
		ctrl.cancelFn = nil
	}
	req := ChatRequest{Message: ""}
	ctrl.executeTurnLocked(c, req, turnResumeStopped)
}

// executeTurnLocked must be called with ctrl.mu held. It releases the lock
// internally before any blocking IO.
func (ctrl *ChatController) executeTurnLocked(c *gin.Context, req ChatRequest, kind turnKind) {
	resumeMode := kind != turnStart

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
		// Graph identity changed; any prior checkpointed run referenced old node IDs.
		if resumeMode {
			ctrl.mu.Unlock()
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "graph configuration changed; cannot resume previous run"})
			return
		}
	}
	graphJSON, _ := os.ReadFile(filepath.Join(ctrl.baseDir, "graph.json"))
	var graphMeta struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(graphJSON, &graphMeta)
	ctrl.hub.StartRun(graphJSON, "Neo Agent", graphMeta.ID)

	var (
		runDir        string
		previousRunID string
	)
	if resumeMode {
		runDir = ctrl.runDir
		previousRunID = ctrl.runID
	} else {
		runDir = filepath.Join(ctrl.baseDir, fmt.Sprintf("run_%d", time.Now().UnixMilli()))
	}
	ctrl.mu.Unlock()

	var history []llms.MessageContent
	if ctrl.store != nil && !resumeMode {
		var loadErr error
		history, loadErr = ctrl.store.LoadLLMMessagesWithOptions(defaultSessionID, PromptHistoryOptions{
			RecentTurns:     cfg.HistoryRecentTurns,
			SummaryMaxChars: cfg.HistorySummaryMaxChars,
		})
		if loadErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "load history failed: " + loadErr.Error()})
			return
		}
	}

	log, _ := zap.NewDevelopment()
	fruntime.SetLogger(log)

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "create run dir failed: " + err.Error()})
		return
	}
	channelSink := NewChannelEventSink()
	sinks := []fruntime.EventSink{
		channelSink,
		ctrl.hub,
		fruntime.NewLoggerEventSink(log),
		fruntime.NewFileEventSink(filepath.Join(runDir, "events")),
	}

	var turnWriter *TurnWriter
	if ctrl.store != nil && kind != turnResumeStopped {
		var storeErr error
		turnWriter, storeErr = ctrl.store.BeginTurn(defaultSessionID, req.Message)
		if storeErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "persist turn failed: " + storeErr.Error()})
			return
		}
		sinks = append(sinks, turnWriter)
	}

	combinedSink := fruntime.NewCombineEventSink(sinks...)

	runner := newChatRunner(graph, graphMeta.ID, runDir, combinedSink)

	baseCtx := core.WithServices(c.Request.Context(), services)
	ctx, cancel := context.WithCancel(baseCtx)
	if cfg.RequestTimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(baseCtx, time.Duration(cfg.RequestTimeoutSeconds)*time.Second)
	}

	ctrl.mu.Lock()
	ctrl.runner = runner
	ctrl.cancelFn = cancel
	ctrl.mu.Unlock()

	type runResult struct {
		run   fruntime.RunRecord
		state wfstate.State
		err   error
	}
	done := make(chan runResult, 1)

	var initialState wfstate.State
	var resumeInput wfstate.State
	switch kind {
	case turnStart:
		initialState = NewInitialState(req.Message, history)
		if len(graphJSON) > 0 {
			_ = os.WriteFile(filepath.Join(runDir, "graph.json"), graphJSON, 0o644)
		}
	case turnResumeClarification:
		resumeInput = wfstate.State{
			nodes.ClarificationStateKey: wfstate.State{
				nodes.ClarificationUserChoiceKey: req.Message,
			},
		}
	case turnResumeStopped:
		resumeInput = nil
	}
	startedAt := time.Now()
	if !resumeMode {
		_ = writeRunMetadata(runDir, RunMetadata{
			GraphID:       graphMeta.ID,
			GraphVersion:  fruntime.DefaultGraphVersion,
			Status:        string(fruntime.RunStatusPending),
			StartedAt:     startedAt,
			Request:       req,
			Config:        cfg,
			EnabledTools:  enabledToolNames(ctrl.toolFlags),
			InitialState:  initialState.CloneState(),
			GraphFile:     "graph.json",
			ExecutionRoot: "execution",
		})
	}
	go func() {
		defer channelSink.Close()
		defer ctrl.hub.Done()
		var (
			run    fruntime.RunRecord
			state  wfstate.State
			runErr error
		)
		if resumeMode {
			run, state, runErr = runner.Resume(ctx, previousRunID, resumeInput)
		} else {
			run, state, runErr = runner.Start(ctx, initialState)
		}
		runStatus := overallRunStatus(ctx, run, runErr)
		finishedAt := time.Now()
		_ = writeRunMetadata(runDir, RunMetadata{
			RunID:         run.RunID,
			GraphID:       nonEmpty(run.GraphID, graphMeta.ID),
			GraphVersion:  run.GraphVersion,
			Status:        runStatus,
			StartedAt:     startedAt,
			FinishedAt:    &finishedAt,
			Request:       req,
			Config:        cfg,
			EnabledTools:  enabledToolNames(ctrl.toolFlags),
			InitialState:  initialState.CloneState(),
			FinalState:    state,
			FinalAnswer:   finalAnswerFromState(state),
			Error:         errorString(runErr),
			GraphFile:     "graph.json",
			ExecutionRoot: "execution",
		})
		if turnWriter != nil {
			_ = turnWriter.AppendAssistantText(finalAnswerFromState(state))
			_ = turnWriter.Finalize(runStatus)
		}
		ctrl.recordTurnOutcome(run, state, runDir)
		done <- runResult{run: run, state: state, err: runErr}
	}()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	clientGone := c.Request.Context().Done()
	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()
	streamedReasoningByStep := make(map[string]string)
	streamedContentByStep := make(map[string]string)
	sentAssistantContent := false
	for {
		select {
		case <-clientGone:
			log.Warn("[cancel-diag] client gone, cancelling run",
				zap.String("runID", ctrl.runID),
				zap.Error(c.Request.Context().Err()),
			)
			cancel()
			ctrl.mu.Lock()
			ctrl.cancelFn = nil
			ctrl.mu.Unlock()
			return
		case <-heartbeatTicker.C:
			writeSSEHeartbeat(c)
		case event, ok := <-channelSink.Events():
			if !ok {
				res := <-done
				paused := res.run.Status == fruntime.RunStatusPaused

				if !paused {
					if answer := finalAnswerFromState(res.state); !sentAssistantContent && answer != "" {
						writeSSE(c, &ChatEvent{
							Type:    ChatEventTypeGenerating,
							Content: answer,
						})
					}
				}

				if res.err != nil {
					switch {
					case errors.Is(res.err, context.DeadlineExceeded):
						writeSSE(c, &ChatEvent{
							Type:    ChatEventTypeError,
							Content: "执行超时: " + res.err.Error(),
						})
					case !errors.Is(res.err, context.Canceled):
						writeSSE(c, &ChatEvent{
							Type:    ChatEventTypeError,
							Content: res.err.Error(),
						})
					}
				}
				return
			}

			chatEvent := attachEventIdentity(event, TranslateEvent(event))
			rememberStreamedReasoningText(event, chatEvent, streamedReasoningByStep)
			rememberStreamedContentText(event, chatEvent, streamedContentByStep)
			chatEvent = syncReasoningSummary(event, chatEvent, streamedReasoningByStep)
			chatEvent = syncContentSummary(event, chatEvent, streamedContentByStep)
			if chatEvent != nil {
				if chatEvent.Type == ChatEventTypeGenerating && strings.TrimSpace(chatEvent.Content) != "" {
					sentAssistantContent = true
				}
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

func rememberStreamedContentText(event fruntime.Event, chatEvent *ChatEvent, streamedContentByStep map[string]string) {
	if event.Type != fruntime.EventLLMContentChunk || chatEvent == nil || chatEvent.Type != ChatEventTypeGenerating {
		return
	}
	key := streamEventKey(event)
	if key == "" || chatEvent.Content == "" {
		return
	}
	streamedContentByStep[key] += chatEvent.Content
}

func syncContentSummary(event fruntime.Event, chatEvent *ChatEvent, streamedContentByStep map[string]string) *ChatEvent {
	if event.Type != fruntime.EventLLMContent || chatEvent == nil || chatEvent.Type != ChatEventTypeGenerating {
		return chatEvent
	}

	key := streamEventKey(event)
	if key == "" {
		return chatEvent
	}

	streamed := streamedContentByStep[key]
	delete(streamedContentByStep, key)
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

func finalAnswerFromState(state wfstate.State) string {
	if state == nil {
		return ""
	}
	if answer := strings.TrimSpace(state.Conversation(stateScope).FinalAnswer()); answer != "" {
		return answer
	}
	finalState := state.Get(wfstate.StateKeyFinal)
	if finalState == nil {
		return ""
	}
	answer, _ := finalState["answer"].(string)
	return strings.TrimSpace(answer)
}

func (ctrl *ChatController) effectiveServices() *core.Services {
	enabledTools := make(map[string]tools.Tool, len(ctrl.allTools))
	for name, tool := range ctrl.allTools {
		if ctrl.toolFlags[name] {
			enabledTools[name] = tool
		}
	}
	return &core.Services{
		Model:  ctrl.services.Model,
		Memory: ctrl.services.Memory,
		Tools:  enabledTools,
	}
}

func (ctrl *ChatController) GetLastState() wfstate.State {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ctrl.lastState
}

func (ctrl *ChatController) GetHistory() ([]HistoryMessage, error) {
	if ctrl.store != nil {
		history, err := ctrl.store.LoadHistory(defaultSessionID)
		if err != nil {
			return nil, err
		}
		return sanitizeHistoryMessages(history), nil
	}
	state := ctrl.GetLastState()
	if state == nil {
		return []HistoryMessage{}, nil
	}
	return sanitizeHistoryMessages(convertMessages(state.Conversation(stateScope).Messages())), nil
}

func (ctrl *ChatController) ClearHistory() error {
	if ctrl.store != nil {
		if err := ctrl.store.ClearHistory(defaultSessionID); err != nil {
			return err
		}
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	ctrl.lastState = nil
	ctrl.runID = ""
	ctrl.runDir = ""
	ctrl.paused = false
	ctrl.resumable = false
	ctrl.lastRunStatus = ""
	return nil
}

func (ctrl *ChatController) GetMemory() ([]memory.Entry, error) {
	if ctrl.services == nil || ctrl.services.Memory == nil {
		return []memory.Entry{}, nil
	}
	return ctrl.services.Memory.Load(nil)
}

func (ctrl *ChatController) ClearMemory() error {
	if ctrl.services == nil || ctrl.services.Memory == nil {
		return nil
	}
	return ctrl.services.Memory.Delete()
}

func writeSSE(c *gin.Context, event *ChatEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

func writeSSEHeartbeat(c *gin.Context) {
	_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
	c.Writer.Flush()
}

// runStatusString determines the run outcome label for history persistence.
func runStatusString(ctx context.Context, runErr error) string {
	if runErr == nil {
		return "completed"
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "stopped"
	}
	return "failed"
}

// overallRunStatus reports the run outcome including paused state.
func overallRunStatus(ctx context.Context, run fruntime.RunRecord, runErr error) string {
	if run.Status == fruntime.RunStatusPaused {
		return string(fruntime.RunStatusPaused)
	}
	return runStatusString(ctx, runErr)
}

// recordTurnOutcome persists per-turn state on the controller after a run finishes,
// so subsequent requests can decide whether to resume or start fresh — even when the
// HTTP handler returned early due to client disconnect.
func (ctrl *ChatController) recordTurnOutcome(run fruntime.RunRecord, state wfstate.State, runDir string) {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	ctrl.lastState = state
	if run.RunID != "" {
		ctrl.runID = run.RunID
	}
	ctrl.cancelFn = nil
	ctrl.lastRunStatus = string(run.Status)
	switch run.Status {
	case fruntime.RunStatusPaused:
		ctrl.paused = true
		ctrl.resumable = false
		ctrl.runDir = runDir
	case fruntime.RunStatusCompleted:
		ctrl.paused = false
		ctrl.resumable = false
		ctrl.runDir = ""
	default:
		// running (interrupted), canceled, failed — resumable if a checkpoint exists.
		ctrl.paused = false
		ctrl.resumable = strings.TrimSpace(run.LastCheckpointID) != ""
		if ctrl.resumable {
			ctrl.runDir = runDir
		} else {
			ctrl.runDir = ""
		}
	}
}

// ResumableStatus describes whether the controller has a run that can be resumed.
type ResumableStatus struct {
	Paused    bool   `json:"paused"`
	Resumable bool   `json:"resumable"`
	RunID     string `json:"run_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (ctrl *ChatController) ResumableStatus() ResumableStatus {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ResumableStatus{
		Paused:    ctrl.paused,
		Resumable: ctrl.resumable,
		RunID:     ctrl.runID,
		Status:    ctrl.lastRunStatus,
	}
}

func (ctrl *ChatController) Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ctrl.ResumableStatus()})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
