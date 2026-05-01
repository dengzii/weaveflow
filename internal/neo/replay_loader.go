package neo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"weaveflow/runtime"
)

const artifactPreviewLimit = 128 * 1024

type cacheExplorer struct {
	baseDir string
	sources []cacheSource
}

type cacheSource struct {
	meta        SourceMeta
	execution   *runtime.FileExecutionStore
	checkpoints *runtime.FileCheckpointStore
	events      *runtime.FileEventSink
	artifacts   *runtime.FileArtifactStore
	codec       runtime.StateCodec
}

type RunsResponse struct {
	CacheDir string       `json:"cache_dir"`
	Sources  []SourceMeta `json:"sources"`
	Runs     []RunSummary `json:"runs"`
}

type SourceMeta struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Root         string   `json:"root"`
	InstanceID   string   `json:"instance_id,omitempty"`
	GraphRef     string   `json:"graph_ref,omitempty"`
	GraphVersion string   `json:"graph_version,omitempty"`
	Instance     any      `json:"instance,omitempty"`
	Graph        any      `json:"graph,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

type RunSummary struct {
	SourceID        string            `json:"source_id"`
	SourceName      string            `json:"source_name"`
	CacheRoot       string            `json:"cache_root"`
	InstanceID      string            `json:"instance_id,omitempty"`
	GraphRef        string            `json:"graph_ref,omitempty"`
	GraphVersion    string            `json:"graph_version,omitempty"`
	Run             runtime.RunRecord `json:"run"`
	DurationMS      int64             `json:"duration_ms"`
	StepCount       int               `json:"step_count"`
	EventCount      int               `json:"event_count"`
	CheckpointCount int               `json:"checkpoint_count"`
	ArtifactCount   int               `json:"artifact_count"`
}

type RunDetail struct {
	Summary     RunSummary          `json:"summary"`
	Source      SourceMeta          `json:"source"`
	Run         runtime.RunRecord   `json:"run"`
	Steps       []StepView          `json:"steps"`
	Events      []EventView         `json:"events"`
	Replay      []ReplayItem        `json:"replay"`
	Checkpoints []CheckpointSummary `json:"checkpoints"`
	Artifacts   []ArtifactSummary   `json:"artifacts"`
}

type StepView struct {
	Record     runtime.StepRecord `json:"record"`
	DurationMS int64              `json:"duration_ms"`
}

type EventView struct {
	ID        string            `json:"id"`
	RunID     string            `json:"run_id"`
	StepID    string            `json:"step_id,omitempty"`
	NodeID    string            `json:"node_id,omitempty"`
	Type      runtime.EventType `json:"type"`
	Timestamp time.Time         `json:"timestamp"`
	Payload   any               `json:"payload,omitempty"`
}

type ReplayItem struct {
	Index     int       `json:"index"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"`
	Event     EventView `json:"event"`
}

type CheckpointSummary struct {
	Record runtime.CheckpointRecord `json:"record"`
}

type CheckpointDetail struct {
	Source    SourceMeta               `json:"source"`
	Record    runtime.CheckpointRecord `json:"record"`
	Snapshot  runtime.StateSnapshot    `json:"snapshot"`
	Business  runtime.State            `json:"business"`
	Runtime   runtime.RuntimeState     `json:"runtime"`
	Artifacts []runtime.ArtifactRef    `json:"artifacts,omitempty"`
}

type ArtifactSummary struct {
	Ref   runtime.ArtifactRef `json:"ref"`
	Bytes int64               `json:"bytes"`
}

type ArtifactDetail struct {
	Source    SourceMeta          `json:"source"`
	Ref       runtime.ArtifactRef `json:"ref"`
	Bytes     int                 `json:"bytes"`
	Encoding  string              `json:"encoding"`
	Payload   any                 `json:"payload,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
}

func newCacheExplorer(baseDir string) (*cacheExplorer, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("cache_dir is required")
	}

	absDir, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cache_dir %q is not a directory", absDir)
	}

	roots, err := discoverCacheRoots(absDir)
	if err != nil {
		return nil, err
	}

	sources := make([]cacheSource, 0, len(roots))
	for _, root := range roots {
		sources = append(sources, cacheSource{
			meta:        loadSourceMeta(absDir, root),
			execution:   runtime.NewFileExecutionStore(filepath.Join(root, "execution")),
			checkpoints: runtime.NewFileCheckpointStore(filepath.Join(root, "checkpoints")),
			events:      runtime.NewFileEventSink(filepath.Join(root, "events")),
			artifacts:   runtime.NewFileArtifactStore(filepath.Join(root, "artifacts")),
			codec:       runtime.NewJSONStateCodec(runtime.DefaultStateVersion),
		})
	}

	sort.Slice(sources, func(i, j int) bool {
		left := strings.ToLower(sources[i].meta.Name)
		right := strings.ToLower(sources[j].meta.Name)
		if left == right {
			return sources[i].meta.ID < sources[j].meta.ID
		}
		return left < right
	})

	return &cacheExplorer{
		baseDir: absDir,
		sources: sources,
	}, nil
}

func discoverCacheRoots(baseDir string) ([]string, error) {
	if hasCacheLayout(baseDir) {
		return []string{baseDir}, nil
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}

	roots := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(baseDir, entry.Name())
		if hasCacheLayout(candidate) {
			roots = append(roots, candidate)
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("cache_dir %q does not contain a graph cache layout", baseDir)
	}
	sort.Strings(roots)
	return roots, nil
}

func hasCacheLayout(dir string) bool {
	if isDir(filepath.Join(dir, "execution", "runs")) {
		return true
	}
	if !isDir(filepath.Join(dir, "execution")) {
		return false
	}
	return isDir(filepath.Join(dir, "events")) || isDir(filepath.Join(dir, "checkpoints")) || isDir(filepath.Join(dir, "artifacts"))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func loadSourceMeta(baseDir, root string) SourceMeta {
	meta := SourceMeta{
		ID:   sourceID(baseDir, root),
		Name: filepath.Base(root),
		Root: root,
	}

	instancePath := filepath.Join(root, "instance.json")
	if raw, err := readOptionalJSON(instancePath); err == nil {
		meta.Instance = raw
		meta.InstanceID = stringField(raw, "id")
		meta.GraphRef = stringField(raw, "graph_ref")
		meta.GraphVersion = stringField(raw, "graph_version")
		if name := stringField(raw, "name"); name != "" {
			meta.Name = name
		} else if meta.InstanceID != "" {
			meta.Name = meta.InstanceID
		}
	} else if !os.IsNotExist(err) {
		meta.Warnings = append(meta.Warnings, fmt.Sprintf("read instance.json: %v", err))
	}

	graphPath := filepath.Join(root, "graph.json")
	if raw, err := readOptionalJSON(graphPath); err == nil {
		meta.Graph = raw
	} else if !os.IsNotExist(err) {
		meta.Warnings = append(meta.Warnings, fmt.Sprintf("read graph.json: %v", err))
	}

	if meta.Name == "" {
		meta.Name = filepath.Base(root)
	}
	if meta.Name == "" || meta.Name == "." {
		meta.Name = root
	}
	return meta
}

func sourceID(baseDir, root string) string {
	rel, err := filepath.Rel(baseDir, root)
	if err != nil {
		return filepath.ToSlash(root)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return "."
	}
	return rel
}

func readOptionalJSON(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func stringField(value any, key string) string {
	mapped, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	text, ok := mapped[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func (e *cacheExplorer) sourceMetas() []SourceMeta {
	metas := make([]SourceMeta, 0, len(e.sources))
	for _, source := range e.sources {
		metas = append(metas, source.meta)
	}
	return metas
}

func (e *cacheExplorer) listRuns(ctx context.Context) ([]RunSummary, error) {
	items := make([]RunSummary, 0)
	for _, source := range e.sources {
		runs, err := source.execution.ListRuns(ctx, runtime.RunFilter{})
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			stepCount, err := source.countSteps(ctx, run.RunID)
			if err != nil {
				return nil, err
			}
			eventCount, err := source.countEvents(run.RunID)
			if err != nil {
				return nil, err
			}
			checkpointCount, err := source.countCheckpoints(ctx, run.RunID)
			if err != nil {
				return nil, err
			}
			artifactCount, err := source.countArtifacts(ctx, run.RunID)
			if err != nil {
				return nil, err
			}
			items = append(items, buildRunSummary(source, run, stepCount, eventCount, checkpointCount, artifactCount))
		}
	}

	sort.Slice(items, func(i, j int) bool {
		left := items[i].Run.UpdatedAt
		right := items[j].Run.UpdatedAt
		if left.Equal(right) {
			return items[i].Run.RunID > items[j].Run.RunID
		}
		return left.After(right)
	})
	return items, nil
}

func buildRunSummary(source cacheSource, run runtime.RunRecord, stepCount, eventCount, checkpointCount, artifactCount int) RunSummary {
	return RunSummary{
		SourceID:        source.meta.ID,
		SourceName:      source.meta.Name,
		CacheRoot:       source.meta.Root,
		InstanceID:      source.meta.InstanceID,
		GraphRef:        nonEmpty(source.meta.GraphRef, run.GraphID),
		GraphVersion:    nonEmpty(source.meta.GraphVersion, run.GraphVersion),
		Run:             run,
		DurationMS:      durationMS(run.StartedAt, run.FinishedAt, run.UpdatedAt),
		StepCount:       stepCount,
		EventCount:      eventCount,
		CheckpointCount: checkpointCount,
		ArtifactCount:   artifactCount,
	}
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func durationMS(start time.Time, finish *time.Time, fallback time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	end := fallback
	if finish != nil {
		end = *finish
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func (s cacheSource) countSteps(ctx context.Context, runID string) (int, error) {
	steps, err := s.execution.ListSteps(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(steps), nil
}

func (s cacheSource) countEvents(runID string) (int, error) {
	events, err := s.events.ListEvents(runID)
	if err != nil {
		return 0, err
	}
	return len(events), nil
}

func (s cacheSource) countCheckpoints(ctx context.Context, runID string) (int, error) {
	records, err := s.checkpoints.List(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func (s cacheSource) countArtifacts(ctx context.Context, runID string) (int, error) {
	records, err := s.artifacts.List(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func (e *cacheExplorer) loadRunDetail(ctx context.Context, runID, sourceID string) (RunDetail, error) {
	source, run, err := e.locateRun(ctx, runID, sourceID)
	if err != nil {
		return RunDetail{}, err
	}

	steps, err := source.execution.ListSteps(ctx, runID)
	if err != nil {
		return RunDetail{}, err
	}
	events, err := source.events.ListEvents(runID)
	if err != nil {
		return RunDetail{}, err
	}
	checkpoints, err := source.checkpoints.List(ctx, runID)
	if err != nil {
		return RunDetail{}, err
	}
	artifacts, err := source.artifacts.List(ctx, runID)
	if err != nil {
		return RunDetail{}, err
	}

	stepViews := make([]StepView, 0, len(steps))
	for _, step := range steps {
		stepViews = append(stepViews, StepView{
			Record:     step,
			DurationMS: durationMS(step.StartedAt, step.FinishedAt, step.UpdatedAt),
		})
	}

	eventViews, err := decodeEvents(events)
	if err != nil {
		return RunDetail{}, err
	}

	checkpointViews := make([]CheckpointSummary, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		checkpointViews = append(checkpointViews, CheckpointSummary{Record: checkpoint})
	}

	artifactViews := make([]ArtifactSummary, 0, len(artifacts))
	for _, ref := range artifacts {
		artifactViews = append(artifactViews, ArtifactSummary{
			Ref:   ref,
			Bytes: artifactSize(ref.Location),
		})
	}

	return RunDetail{
		Summary:     buildRunSummary(*source, run, len(steps), len(events), len(checkpoints), len(artifacts)),
		Source:      source.meta,
		Run:         run,
		Steps:       stepViews,
		Events:      eventViews,
		Replay:      buildReplay(eventViews),
		Checkpoints: checkpointViews,
		Artifacts:   artifactViews,
	}, nil
}

func (e *cacheExplorer) loadCheckpointDetail(ctx context.Context, runID, sourceID, checkpointID string) (CheckpointDetail, error) {
	source, _, err := e.locateRun(ctx, runID, sourceID)
	if err != nil {
		return CheckpointDetail{}, err
	}

	record, payload, err := source.checkpoints.Load(ctx, checkpointID)
	if err != nil {
		return CheckpointDetail{}, err
	}
	if record.RunID != runID {
		return CheckpointDetail{}, fmt.Errorf("checkpoint %q does not belong to run %q", checkpointID, runID)
	}

	snapshot, err := source.codec.Decode(payload)
	if err != nil {
		return CheckpointDetail{}, err
	}
	restored, err := runtime.RestoreStateSnapshot(snapshot)
	if err != nil {
		return CheckpointDetail{}, err
	}

	return CheckpointDetail{
		Source:    source.meta,
		Record:    record,
		Snapshot:  restored.Snapshot,
		Business:  restored.Business,
		Runtime:   restored.Runtime,
		Artifacts: restored.Artifacts,
	}, nil
}

func (e *cacheExplorer) loadArtifactRaw(ctx context.Context, runID, sourceID, artifactID string) (runtime.Artifact, SourceMeta, error) {
	source, _, err := e.locateRun(ctx, runID, sourceID)
	if err != nil {
		return runtime.Artifact{}, SourceMeta{}, err
	}

	artifact, err := source.artifacts.Load(ctx, runtime.ArtifactRef{
		RunID: runID,
		ID:    artifactID,
	})
	if err != nil {
		return runtime.Artifact{}, SourceMeta{}, err
	}
	return artifact, source.meta, nil
}

func (e *cacheExplorer) loadArtifactDetail(ctx context.Context, runID, sourceID, artifactID string) (ArtifactDetail, error) {
	artifact, sourceMeta, err := e.loadArtifactRaw(ctx, runID, sourceID, artifactID)
	if err != nil {
		return ArtifactDetail{}, err
	}

	payload, encoding, truncated := previewArtifact(artifact, artifactPreviewLimit)
	return ArtifactDetail{
		Source: sourceMeta,
		Ref: runtime.ArtifactRef{
			ID:        artifact.ID,
			RunID:     artifact.RunID,
			StepID:    artifact.StepID,
			NodeID:    artifact.NodeID,
			Type:      artifact.Type,
			MIMEType:  artifact.MIMEType,
			Location:  artifact.Location,
			CreatedAt: artifact.CreatedAt,
		},
		Bytes:     len(artifact.Data),
		Encoding:  encoding,
		Payload:   payload,
		Truncated: truncated,
	}, nil
}

func (e *cacheExplorer) locateRun(ctx context.Context, runID, sourceID string) (*cacheSource, runtime.RunRecord, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, runtime.RunRecord{}, fmt.Errorf("run id is required")
	}

	if strings.TrimSpace(sourceID) != "" {
		source, err := e.sourceByID(sourceID)
		if err != nil {
			return nil, runtime.RunRecord{}, err
		}
		run, err := source.execution.GetRun(ctx, runID)
		if err != nil {
			return nil, runtime.RunRecord{}, err
		}
		return source, run, nil
	}

	var matched *cacheSource
	var record runtime.RunRecord
	for index := range e.sources {
		source := &e.sources[index]
		run, err := source.execution.GetRun(ctx, runID)
		switch {
		case err == nil:
			if matched != nil {
				return nil, runtime.RunRecord{}, fmt.Errorf("run %q exists in multiple cache roots, provide source id explicitly", runID)
			}
			matched = source
			record = run
		case errors.Is(err, runtime.ErrRunnerRecordNotFound):
			continue
		default:
			return nil, runtime.RunRecord{}, err
		}
	}

	if matched == nil {
		return nil, runtime.RunRecord{}, runtime.ErrRunnerRecordNotFound
	}
	return matched, record, nil
}

func (e *cacheExplorer) sourceByID(sourceID string) (*cacheSource, error) {
	sourceID = strings.TrimSpace(sourceID)
	for index := range e.sources {
		source := &e.sources[index]
		if source.meta.ID == sourceID || source.meta.Root == sourceID {
			return source, nil
		}
	}
	return nil, fmt.Errorf("source %q not found", sourceID)
}

func decodeEvents(events []runtime.Event) ([]EventView, error) {
	items := make([]EventView, 0, len(events))
	for _, event := range events {
		payload, err := decodeJSONPayload(event.Payload)
		if err != nil {
			return nil, err
		}
		items = append(items, EventView{
			ID:        event.ID,
			RunID:     event.RunID,
			StepID:    event.StepID,
			NodeID:    event.NodeID,
			Type:      event.Type,
			Timestamp: event.Timestamp,
			Payload:   payload,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		left := items[i].Timestamp
		right := items[j].Timestamp
		if left.Equal(right) {
			return items[i].ID < items[j].ID
		}
		return left.Before(right)
	})
	return items, nil
}

func decodeJSONPayload(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func buildReplay(events []EventView) []ReplayItem {
	items := make([]ReplayItem, 0, len(events))
	for index, event := range events {
		items = append(items, ReplayItem{
			Index:     index,
			Timestamp: event.Timestamp,
			Level:     replayLevel(event),
			Title:     replayTitle(event),
			Subtitle:  replaySubtitle(event),
			Event:     event,
		})
	}
	return items
}

func replayLevel(event EventView) string {
	switch event.Type {
	case runtime.EventRunFailed, runtime.EventNodeFailed, runtime.EventToolFailed, runtime.EventSubgraphFailed, runtime.EventContractViolation:
		return "error"
	case runtime.EventRunPaused, runtime.EventBreakpointHit, runtime.EventRunPauseRequested:
		return "warning"
	case runtime.EventRunFinished, runtime.EventNodeFinished, runtime.EventToolReturned, runtime.EventSubgraphFinished:
		return "success"
	default:
		return "info"
	}
}

func replayTitle(event EventView) string {
	switch event.Type {
	case runtime.EventRunCreated:
		return "创建运行"
	case runtime.EventRunStarted:
		return "运行开始"
	case runtime.EventRunPauseRequested:
		return "请求暂停"
	case runtime.EventRunPaused:
		return "运行暂停"
	case runtime.EventRunResumed:
		return "运行恢复"
	case runtime.EventRunCancelRequested:
		return "请求取消"
	case runtime.EventRunCanceled:
		return "运行取消"
	case runtime.EventRunFinished:
		return "运行完成"
	case runtime.EventRunFailed:
		return "运行失败"
	case runtime.EventNodeStarted:
		return "节点开始"
	case runtime.EventNodeFinished:
		return "节点完成"
	case runtime.EventNodeFailed:
		return "节点失败"
	case runtime.EventNodeRetry:
		return "节点重试"
	case runtime.EventNodeCustom:
		return "节点自定义事件"
	case runtime.EventLLMReasoningChunk:
		return "LLM 推理片段"
	case runtime.EventLLMContentChunk:
		return "LLM 输出片段"
	case runtime.EventLLMUsage:
		return "LLM 用量"
	case runtime.EventToolCalled:
		return "工具调用"
	case runtime.EventToolReturned:
		return "工具返回"
	case runtime.EventToolFailed:
		return "工具失败"
	case runtime.EventSubgraphStarted:
		return "子图开始"
	case runtime.EventSubgraphFinished:
		return "子图完成"
	case runtime.EventSubgraphFailed:
		return "子图失败"
	case runtime.EventCheckpointCreated:
		return "创建检查点"
	case runtime.EventArtifactCreated:
		return "生成产物"
	case runtime.EventBreakpointHit:
		return "命中断点"
	case runtime.EventStateChanged:
		return "状态变更"
	case runtime.EventContractViolation:
		return "状态契约违规"
	default:
		return string(event.Type)
	}
}

func replaySubtitle(event EventView) string {
	payload := payloadMap(event.Payload)
	switch event.Type {
	case runtime.EventNodeStarted, runtime.EventNodeFinished, runtime.EventNodeFailed:
		nodeName := valueAsString(payload["node_name"])
		if nodeName == "" {
			nodeName = event.NodeID
		}
		return nodeName
	case runtime.EventCheckpointCreated:
		stage := valueAsString(payload["stage"])
		checkpointID := valueAsString(payload["checkpoint_id"])
		return strings.TrimSpace(strings.Join([]string{stage, checkpointID}, " "))
	case runtime.EventArtifactCreated:
		artifactType := valueAsString(payload["type"])
		mimeType := valueAsString(payload["mime_type"])
		return strings.TrimSpace(strings.Join([]string{artifactType, mimeType}, " "))
	case runtime.EventStateChanged:
		if changes, ok := payload["changes"].([]any); ok {
			return fmt.Sprintf("%d 项字段变化", len(changes))
		}
	case runtime.EventRunFailed:
		if message := valueAsString(payload["error_message"]); message != "" {
			return message
		}
	case runtime.EventBreakpointHit:
		stage := valueAsString(payload["stage"])
		nodeID := valueAsString(payload["node_id"])
		return strings.TrimSpace(strings.Join([]string{stage, nodeID}, " "))
	}
	if event.NodeID != "" {
		return event.NodeID
	}
	return ""
}

func payloadMap(payload any) map[string]any {
	mapped, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	return mapped
}

func valueAsString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func artifactSize(location string) int64 {
	info, err := os.Stat(location)
	if err != nil {
		return 0
	}
	return info.Size()
}

func previewArtifact(artifact runtime.Artifact, limit int) (any, string, bool) {
	mimeType := strings.ToLower(strings.TrimSpace(artifact.MIMEType))
	switch {
	case strings.Contains(mimeType, "json"):
		if len(artifact.Data) <= limit {
			payload, err := decodeJSONPayload(artifact.Data)
			if err == nil {
				return payload, "json", false
			}
		}
		text, truncated := truncateString(string(artifact.Data), limit)
		return text, "text", truncated
	case strings.HasPrefix(mimeType, "text/"):
		text, truncated := truncateString(string(artifact.Data), limit)
		return text, "text", truncated
	default:
		encoded := base64.StdEncoding.EncodeToString(artifact.Data)
		preview, truncated := truncateString(encoded, limit)
		return preview, "base64", truncated
	}
}

func truncateString(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit] + "\n...<truncated>", true
}
