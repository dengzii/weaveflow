package utilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	fruntime "github.com/dengzii/weaveflow/runtime"
)

type AnalyzeGraphRunner struct {
	Runner          *fruntime.GraphRunner
	ExecutionStore  fruntime.ExecutionStore
	CheckpointStore fruntime.CheckpointStore
	ArtifactStore   fruntime.ArtifactStore
	EventReader     fruntime.EventReader
}

type GraphRunAnalysis struct {
	Run         fruntime.RunRecord         `json:"run"`
	State       GraphRunState              `json:"state"`
	Stats       GraphRunStats              `json:"stats"`
	NodeStats   []GraphNodeRunStats        `json:"node_stats,omitempty"`
	EventCounts map[fruntime.EventType]int `json:"event_counts,omitempty"`
	Warnings    []string                   `json:"warnings,omitempty"`
}

type GraphRunState struct {
	Status           fruntime.RunStatus `json:"status"`
	Active           bool               `json:"active"`
	Terminal         bool               `json:"terminal"`
	Resumable        bool               `json:"resumable"`
	Continuable      bool               `json:"continuable"`
	Paused           bool               `json:"paused"`
	Failed           bool               `json:"failed"`
	Completed        bool               `json:"completed"`
	CurrentNodeID    string             `json:"current_node_id,omitempty"`
	CurrentNodeName  string             `json:"current_node_name,omitempty"`
	LastStepID       string             `json:"last_step_id,omitempty"`
	LastCheckpointID string             `json:"last_checkpoint_id,omitempty"`
	LastActivityAt   time.Time          `json:"last_activity_at,omitempty"`
	Duration         time.Duration      `json:"duration"`
	ErrorCode        string             `json:"error_code,omitempty"`
	ErrorMessage     string             `json:"error_message,omitempty"`
}

type GraphRunStats struct {
	StepCount              int                `json:"step_count"`
	NodeCount              int                `json:"node_count"`
	AttemptCount           int                `json:"attempt_count"`
	SucceededStepCount     int                `json:"succeeded_step_count"`
	FailedStepCount        int                `json:"failed_step_count"`
	PausedStepCount        int                `json:"paused_step_count"`
	RunningStepCount       int                `json:"running_step_count"`
	ScheduledStepCount     int                `json:"scheduled_step_count"`
	CheckpointCount        int                `json:"checkpoint_count"`
	ArtifactCount          int                `json:"artifact_count"`
	EventCount             int                `json:"event_count"`
	WarningCount           int                `json:"warning_count"`
	ContractViolationCount int                `json:"contract_violation_count"`
	StateChangeCount       int                `json:"state_change_count"`
	BreakpointHitCount     int                `json:"breakpoint_hit_count"`
	LLM                    GraphLLMStats      `json:"llm"`
	Tools                  GraphToolStats     `json:"tools"`
	Subgraphs              GraphSubgraphStats `json:"subgraphs"`
}

type GraphLLMStats struct {
	Calls              int                           `json:"calls"`
	PromptTokens       int                           `json:"prompt_tokens"`
	CompletionTokens   int                           `json:"completion_tokens"`
	TotalTokens        int                           `json:"total_tokens"`
	ReasoningTokens    int                           `json:"reasoning_tokens"`
	PromptCachedTokens int                           `json:"prompt_cached_tokens"`
	Models             map[string]GraphLLMModelStats `json:"models,omitempty"`
}

type GraphLLMModelStats struct {
	Calls              int `json:"calls"`
	PromptTokens       int `json:"prompt_tokens"`
	CompletionTokens   int `json:"completion_tokens"`
	TotalTokens        int `json:"total_tokens"`
	ReasoningTokens    int `json:"reasoning_tokens"`
	PromptCachedTokens int `json:"prompt_cached_tokens"`
}

type GraphToolStats struct {
	Started  int                           `json:"started"`
	Called   int                           `json:"called"`
	Returned int                           `json:"returned"`
	Failed   int                           `json:"failed"`
	ByName   map[string]GraphToolNameStats `json:"by_name,omitempty"`
}

type GraphToolNameStats struct {
	Started  int `json:"started"`
	Called   int `json:"called"`
	Returned int `json:"returned"`
	Failed   int `json:"failed"`
}

type GraphSubgraphStats struct {
	Started  int                              `json:"started"`
	Finished int                              `json:"finished"`
	Failed   int                              `json:"failed"`
	ByRef    map[string]GraphSubgraphRefStats `json:"by_ref,omitempty"`
}

type GraphSubgraphRefStats struct {
	Started  int `json:"started"`
	Finished int `json:"finished"`
	Failed   int `json:"failed"`
}

type GraphNodeRunStats struct {
	NodeID           string              `json:"node_id"`
	NodeName         string              `json:"node_name,omitempty"`
	StepCount        int                 `json:"step_count"`
	AttemptCount     int                 `json:"attempt_count"`
	Succeeded        int                 `json:"succeeded"`
	Failed           int                 `json:"failed"`
	Paused           int                 `json:"paused"`
	Running          int                 `json:"running"`
	Scheduled        int                 `json:"scheduled"`
	RetryCount       int                 `json:"retry_count"`
	ToolCalls        int                 `json:"tool_calls"`
	ToolFailures     int                 `json:"tool_failures"`
	LLMCalls         int                 `json:"llm_calls"`
	TotalDuration    time.Duration       `json:"total_duration"`
	FirstStartedAt   time.Time           `json:"first_started_at,omitempty"`
	LastFinishedAt   time.Time           `json:"last_finished_at,omitempty"`
	LastStatus       fruntime.StepStatus `json:"last_status,omitempty"`
	LastErrorCode    string              `json:"last_error_code,omitempty"`
	LastErrorMessage string              `json:"last_error_message,omitempty"`
}

func NewAnalyzeGraphRunner(runner *fruntime.GraphRunner) *AnalyzeGraphRunner {
	return &AnalyzeGraphRunner{Runner: runner}
}

func NewAnalyzeGraphRunnerFromStores(executionStore fruntime.ExecutionStore, checkpointStore fruntime.CheckpointStore, artifactStore fruntime.ArtifactStore, eventReader fruntime.EventReader) *AnalyzeGraphRunner {
	return &AnalyzeGraphRunner{
		ExecutionStore:  executionStore,
		CheckpointStore: checkpointStore,
		ArtifactStore:   artifactStore,
		EventReader:     eventReader,
	}
}

func (a *AnalyzeGraphRunner) AnalyzeRun(ctx context.Context, runID string) (GraphRunAnalysis, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return GraphRunAnalysis{}, fmt.Errorf("run id is required")
	}
	executionStore := a.executionStore()
	if executionStore == nil {
		return GraphRunAnalysis{}, errors.New("analyze graph runner execution store is nil")
	}
	run, err := executionStore.GetRun(ctx, runID)
	if err != nil {
		return GraphRunAnalysis{}, err
	}
	return a.analyzeRunRecord(ctx, run)
}

func (a *AnalyzeGraphRunner) AnalyzeRuns(ctx context.Context, filter fruntime.RunFilter) ([]GraphRunAnalysis, error) {
	executionStore := a.executionStore()
	if executionStore == nil {
		return nil, errors.New("analyze graph runner execution store is nil")
	}
	runs, err := executionStore.ListRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	analyses := make([]GraphRunAnalysis, 0, len(runs))
	for _, run := range runs {
		analysis, err := a.analyzeRunRecord(ctx, run)
		if err != nil {
			return nil, err
		}
		analyses = append(analyses, analysis)
	}
	return analyses, nil
}

func (a *AnalyzeGraphRunner) AnalyzeLatestRun(ctx context.Context, filter fruntime.RunFilter) (*GraphRunAnalysis, error) {
	executionStore := a.executionStore()
	if executionStore == nil {
		return nil, errors.New("analyze graph runner execution store is nil")
	}
	runs, err := executionStore.ListRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	latest := runs[0]
	for _, run := range runs[1:] {
		if latest.UpdatedAt.Before(run.UpdatedAt) {
			latest = run
		}
	}
	analysis, err := a.analyzeRunRecord(ctx, latest)
	if err != nil {
		return nil, err
	}
	return &analysis, nil
}

func (a *AnalyzeGraphRunner) analyzeRunRecord(ctx context.Context, run fruntime.RunRecord) (GraphRunAnalysis, error) {
	executionStore := a.executionStore()
	if executionStore == nil {
		return GraphRunAnalysis{}, errors.New("analyze graph runner execution store is nil")
	}
	steps, err := executionStore.ListSteps(ctx, run.RunID)
	if err != nil {
		return GraphRunAnalysis{}, err
	}

	var warnings []string
	checkpoints := []fruntime.CheckpointRecord{}
	if checkpointStore := a.checkpointStore(); checkpointStore != nil {
		checkpoints, err = checkpointStore.List(ctx, run.RunID)
		if err != nil {
			return GraphRunAnalysis{}, err
		}
	}

	artifactCount := 0
	if artifactStore := a.artifactStore(); artifactStore != nil {
		artifacts, err := artifactStore.List(ctx, run.RunID)
		if err != nil {
			return GraphRunAnalysis{}, err
		}
		artifactCount = len(artifacts)
	}

	events := []fruntime.Event{}
	if eventReader := a.eventReader(); eventReader != nil {
		events, err = eventReader.ListEvents(run.RunID)
		if err != nil {
			return GraphRunAnalysis{}, err
		}
	} else {
		warnings = append(warnings, "event reader is unavailable")
	}

	nodeStats := buildNodeRunStats(steps, events)
	stats := buildGraphRunStats(steps, len(checkpoints), artifactCount, events, nodeStats)
	state := buildGraphRunState(run, steps)

	return GraphRunAnalysis{
		Run:         run,
		State:       state,
		Stats:       stats,
		NodeStats:   nodeStats,
		EventCounts: eventCounts(events),
		Warnings:    warnings,
	}, nil
}

func (a *AnalyzeGraphRunner) executionStore() fruntime.ExecutionStore {
	if a == nil {
		return nil
	}
	if a.ExecutionStore != nil {
		return a.ExecutionStore
	}
	if a.Runner != nil {
		return a.Runner.ExecutionStore
	}
	return nil
}

func (a *AnalyzeGraphRunner) checkpointStore() fruntime.CheckpointStore {
	if a == nil {
		return nil
	}
	if a.CheckpointStore != nil {
		return a.CheckpointStore
	}
	if a.Runner != nil {
		return a.Runner.CheckpointStore
	}
	return nil
}

func (a *AnalyzeGraphRunner) artifactStore() fruntime.ArtifactStore {
	if a == nil {
		return nil
	}
	if a.ArtifactStore != nil {
		return a.ArtifactStore
	}
	if a.Runner != nil {
		return a.Runner.ArtifactStore
	}
	return nil
}

func (a *AnalyzeGraphRunner) eventReader() fruntime.EventReader {
	if a == nil {
		return nil
	}
	if a.EventReader != nil {
		return a.EventReader
	}
	if a.Runner == nil || a.Runner.EventSink == nil {
		return nil
	}
	reader, _ := a.Runner.EventSink.(fruntime.EventReader)
	return reader
}

func buildGraphRunState(run fruntime.RunRecord, steps []fruntime.StepRecord) GraphRunState {
	lastActivityAt := run.UpdatedAt
	for _, step := range steps {
		if lastActivityAt.Before(step.UpdatedAt) {
			lastActivityAt = step.UpdatedAt
		}
	}
	currentNodeName := ""
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].NodeID == run.CurrentNodeID || steps[i].StepID == run.LastStepID {
			currentNodeName = steps[i].NodeName
			break
		}
	}
	return GraphRunState{
		Status:           run.Status,
		Active:           isActiveRunStatus(run.Status),
		Terminal:         isTerminalRunStatus(run.Status),
		Resumable:        isResumableRunStatus(run.Status),
		Continuable:      isContinuableRunStatus(run.Status),
		Paused:           run.Status == fruntime.RunStatusPaused,
		Failed:           run.Status == fruntime.RunStatusFailed,
		Completed:        run.Status == fruntime.RunStatusCompleted,
		CurrentNodeID:    run.CurrentNodeID,
		CurrentNodeName:  currentNodeName,
		LastStepID:       run.LastStepID,
		LastCheckpointID: run.LastCheckpointID,
		LastActivityAt:   lastActivityAt,
		Duration:         runDuration(run),
		ErrorCode:        run.ErrorCode,
		ErrorMessage:     run.ErrorMessage,
	}
}

func buildGraphRunStats(steps []fruntime.StepRecord, checkpointCount int, artifactCount int, events []fruntime.Event, nodeStats []GraphNodeRunStats) GraphRunStats {
	stats := GraphRunStats{
		StepCount:       len(steps),
		NodeCount:       len(nodeStats),
		CheckpointCount: checkpointCount,
		ArtifactCount:   artifactCount,
		EventCount:      len(events),
	}
	for _, step := range steps {
		attempts := step.Attempt
		if attempts <= 0 {
			attempts = 1
		}
		stats.AttemptCount += attempts
		switch step.Status {
		case fruntime.StepStatusSucceeded:
			stats.SucceededStepCount++
		case fruntime.StepStatusFailed:
			stats.FailedStepCount++
		case fruntime.StepStatusPaused:
			stats.PausedStepCount++
		case fruntime.StepStatusRunning:
			stats.RunningStepCount++
		case fruntime.StepStatusScheduled:
			stats.ScheduledStepCount++
		}
	}
	for _, event := range events {
		applyEventStats(&stats, event)
	}
	return stats
}

func buildNodeRunStats(steps []fruntime.StepRecord, events []fruntime.Event) []GraphNodeRunStats {
	byNode := make(map[string]*GraphNodeRunStats)
	for _, step := range steps {
		nodeID := strings.TrimSpace(step.NodeID)
		if nodeID == "" {
			continue
		}
		stats := nodeStatsFor(byNode, nodeID)
		stats.NodeName = firstNonEmptyString(stats.NodeName, step.NodeName)
		stats.StepCount++
		attempts := step.Attempt
		if attempts <= 0 {
			attempts = 1
		}
		stats.AttemptCount += attempts
		switch step.Status {
		case fruntime.StepStatusSucceeded:
			stats.Succeeded++
		case fruntime.StepStatusFailed:
			stats.Failed++
		case fruntime.StepStatusPaused:
			stats.Paused++
		case fruntime.StepStatusRunning:
			stats.Running++
		case fruntime.StepStatusScheduled:
			stats.Scheduled++
		}
		stats.TotalDuration += stepDuration(step)
		if stats.FirstStartedAt.IsZero() || step.StartedAt.Before(stats.FirstStartedAt) {
			stats.FirstStartedAt = step.StartedAt
		}
		if step.FinishedAt != nil && (stats.LastFinishedAt.IsZero() || stats.LastFinishedAt.Before(*step.FinishedAt)) {
			stats.LastFinishedAt = *step.FinishedAt
		}
		if stats.LastStatus == "" || !step.UpdatedAt.Before(stats.LastFinishedAt) {
			stats.LastStatus = step.Status
			stats.LastErrorCode = step.ErrorCode
			stats.LastErrorMessage = step.ErrorMessage
		}
	}

	for _, event := range events {
		nodeID := strings.TrimSpace(event.NodeID)
		if nodeID == "" {
			continue
		}
		stats := nodeStatsFor(byNode, nodeID)
		switch event.Type {
		case fruntime.EventNodeStarted:
			stats.NodeName = firstNonEmptyString(stats.NodeName, stringPayloadField(event.Payload, "node_name"))
		case fruntime.EventNodeRetry:
			stats.RetryCount++
		case fruntime.EventToolCalled:
			stats.ToolCalls += toolEventCount(event.Payload)
		case fruntime.EventToolFailed:
			stats.ToolFailures += toolEventCount(event.Payload)
		case fruntime.EventLLMCall, fruntime.EventLLMUsage:
			calls := intPayloadField(event.Payload, "calls")
			if calls <= 0 {
				calls = 1
			}
			stats.LLMCalls += calls
		}
	}

	result := make([]GraphNodeRunStats, 0, len(byNode))
	for _, stats := range byNode {
		result = append(result, *stats)
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i]
		right := result[j]
		if left.FirstStartedAt.Equal(right.FirstStartedAt) {
			return left.NodeID < right.NodeID
		}
		if left.FirstStartedAt.IsZero() {
			return false
		}
		if right.FirstStartedAt.IsZero() {
			return true
		}
		return left.FirstStartedAt.Before(right.FirstStartedAt)
	})
	return result
}

func nodeStatsFor(byNode map[string]*GraphNodeRunStats, nodeID string) *GraphNodeRunStats {
	stats := byNode[nodeID]
	if stats == nil {
		stats = &GraphNodeRunStats{NodeID: nodeID}
		byNode[nodeID] = stats
	}
	return stats
}

func applyEventStats(stats *GraphRunStats, event fruntime.Event) {
	switch event.Type {
	case fruntime.EventLLMCall, fruntime.EventLLMUsage:
		applyLLMStats(&stats.LLM, event.Payload)
	case fruntime.EventToolStarted:
		count := toolEventCount(event.Payload)
		stats.Tools.Started += count
		applyToolNameStats(&stats.Tools, event.Payload, func(item *GraphToolNameStats) { item.Started++ })
	case fruntime.EventToolCalled:
		count := toolEventCount(event.Payload)
		stats.Tools.Called += count
		applyToolNameStats(&stats.Tools, event.Payload, func(item *GraphToolNameStats) { item.Called++ })
	case fruntime.EventToolReturned:
		count := toolEventCount(event.Payload)
		stats.Tools.Returned += count
		applyToolNameStats(&stats.Tools, event.Payload, func(item *GraphToolNameStats) { item.Returned++ })
	case fruntime.EventToolFailed:
		count := toolEventCount(event.Payload)
		stats.Tools.Failed += count
		applyToolNameStats(&stats.Tools, event.Payload, func(item *GraphToolNameStats) { item.Failed++ })
	case fruntime.EventSubgraphStarted:
		stats.Subgraphs.Started++
		applySubgraphRefStats(&stats.Subgraphs, event.Payload, func(item *GraphSubgraphRefStats) { item.Started++ })
	case fruntime.EventSubgraphFinished:
		stats.Subgraphs.Finished++
		applySubgraphRefStats(&stats.Subgraphs, event.Payload, func(item *GraphSubgraphRefStats) { item.Finished++ })
	case fruntime.EventSubgraphFailed:
		stats.Subgraphs.Failed++
		applySubgraphRefStats(&stats.Subgraphs, event.Payload, func(item *GraphSubgraphRefStats) { item.Failed++ })
	case fruntime.EventWarning:
		stats.WarningCount++
	case fruntime.EventContractViolation:
		count := payloadArrayCount(event.Payload, "violations")
		if count <= 0 {
			count = 1
		}
		stats.ContractViolationCount += count
	case fruntime.EventStateChanged:
		stats.StateChangeCount += payloadArrayCount(event.Payload, "changes")
	case fruntime.EventBreakpointHit:
		stats.BreakpointHitCount++
	}
}

func applyLLMStats(stats *GraphLLMStats, payload json.RawMessage) {
	calls := intPayloadField(payload, "calls")
	if calls <= 0 {
		calls = 1
	}
	prompt := intPayloadField(payload, "prompt_tokens")
	completion := intPayloadField(payload, "completion_tokens")
	total := intPayloadField(payload, "total_tokens")
	reasoning := intPayloadField(payload, "reasoning_tokens")
	cached := intPayloadField(payload, "prompt_cached_tokens")

	stats.Calls += calls
	stats.PromptTokens += prompt
	stats.CompletionTokens += completion
	stats.TotalTokens += total
	stats.ReasoningTokens += reasoning
	stats.PromptCachedTokens += cached

	model := strings.TrimSpace(stringPayloadField(payload, "model"))
	if model == "" {
		return
	}
	if stats.Models == nil {
		stats.Models = make(map[string]GraphLLMModelStats)
	}
	modelStats := stats.Models[model]
	modelStats.Calls += calls
	modelStats.PromptTokens += prompt
	modelStats.CompletionTokens += completion
	modelStats.TotalTokens += total
	modelStats.ReasoningTokens += reasoning
	modelStats.PromptCachedTokens += cached
	stats.Models[model] = modelStats
}

func applyToolNameStats(stats *GraphToolStats, payload json.RawMessage, apply func(*GraphToolNameStats)) {
	names := toolEventNames(payload)
	if len(names) == 0 {
		return
	}
	if stats.ByName == nil {
		stats.ByName = make(map[string]GraphToolNameStats)
	}
	for _, name := range names {
		item := stats.ByName[name]
		apply(&item)
		stats.ByName[name] = item
	}
}

func applySubgraphRefStats(stats *GraphSubgraphStats, payload json.RawMessage, apply func(*GraphSubgraphRefStats)) {
	ref := strings.TrimSpace(stringPayloadField(payload, "graph_ref"))
	if ref == "" {
		return
	}
	if stats.ByRef == nil {
		stats.ByRef = make(map[string]GraphSubgraphRefStats)
	}
	item := stats.ByRef[ref]
	apply(&item)
	stats.ByRef[ref] = item
}

func eventCounts(events []fruntime.Event) map[fruntime.EventType]int {
	if len(events) == 0 {
		return nil
	}
	counts := make(map[fruntime.EventType]int)
	for _, event := range events {
		counts[event.Type]++
	}
	return counts
}

func runDuration(run fruntime.RunRecord) time.Duration {
	end := run.UpdatedAt
	if run.FinishedAt != nil {
		end = *run.FinishedAt
	}
	if end.Before(run.StartedAt) {
		return 0
	}
	return end.Sub(run.StartedAt)
}

func stepDuration(step fruntime.StepRecord) time.Duration {
	if step.StartedAt.IsZero() {
		return 0
	}
	end := step.UpdatedAt
	if step.FinishedAt != nil {
		end = *step.FinishedAt
	}
	if end.Before(step.StartedAt) {
		return 0
	}
	return end.Sub(step.StartedAt)
}

func isActiveRunStatus(status fruntime.RunStatus) bool {
	switch status {
	case fruntime.RunStatusPending, fruntime.RunStatusRunning:
		return true
	default:
		return false
	}
}

func isTerminalRunStatus(status fruntime.RunStatus) bool {
	switch status {
	case fruntime.RunStatusCompleted, fruntime.RunStatusFailed, fruntime.RunStatusCanceled:
		return true
	default:
		return false
	}
}

func isResumableRunStatus(status fruntime.RunStatus) bool {
	switch status {
	case fruntime.RunStatusPaused, fruntime.RunStatusRunning, fruntime.RunStatusPending:
		return true
	default:
		return false
	}
}

func isContinuableRunStatus(status fruntime.RunStatus) bool {
	if isResumableRunStatus(status) {
		return true
	}
	switch status {
	case fruntime.RunStatusCompleted, fruntime.RunStatusFailed, fruntime.RunStatusCanceled:
		return true
	default:
		return false
	}
}

func toolEventCount(payload json.RawMessage) int {
	if len(payload) == 0 {
		return 1
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 1
	}
	if count := analyzeIntFromAny(fields["count"]); count > 0 {
		return count
	}
	if tools, ok := fields["tools"].([]any); ok && len(tools) > 0 {
		return len(tools)
	}
	return 1
}

func toolEventNames(payload json.RawMessage) []string {
	if len(payload) == 0 {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil
	}
	if tools, ok := fields["tools"].([]any); ok {
		names := make([]string, 0, len(tools))
		for _, raw := range tools {
			item, _ := raw.(map[string]any)
			name := strings.TrimSpace(stringFromAny(item["name"]))
			if name != "" {
				names = append(names, name)
			}
		}
		return names
	}
	name := strings.TrimSpace(stringFromAny(fields["name"]))
	if name == "" {
		return nil
	}
	return []string{name}
}

func intPayloadField(payload json.RawMessage, key string) int {
	if len(payload) == 0 {
		return 0
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0
	}
	return analyzeIntFromAny(fields[key])
}

func stringPayloadField(payload json.RawMessage, key string) string {
	if len(payload) == 0 {
		return ""
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return ""
	}
	return stringFromAny(fields[key])
}

func payloadArrayCount(payload json.RawMessage, key string) int {
	if len(payload) == 0 {
		return 0
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0
	}
	items, ok := fields[key].([]any)
	if !ok {
		return 0
	}
	return len(items)
}

func analyzeIntFromAny(v any) int {
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
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmptyString(left, right string) string {
	if strings.TrimSpace(left) != "" {
		return left
	}
	return right
}
