package runtime

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

type EventAnalyzer struct {
	mu     sync.RWMutex
	events map[string][]Event
}

type EventRunAnalysis struct {
	RunID              string                         `json:"run_id"`
	Status             RunStatus                      `json:"status,omitempty"`
	EntryNodeID        string                         `json:"entry_node_id,omitempty"`
	CurrentNodeID      string                         `json:"current_node_id,omitempty"`
	StartedAt          time.Time                      `json:"started_at,omitempty"`
	FinishedAt         time.Time                      `json:"finished_at,omitempty"`
	LastEventAt        time.Time                      `json:"last_event_at,omitempty"`
	Duration           time.Duration                  `json:"duration"`
	EventCount         int                            `json:"event_count"`
	EventCounts        map[EventType]int              `json:"event_counts,omitempty"`
	Timeline           []EventTimelineItem            `json:"timeline,omitempty"`
	Nodes              []EventNodeUsage               `json:"nodes,omitempty"`
	LLM                EventLLMUsageStats             `json:"llm"`
	Tools              EventToolUsage                 `json:"tools"`
	Subgraphs          EventSubgraphUsage             `json:"subgraphs"`
	State              EventStateUsage                `json:"state"`
	Checkpoints        EventCheckpointUsage           `json:"checkpoints"`
	Artifacts          EventArtifactUsage             `json:"artifacts"`
	Warnings           []EventWarningRecord           `json:"warnings,omitempty"`
	Errors             []EventErrorRecord             `json:"errors,omitempty"`
	ContractViolations []EventContractViolationRecord `json:"contract_violations,omitempty"`
}

type EventTimelineItem struct {
	Index     int             `json:"index"`
	EventID   string          `json:"event_id,omitempty"`
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
	StepID    string          `json:"step_id,omitempty"`
	NodeID    string          `json:"node_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type EventNodeUsage struct {
	NodeID                 string             `json:"node_id"`
	NodeName               string             `json:"node_name,omitempty"`
	StepIDs                []string           `json:"step_ids,omitempty"`
	Started                int                `json:"started"`
	Finished               int                `json:"finished"`
	Failed                 int                `json:"failed"`
	RetryCount             int                `json:"retry_count"`
	AttemptCount           int                `json:"attempt_count"`
	Duration               time.Duration      `json:"duration"`
	FirstSeenAt            time.Time          `json:"first_seen_at,omitempty"`
	LastSeenAt             time.Time          `json:"last_seen_at,omitempty"`
	LLM                    EventLLMUsageStats `json:"llm"`
	Tools                  EventToolUsage     `json:"tools"`
	StateChangeCount       int                `json:"state_change_count"`
	CheckpointCount        int                `json:"checkpoint_count"`
	ArtifactCount          int                `json:"artifact_count"`
	WarningCount           int                `json:"warning_count"`
	ContractViolationCount int                `json:"contract_violation_count"`
	LastError              string             `json:"last_error,omitempty"`
}

type EventLLMUsageStats struct {
	Calls              int                           `json:"calls"`
	PromptTokens       int                           `json:"prompt_tokens"`
	CompletionTokens   int                           `json:"completion_tokens"`
	TotalTokens        int                           `json:"total_tokens"`
	ReasoningTokens    int                           `json:"reasoning_tokens"`
	PromptCachedTokens int                           `json:"prompt_cached_tokens"`
	ReasoningChars     int                           `json:"reasoning_chars"`
	ContentChars       int                           `json:"content_chars"`
	ByModel            map[string]EventLLMModelUsage `json:"by_model,omitempty"`
}

type EventLLMModelUsage struct {
	Calls              int `json:"calls"`
	PromptTokens       int `json:"prompt_tokens"`
	CompletionTokens   int `json:"completion_tokens"`
	TotalTokens        int `json:"total_tokens"`
	ReasoningTokens    int `json:"reasoning_tokens"`
	PromptCachedTokens int `json:"prompt_cached_tokens"`
}

type EventToolUsage struct {
	Called   int                           `json:"called"`
	Returned int                           `json:"returned"`
	Failed   int                           `json:"failed"`
	Duration time.Duration                 `json:"duration"`
	ByName   map[string]EventToolNameUsage `json:"by_name,omitempty"`
	Calls    []EventToolCallRecord         `json:"calls,omitempty"`
}

type EventToolNameUsage struct {
	Called   int           `json:"called"`
	Returned int           `json:"returned"`
	Failed   int           `json:"failed"`
	Duration time.Duration `json:"duration"`
}

type EventToolCallRecord struct {
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
	NodeID     string        `json:"node_id,omitempty"`
	StepID     string        `json:"step_id,omitempty"`
	Status     string        `json:"status,omitempty"`
	StartedAt  time.Time     `json:"started_at,omitempty"`
	FinishedAt time.Time     `json:"finished_at,omitempty"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
}

type EventSubgraphUsage struct {
	Started  int                              `json:"started"`
	Finished int                              `json:"finished"`
	Failed   int                              `json:"failed"`
	Duration time.Duration                    `json:"duration"`
	ByRef    map[string]EventSubgraphRefUsage `json:"by_ref,omitempty"`
	Calls    []EventSubgraphCallRecord        `json:"calls,omitempty"`
}

type EventSubgraphRefUsage struct {
	Started  int           `json:"started"`
	Finished int           `json:"finished"`
	Failed   int           `json:"failed"`
	Duration time.Duration `json:"duration"`
}

type EventSubgraphCallRecord struct {
	GraphRef   string        `json:"graph_ref,omitempty"`
	NodeID     string        `json:"node_id,omitempty"`
	StepID     string        `json:"step_id,omitempty"`
	Status     string        `json:"status,omitempty"`
	StartedAt  time.Time     `json:"started_at,omitempty"`
	FinishedAt time.Time     `json:"finished_at,omitempty"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
}

type EventStateUsage struct {
	ChangeEvents int `json:"change_events"`
	ChangeCount  int `json:"change_count"`
}

type EventCheckpointUsage struct {
	Created int                     `json:"created"`
	Items   []EventCheckpointRecord `json:"items,omitempty"`
}

type EventCheckpointRecord struct {
	CheckpointID string          `json:"checkpoint_id,omitempty"`
	Stage        CheckpointStage `json:"stage,omitempty"`
	NodeID       string          `json:"node_id,omitempty"`
	StepID       string          `json:"step_id,omitempty"`
	CreatedAt    time.Time       `json:"created_at,omitempty"`
}

type EventArtifactUsage struct {
	Created int                   `json:"created"`
	Items   []EventArtifactRecord `json:"items,omitempty"`
}

type EventArtifactRecord struct {
	ArtifactID string    `json:"artifact_id,omitempty"`
	Type       string    `json:"type,omitempty"`
	MIMEType   string    `json:"mime_type,omitempty"`
	NodeID     string    `json:"node_id,omitempty"`
	StepID     string    `json:"step_id,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

type EventWarningRecord struct {
	NodeID    string          `json:"node_id,omitempty"`
	StepID    string          `json:"step_id,omitempty"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type EventErrorRecord struct {
	Type      EventType `json:"type"`
	NodeID    string    `json:"node_id,omitempty"`
	StepID    string    `json:"step_id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type EventContractViolationRecord struct {
	NodeID    string          `json:"node_id,omitempty"`
	StepID    string          `json:"step_id,omitempty"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
	Count     int             `json:"count"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func NewEventAnalyzer() *EventAnalyzer {
	return &EventAnalyzer{events: make(map[string][]Event)}
}

func (a *EventAnalyzer) Publish(_ context.Context, event Event) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.events == nil {
		a.events = make(map[string][]Event)
	}
	a.events[event.RunID] = append(a.events[event.RunID], cloneEvent(event))
	return nil
}

func (a *EventAnalyzer) PublishBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := a.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (a *EventAnalyzer) ListEvents(runID string) ([]Event, error) {
	if a == nil {
		return []Event{}, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneEvents(a.events[runID]), nil
}

func (a *EventAnalyzer) RunIDs() []string {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	runIDs := make([]string, 0, len(a.events))
	for runID := range a.events {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	return runIDs
}

func (a *EventAnalyzer) Reset() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = make(map[string][]Event)
}

func (a *EventAnalyzer) AnalyzeRun(runID string) (EventRunAnalysis, error) {
	events, err := a.ListEvents(runID)
	if err != nil {
		return EventRunAnalysis{}, err
	}
	return AnalyzeRunEvents(runID, events), nil
}

func (a *EventAnalyzer) AnalyzeRuns() ([]EventRunAnalysis, error) {
	runIDs := a.RunIDs()
	items := make([]EventRunAnalysis, 0, len(runIDs))
	for _, runID := range runIDs {
		analysis, err := a.AnalyzeRun(runID)
		if err != nil {
			return nil, err
		}
		items = append(items, analysis)
	}
	return items, nil
}

func (a *EventAnalyzer) ExportRunJSON(runID string) ([]byte, error) {
	analysis, err := a.AnalyzeRun(runID)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(analysis, "", "  ")
}

func (a *EventAnalyzer) ExportRunsJSON() ([]byte, error) {
	analyses, err := a.AnalyzeRuns()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(analyses, "", "  ")
}

func AnalyzeRunEvents(runID string, events []Event) EventRunAnalysis {
	events = cloneEvents(events)
	stableSortEvents(events)
	analysis := EventRunAnalysis{
		RunID:       strings.TrimSpace(runID),
		EventCount:  len(events),
		EventCounts: make(map[EventType]int),
	}
	if analysis.RunID == "" {
		analysis.RunID = firstEventRunID(events)
	}

	builder := newEventAnalysisBuilder()
	for index, event := range events {
		if analysis.RunID == "" {
			analysis.RunID = event.RunID
		}
		analysis.EventCounts[event.Type]++
		analysis.Timeline = append(analysis.Timeline, EventTimelineItem{
			Index:     index,
			EventID:   event.ID,
			Type:      event.Type,
			Timestamp: event.Timestamp,
			StepID:    event.StepID,
			NodeID:    event.NodeID,
			Payload:   cloneRawMessage(event.Payload),
		})
		applyRunLifecycle(&analysis, event)
		builder.applyEvent(&analysis, event)
	}

	if len(analysis.EventCounts) == 0 {
		analysis.EventCounts = nil
	}
	analysis.Nodes = builder.collectNodes()
	analysis.Tools.Calls = builder.collectToolCalls()
	analysis.Subgraphs.Calls = builder.collectSubgraphCalls()
	finalizeRunTiming(&analysis)
	return analysis
}

type eventAnalysisBuilder struct {
	nodes           map[string]*EventNodeUsage
	nodeStepIDs     map[string]map[string]struct{}
	activeNodeSteps map[string]time.Time
	toolCalls       []*EventToolCallRecord
	activeTools     map[string][]*EventToolCallRecord
	subgraphCalls   []*EventSubgraphCallRecord
	activeSubgraphs map[string][]*EventSubgraphCallRecord
}

func newEventAnalysisBuilder() *eventAnalysisBuilder {
	return &eventAnalysisBuilder{
		nodes:           make(map[string]*EventNodeUsage),
		nodeStepIDs:     make(map[string]map[string]struct{}),
		activeNodeSteps: make(map[string]time.Time),
		activeTools:     make(map[string][]*EventToolCallRecord),
		activeSubgraphs: make(map[string][]*EventSubgraphCallRecord),
	}
}

func (b *eventAnalysisBuilder) applyEvent(analysis *EventRunAnalysis, event Event) {
	node := b.node(event.NodeID)
	if node != nil {
		b.touchNode(node, event)
	}
	switch event.Type {
	case EventNodeStarted:
		b.applyNodeStarted(node, event)
	case EventNodeFinished:
		b.applyNodeFinished(node, event)
	case EventNodeFailed:
		b.applyNodeFailed(analysis, node, event)
	case EventNodeRetry:
		b.applyNodeRetry(node, event)
	case EventLLMCall, EventLLMUsage:
		usage := llmUsageFromPayload(event.Payload)
		mergeLLMUsage(&analysis.LLM, usage)
		if node != nil {
			mergeLLMUsage(&node.LLM, usage)
		}
	case EventLLMReasoning, EventLLMReasoningChunk:
		chars := len(payloadString(event.Payload, "text"))
		analysis.LLM.ReasoningChars += chars
		if node != nil {
			node.LLM.ReasoningChars += chars
		}
	case EventLLMContent, EventLLMContentChunk:
		chars := len(payloadString(event.Payload, "text"))
		analysis.LLM.ContentChars += chars
		if node != nil {
			node.LLM.ContentChars += chars
		}
	case EventToolCalled, EventToolStarted:
		b.applyToolCalled(analysis, node, event)
	case EventToolReturned:
		b.applyToolFinished(analysis, node, event, "returned")
	case EventToolFailed:
		b.applyToolFinished(analysis, node, event, "failed")
		b.appendError(analysis, event)
	case EventSubgraphStarted:
		b.applySubgraphStarted(analysis, event)
	case EventSubgraphFinished:
		b.applySubgraphFinished(analysis, event, "finished")
	case EventSubgraphFailed:
		b.applySubgraphFinished(analysis, event, "failed")
		b.appendError(analysis, event)
	case EventStateChanged:
		count := payloadArrayCount(event.Payload, "changes")
		analysis.State.ChangeEvents++
		analysis.State.ChangeCount += count
		if node != nil {
			node.StateChangeCount += count
		}
	case EventCheckpointCreated:
		analysis.Checkpoints.Created++
		analysis.Checkpoints.Items = append(analysis.Checkpoints.Items, checkpointRecordFromEvent(event))
		if node != nil {
			node.CheckpointCount++
		}
	case EventArtifactCreated:
		analysis.Artifacts.Created++
		analysis.Artifacts.Items = append(analysis.Artifacts.Items, artifactRecordFromEvent(event))
		if node != nil {
			node.ArtifactCount++
		}
	case EventWarning:
		analysis.Warnings = append(analysis.Warnings, EventWarningRecord{
			NodeID:    event.NodeID,
			StepID:    event.StepID,
			Timestamp: event.Timestamp,
			Payload:   cloneRawMessage(event.Payload),
		})
		if node != nil {
			node.WarningCount++
		}
	case EventContractViolation:
		count := payloadArrayCount(event.Payload, "violations")
		if count <= 0 {
			count = 1
		}
		analysis.ContractViolations = append(analysis.ContractViolations, EventContractViolationRecord{
			NodeID:    event.NodeID,
			StepID:    event.StepID,
			Timestamp: event.Timestamp,
			Count:     count,
			Payload:   cloneRawMessage(event.Payload),
		})
		if node != nil {
			node.ContractViolationCount += count
		}
	case EventRunFailed, EventRunCanceled:
		b.appendError(analysis, event)
	}
}

func (b *eventAnalysisBuilder) applyNodeStarted(node *EventNodeUsage, event Event) {
	if node == nil {
		return
	}
	node.Started++
	if node.AttemptCount == 0 {
		node.AttemptCount = 1
	}
	if name := payloadString(event.Payload, "node_name"); name != "" {
		node.NodeName = name
	}
	if event.StepID != "" {
		b.activeNodeSteps[event.StepID] = event.Timestamp
		b.addNodeStepID(node.NodeID, event.StepID)
	}
}

func (b *eventAnalysisBuilder) applyNodeFinished(node *EventNodeUsage, event Event) {
	if node == nil {
		return
	}
	node.Finished++
	if attempt := payloadInt(event.Payload, "attempt"); attempt > node.AttemptCount {
		node.AttemptCount = attempt
	}
	b.finishNodeStep(node, event)
}

func (b *eventAnalysisBuilder) applyNodeFailed(analysis *EventRunAnalysis, node *EventNodeUsage, event Event) {
	if node != nil {
		node.Failed++
		node.LastError = firstNonEmpty(payloadString(event.Payload, "error"), payloadString(event.Payload, "error_message"))
		if attempt := payloadInt(event.Payload, "attempt"); attempt > node.AttemptCount {
			node.AttemptCount = attempt
		}
		b.finishNodeStep(node, event)
	}
	b.appendError(analysis, event)
}

func (b *eventAnalysisBuilder) applyNodeRetry(node *EventNodeUsage, event Event) {
	if node == nil {
		return
	}
	node.RetryCount++
	if node.AttemptCount <= node.RetryCount {
		node.AttemptCount = node.RetryCount + 1
	}
}

func (b *eventAnalysisBuilder) finishNodeStep(node *EventNodeUsage, event Event) {
	if event.StepID == "" {
		return
	}
	startedAt, ok := b.activeNodeSteps[event.StepID]
	if ok {
		node.Duration += nonNegativeDuration(startedAt, event.Timestamp)
		delete(b.activeNodeSteps, event.StepID)
	}
	b.addNodeStepID(node.NodeID, event.StepID)
}

func (b *eventAnalysisBuilder) applyToolCalled(analysis *EventRunAnalysis, node *EventNodeUsage, event Event) {
	items := toolItemsFromPayload(event.Payload)
	if len(items) == 0 {
		items = []eventToolItem{{Name: payloadString(event.Payload, "name"), ToolCallID: payloadString(event.Payload, "tool_call_id")}}
	}
	for _, item := range items {
		record := &EventToolCallRecord{
			ToolCallID: item.ToolCallID,
			Name:       item.Name,
			NodeID:     event.NodeID,
			StepID:     event.StepID,
			Status:     "called",
			StartedAt:  event.Timestamp,
		}
		b.toolCalls = append(b.toolCalls, record)
		b.activeTools[toolActiveKey(event, item)] = append(b.activeTools[toolActiveKey(event, item)], record)
		incrementToolCalled(&analysis.Tools, item.Name)
		if node != nil {
			incrementToolCalled(&node.Tools, item.Name)
		}
	}
}

func (b *eventAnalysisBuilder) applyToolFinished(analysis *EventRunAnalysis, node *EventNodeUsage, event Event, status string) {
	item := eventToolItem{Name: payloadString(event.Payload, "name"), ToolCallID: payloadString(event.Payload, "tool_call_id")}
	record := b.popActiveTool(event, item)
	if record == nil {
		record = &EventToolCallRecord{
			ToolCallID: item.ToolCallID,
			Name:       item.Name,
			NodeID:     event.NodeID,
			StepID:     event.StepID,
			StartedAt:  event.Timestamp,
		}
		b.toolCalls = append(b.toolCalls, record)
	}
	record.Status = status
	record.FinishedAt = event.Timestamp
	record.Duration = nonNegativeDuration(record.StartedAt, event.Timestamp)
	record.Error = payloadString(event.Payload, "error")
	mergeToolFinished(&analysis.Tools, record, status)
	if node != nil {
		mergeToolFinished(&node.Tools, record, status)
	}
}

func (b *eventAnalysisBuilder) applySubgraphStarted(analysis *EventRunAnalysis, event Event) {
	ref := payloadString(event.Payload, "graph_ref")
	record := &EventSubgraphCallRecord{
		GraphRef:  ref,
		NodeID:    event.NodeID,
		StepID:    event.StepID,
		Status:    "started",
		StartedAt: event.Timestamp,
	}
	b.subgraphCalls = append(b.subgraphCalls, record)
	b.activeSubgraphs[subgraphActiveKey(event, ref)] = append(b.activeSubgraphs[subgraphActiveKey(event, ref)], record)
	analysis.Subgraphs.Started++
	incrementSubgraphRef(&analysis.Subgraphs, ref, func(item *EventSubgraphRefUsage) { item.Started++ })
}

func (b *eventAnalysisBuilder) applySubgraphFinished(analysis *EventRunAnalysis, event Event, status string) {
	ref := payloadString(event.Payload, "graph_ref")
	record := b.popActiveSubgraph(event, ref)
	if record == nil {
		record = &EventSubgraphCallRecord{
			GraphRef:  ref,
			NodeID:    event.NodeID,
			StepID:    event.StepID,
			StartedAt: event.Timestamp,
		}
		b.subgraphCalls = append(b.subgraphCalls, record)
	}
	record.Status = status
	record.FinishedAt = event.Timestamp
	record.Duration = nonNegativeDuration(record.StartedAt, event.Timestamp)
	record.Error = payloadString(event.Payload, "error")
	if status == "failed" {
		analysis.Subgraphs.Failed++
		incrementSubgraphRef(&analysis.Subgraphs, ref, func(item *EventSubgraphRefUsage) { item.Failed++ })
	} else {
		analysis.Subgraphs.Finished++
		incrementSubgraphRef(&analysis.Subgraphs, ref, func(item *EventSubgraphRefUsage) { item.Finished++ })
	}
	analysis.Subgraphs.Duration += record.Duration
	incrementSubgraphRef(&analysis.Subgraphs, ref, func(item *EventSubgraphRefUsage) { item.Duration += record.Duration })
}

func (b *eventAnalysisBuilder) popActiveTool(event Event, item eventToolItem) *EventToolCallRecord {
	keys := []string{
		toolActiveKey(event, item),
		toolActiveKey(event, eventToolItem{Name: item.Name}),
		toolActiveKey(event, eventToolItem{ToolCallID: item.ToolCallID}),
	}
	for _, key := range keys {
		records := b.activeTools[key]
		if len(records) == 0 {
			continue
		}
		record := records[0]
		if len(records) == 1 {
			delete(b.activeTools, key)
		} else {
			b.activeTools[key] = records[1:]
		}
		return record
	}
	return nil
}

func (b *eventAnalysisBuilder) popActiveSubgraph(event Event, ref string) *EventSubgraphCallRecord {
	key := subgraphActiveKey(event, ref)
	records := b.activeSubgraphs[key]
	if len(records) == 0 {
		return nil
	}
	record := records[0]
	if len(records) == 1 {
		delete(b.activeSubgraphs, key)
	} else {
		b.activeSubgraphs[key] = records[1:]
	}
	return record
}

func (b *eventAnalysisBuilder) appendError(analysis *EventRunAnalysis, event Event) {
	analysis.Errors = append(analysis.Errors, EventErrorRecord{
		Type:      event.Type,
		NodeID:    event.NodeID,
		StepID:    event.StepID,
		Timestamp: event.Timestamp,
		Code:      firstNonEmpty(payloadString(event.Payload, "error_code"), payloadString(event.Payload, "code")),
		Message:   firstNonEmpty(payloadString(event.Payload, "error_message"), payloadString(event.Payload, "error")),
	})
}

func (b *eventAnalysisBuilder) node(nodeID string) *EventNodeUsage {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}
	node := b.nodes[nodeID]
	if node == nil {
		node = &EventNodeUsage{NodeID: nodeID}
		b.nodes[nodeID] = node
	}
	return node
}

func (b *eventAnalysisBuilder) touchNode(node *EventNodeUsage, event Event) {
	if node.FirstSeenAt.IsZero() || event.Timestamp.Before(node.FirstSeenAt) {
		node.FirstSeenAt = event.Timestamp
	}
	if node.LastSeenAt.IsZero() || node.LastSeenAt.Before(event.Timestamp) {
		node.LastSeenAt = event.Timestamp
	}
	if event.StepID != "" {
		b.addNodeStepID(node.NodeID, event.StepID)
	}
}

func (b *eventAnalysisBuilder) addNodeStepID(nodeID, stepID string) {
	if nodeID == "" || stepID == "" {
		return
	}
	set := b.nodeStepIDs[nodeID]
	if set == nil {
		set = make(map[string]struct{})
		b.nodeStepIDs[nodeID] = set
	}
	set[stepID] = struct{}{}
}

func (b *eventAnalysisBuilder) collectNodes() []EventNodeUsage {
	nodes := make([]EventNodeUsage, 0, len(b.nodes))
	for nodeID, node := range b.nodes {
		if steps := b.nodeStepIDs[nodeID]; len(steps) > 0 {
			node.StepIDs = make([]string, 0, len(steps))
			for stepID := range steps {
				node.StepIDs = append(node.StepIDs, stepID)
			}
			sort.Strings(node.StepIDs)
		}
		nodes = append(nodes, *node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].FirstSeenAt.Equal(nodes[j].FirstSeenAt) {
			return nodes[i].NodeID < nodes[j].NodeID
		}
		return nodes[i].FirstSeenAt.Before(nodes[j].FirstSeenAt)
	})
	return nodes
}

func (b *eventAnalysisBuilder) collectToolCalls() []EventToolCallRecord {
	items := make([]EventToolCallRecord, 0, len(b.toolCalls))
	for _, item := range b.toolCalls {
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].Name < items[j].Name
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items
}

func (b *eventAnalysisBuilder) collectSubgraphCalls() []EventSubgraphCallRecord {
	items := make([]EventSubgraphCallRecord, 0, len(b.subgraphCalls))
	for _, item := range b.subgraphCalls {
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].GraphRef < items[j].GraphRef
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items
}

func applyRunLifecycle(analysis *EventRunAnalysis, event Event) {
	if event.Timestamp.IsZero() {
		return
	}
	if analysis.StartedAt.IsZero() || event.Timestamp.Before(analysis.StartedAt) {
		analysis.StartedAt = event.Timestamp
	}
	if analysis.LastEventAt.IsZero() || analysis.LastEventAt.Before(event.Timestamp) {
		analysis.LastEventAt = event.Timestamp
	}
	switch event.Type {
	case EventRunCreated:
		if entry := payloadString(event.Payload, "entry_node_id"); entry != "" {
			analysis.EntryNodeID = entry
			analysis.CurrentNodeID = entry
		}
		if analysis.Status == "" {
			analysis.Status = RunStatusPending
		}
	case EventRunStarted, EventRunResumed:
		analysis.Status = RunStatusRunning
		if nodeID := payloadString(event.Payload, "node_id"); nodeID != "" {
			analysis.CurrentNodeID = nodeID
		}
	case EventRunPaused:
		analysis.Status = RunStatusPaused
		analysis.FinishedAt = event.Timestamp
	case EventRunFinished:
		analysis.Status = RunStatusCompleted
		analysis.FinishedAt = event.Timestamp
	case EventRunFailed:
		analysis.Status = RunStatusFailed
		analysis.FinishedAt = event.Timestamp
	case EventRunCanceled:
		analysis.Status = RunStatusCanceled
		analysis.FinishedAt = event.Timestamp
	case EventNodeStarted:
		if event.NodeID != "" {
			analysis.CurrentNodeID = event.NodeID
		}
	}
}

func finalizeRunTiming(analysis *EventRunAnalysis) {
	if analysis.Status == "" && analysis.EventCount > 0 {
		analysis.Status = RunStatusRunning
	}
	end := analysis.FinishedAt
	if end.IsZero() {
		end = analysis.LastEventAt
	}
	analysis.Duration = nonNegativeDuration(analysis.StartedAt, end)
}

func llmUsageFromPayload(payload json.RawMessage) EventLLMUsageStats {
	calls := payloadInt(payload, "calls")
	if calls <= 0 {
		calls = 1
	}
	usage := EventLLMUsageStats{
		Calls:              calls,
		PromptTokens:       payloadInt(payload, "prompt_tokens"),
		CompletionTokens:   payloadInt(payload, "completion_tokens"),
		TotalTokens:        payloadInt(payload, "total_tokens"),
		ReasoningTokens:    payloadInt(payload, "reasoning_tokens"),
		PromptCachedTokens: payloadInt(payload, "prompt_cached_tokens"),
	}
	model := payloadString(payload, "model")
	if model != "" {
		usage.ByModel = map[string]EventLLMModelUsage{
			model: {
				Calls:              usage.Calls,
				PromptTokens:       usage.PromptTokens,
				CompletionTokens:   usage.CompletionTokens,
				TotalTokens:        usage.TotalTokens,
				ReasoningTokens:    usage.ReasoningTokens,
				PromptCachedTokens: usage.PromptCachedTokens,
			},
		}
	}
	return usage
}

func mergeLLMUsage(dst *EventLLMUsageStats, src EventLLMUsageStats) {
	dst.Calls += src.Calls
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
	dst.ReasoningTokens += src.ReasoningTokens
	dst.PromptCachedTokens += src.PromptCachedTokens
	dst.ReasoningChars += src.ReasoningChars
	dst.ContentChars += src.ContentChars
	if len(src.ByModel) == 0 {
		return
	}
	if dst.ByModel == nil {
		dst.ByModel = make(map[string]EventLLMModelUsage)
	}
	for model, usage := range src.ByModel {
		current := dst.ByModel[model]
		current.Calls += usage.Calls
		current.PromptTokens += usage.PromptTokens
		current.CompletionTokens += usage.CompletionTokens
		current.TotalTokens += usage.TotalTokens
		current.ReasoningTokens += usage.ReasoningTokens
		current.PromptCachedTokens += usage.PromptCachedTokens
		dst.ByModel[model] = current
	}
}

func incrementToolCalled(usage *EventToolUsage, name string) {
	usage.Called++
	incrementToolName(usage, name, func(item *EventToolNameUsage) { item.Called++ })
}

func mergeToolFinished(usage *EventToolUsage, record *EventToolCallRecord, status string) {
	if record == nil {
		return
	}
	switch status {
	case "failed":
		usage.Failed++
		incrementToolName(usage, record.Name, func(item *EventToolNameUsage) { item.Failed++ })
	default:
		usage.Returned++
		incrementToolName(usage, record.Name, func(item *EventToolNameUsage) { item.Returned++ })
	}
	usage.Duration += record.Duration
	incrementToolName(usage, record.Name, func(item *EventToolNameUsage) { item.Duration += record.Duration })
}

func incrementToolName(usage *EventToolUsage, name string, apply func(*EventToolNameUsage)) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if usage.ByName == nil {
		usage.ByName = make(map[string]EventToolNameUsage)
	}
	item := usage.ByName[name]
	apply(&item)
	usage.ByName[name] = item
}

func incrementSubgraphRef(usage *EventSubgraphUsage, ref string, apply func(*EventSubgraphRefUsage)) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return
	}
	if usage.ByRef == nil {
		usage.ByRef = make(map[string]EventSubgraphRefUsage)
	}
	item := usage.ByRef[ref]
	apply(&item)
	usage.ByRef[ref] = item
}

func checkpointRecordFromEvent(event Event) EventCheckpointRecord {
	return EventCheckpointRecord{
		CheckpointID: payloadString(event.Payload, "checkpoint_id"),
		Stage:        CheckpointStage(payloadString(event.Payload, "stage")),
		NodeID:       event.NodeID,
		StepID:       event.StepID,
		CreatedAt:    event.Timestamp,
	}
}

func artifactRecordFromEvent(event Event) EventArtifactRecord {
	return EventArtifactRecord{
		ArtifactID: firstNonEmpty(payloadString(event.Payload, "artifact_id"), payloadString(event.Payload, "id")),
		Type:       payloadString(event.Payload, "type"),
		MIMEType:   payloadString(event.Payload, "mime_type"),
		NodeID:     event.NodeID,
		StepID:     event.StepID,
		CreatedAt:  event.Timestamp,
	}
}

type eventToolItem struct {
	ToolCallID string
	Name       string
}

func toolItemsFromPayload(payload json.RawMessage) []eventToolItem {
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil
	}
	rawTools, _ := fields["tools"].([]any)
	if len(rawTools) == 0 {
		return nil
	}
	items := make([]eventToolItem, 0, len(rawTools))
	for _, raw := range rawTools {
		item, _ := raw.(map[string]any)
		items = append(items, eventToolItem{
			ToolCallID: stringFromAny(item["tool_call_id"]),
			Name:       stringFromAny(item["name"]),
		})
	}
	return items
}

func toolActiveKey(event Event, item eventToolItem) string {
	id := strings.TrimSpace(item.ToolCallID)
	name := strings.TrimSpace(item.Name)
	if id != "" {
		return event.StepID + "|id|" + id
	}
	return event.StepID + "|name|" + name
}

func subgraphActiveKey(event Event, ref string) string {
	return event.StepID + "|ref|" + strings.TrimSpace(ref)
}

func stableSortEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		left := events[i].Timestamp
		right := events[j].Timestamp
		if left.Equal(right) {
			return false
		}
		if left.IsZero() {
			return false
		}
		if right.IsZero() {
			return true
		}
		return left.Before(right)
	})
}

func firstEventRunID(events []Event) string {
	for _, event := range events {
		if strings.TrimSpace(event.RunID) != "" {
			return event.RunID
		}
	}
	return ""
}

func cloneEvents(events []Event) []Event {
	if len(events) == 0 {
		return []Event{}
	}
	cloned := make([]Event, len(events))
	for i, event := range events {
		cloned[i] = cloneEvent(event)
	}
	return cloned
}

func cloneEvent(event Event) Event {
	event.Payload = cloneRawMessage(event.Payload)
	return event
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func payloadString(payload json.RawMessage, key string) string {
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return ""
	}
	return stringFromAny(fields[key])
}

func payloadInt(payload json.RawMessage, key string) int {
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0
	}
	return intFromAny(fields[key])
}

func payloadArrayCount(payload json.RawMessage, key string) int {
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0
	}
	items, _ := fields[key].([]any)
	return len(items)
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint:
		return int(n)
	case uint8:
		return int(n)
	case uint16:
		return int(n)
	case uint32:
		return int(n)
	case uint64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		value, _ := n.Int64()
		return int(value)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nonNegativeDuration(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}
