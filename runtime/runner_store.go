package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/state"

	"go.uber.org/zap"
)

type FileExecutionStore struct {
	baseDir string
	mu      fileStoreMutex
}

type FileCheckpointStore struct {
	baseDir string
	mu      fileStoreMutex
}

type FileEventSink struct {
	baseDir string
	mu      fileStoreMutex
}

const fileStoreMutexStripeCount = 256

// A fixed stripe set prevents locks for pruned graph-session directories from accumulating forever.
var sharedFileStoreMutexes [fileStoreMutexStripeCount]sync.Mutex

type fileStoreMutex struct {
	baseDir string
	once    sync.Once
	shared  *sync.Mutex
}

type CombineEventSink struct {
	sinks []EventSink
}

func NewCombineEventSink(sinks ...EventSink) EventSink {
	return &CombineEventSink{
		sinks: sinks,
	}
}

func (c *CombineEventSink) Publish(ctx context.Context, event Event) error {
	for _, sink := range c.sinks {
		if err := sink.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (c *CombineEventSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, sink := range c.sinks {
		if err := sink.PublishBatch(ctx, events); err != nil {
			return err
		}
	}
	return nil
}

func (c *CombineEventSink) ListEvents(runID string) ([]Event, error) {
	for _, sink := range c.sinks {
		reader, ok := sink.(EventReader)
		if !ok {
			continue
		}
		events, err := reader.ListEvents(runID)
		if err != nil {
			return nil, err
		}
		return events, nil
	}
	return nil, fmt.Errorf("combine event sink does not support listing events")
}

func (c *CombineEventSink) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	for _, sink := range c.sinks {
		reader, ok := sink.(EventPageReader)
		if !ok {
			continue
		}
		page, err := reader.ListEventPage(runID, cursor, limit)
		if err != nil {
			return EventPage{}, err
		}
		if len(page.Items) > 0 || page.NextCursor != "" {
			return page, nil
		}
	}

	events, err := c.ListEvents(runID)
	if err != nil {
		return EventPage{}, err
	}
	return PaginateEventsNewestFirst(events, cursor, limit)
}

type LoggerEventSink struct {
	logger *zap.Logger
}

func NewLoggerEventSink(logger *zap.Logger) EventSink {
	return &LoggerEventSink{
		logger: logger,
	}
}

func (l *LoggerEventSink) Publish(ctx context.Context, event Event) error {
	if IsStreamingEvent(event.Type) {
		return nil
	}
	l.logger.Info("Publish",
		zap.Any("type", event.Type),
		zap.String("node_id", event.NodeID),
		zap.ByteString("payload", event.Payload),
		zap.Any("event", event),
	)
	return nil
}

func (l *LoggerEventSink) PublishBatch(ctx context.Context, events []Event) error {
	l.logger.Info("EventBatch", zap.Any("events", events))
	return nil
}

func NewFileExecutionStore(baseDir string) *FileExecutionStore {
	baseDir = strings.TrimSpace(baseDir)
	return &FileExecutionStore{baseDir: baseDir, mu: fileStoreMutex{baseDir: baseDir}}
}

func NewFileCheckpointStore(baseDir string) *FileCheckpointStore {
	baseDir = strings.TrimSpace(baseDir)
	return &FileCheckpointStore{baseDir: baseDir, mu: fileStoreMutex{baseDir: baseDir}}
}

func NewFileEventSink(baseDir string) *FileEventSink {
	baseDir = strings.TrimSpace(baseDir)
	return &FileEventSink{baseDir: baseDir, mu: fileStoreMutex{baseDir: baseDir}}
}

func sharedFileStoreMutex(baseDir string) *sync.Mutex {
	key, err := filepath.Abs(baseDir)
	if err != nil {
		key = filepath.Clean(baseDir)
	}
	if goruntime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return &sharedFileStoreMutexes[fileStoreMutexIndex(key)]
}

func fileStoreMutexIndex(value string) int {
	const (
		fnvOffsetBasis uint32 = 2166136261
		fnvPrime       uint32 = 16777619
	)
	hashValue := fnvOffsetBasis
	for index := 0; index < len(value); index++ {
		hashValue ^= uint32(value[index])
		hashValue *= fnvPrime
	}
	return int(hashValue % fileStoreMutexStripeCount)
}

func (m *fileStoreMutex) Lock() {
	m.once.Do(func() {
		m.shared = sharedFileStoreMutex(m.baseDir)
	})
	m.shared.Lock()
}

func (m *fileStoreMutex) Unlock() {
	m.shared.Unlock()
}

func validateRunnerStorageID(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || value == "." || value == ".." || len(value) > 200 {
		return fmt.Errorf("%s must be a portable record ID", name)
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("%s must be a portable record ID", name)
	}
	return nil
}

func (s *FileExecutionStore) CreateRun(_ context.Context, run RunRecord) error {
	if err := validateRunnerStorageID("run ID", run.RunID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.runPath(run.RunID)
	if err := ensureRunnerRecordDoesNotExist(path, "run", run.RunID); err != nil {
		return err
	}
	return writeRunnerJSONFile(path, run)
}

func (s *FileExecutionStore) UpdateRun(_ context.Context, run RunRecord) error {
	if err := validateRunnerStorageID("run ID", run.RunID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.runPath(run.RunID)
	if err := ensureRunnerRecordExists(path, "run", run.RunID); err != nil {
		return err
	}
	return writeRunnerJSONFile(path, run)
}

func (s *FileExecutionStore) GetRun(_ context.Context, runID string) (RunRecord, error) {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return RunRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var run RunRecord
	if err := readRunnerJSONFile(s.runPath(runID), &run); err != nil {
		if os.IsNotExist(err) {
			return RunRecord{}, ErrRunnerRecordNotFound
		}
		return RunRecord{}, err
	}
	if run.RunID != runID {
		return RunRecord{}, fmt.Errorf("run %q metadata identity mismatch", runID)
	}
	return run, nil
}

func (s *FileExecutionStore) ListRuns(_ context.Context, filter RunFilter) ([]RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listRunsLocked(filter)
}

func (s *FileExecutionStore) AppendStep(_ context.Context, step StepRecord) error {
	if err := validateRunnerStorageID("run ID", step.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ensureRunnerRecordExists(s.runPath(step.RunID), "run", step.RunID); err != nil {
		return err
	}
	path := s.stepPath(step.RunID, step.StepID)
	if err := ensureRunnerRecordDoesNotExist(path, "step", step.StepID); err != nil {
		return err
	}
	return writeRunnerJSONFile(path, step)
}

func (s *FileExecutionStore) UpdateStep(_ context.Context, step StepRecord) error {
	if err := validateRunnerStorageID("run ID", step.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.stepPath(step.RunID, step.StepID)
	if err := ensureRunnerRecordExists(path, "step", step.StepID); err != nil {
		return err
	}
	return writeRunnerJSONFile(path, step)
}

func (s *FileExecutionStore) GetStep(_ context.Context, stepID string) (StepRecord, error) {
	if err := validateRunnerStorageID("step ID", stepID); err != nil {
		return StepRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	runs, err := s.listRunsLocked(RunFilter{})
	if err != nil {
		return StepRecord{}, err
	}
	for _, run := range runs {
		path := s.stepPath(run.RunID, stepID)
		var step StepRecord
		err := readRunnerJSONFile(path, &step)
		if err == nil {
			if step.RunID != run.RunID || step.StepID != stepID {
				return StepRecord{}, fmt.Errorf("step %q metadata identity mismatch", stepID)
			}
			return step, nil
		}
		if !os.IsNotExist(err) {
			return StepRecord{}, err
		}
	}
	return StepRecord{}, ErrRunnerRecordNotFound
}

func (s *FileExecutionStore) ListSteps(_ context.Context, runID string) ([]StepRecord, error) {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.stepsDir(runID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []StepRecord{}, nil
	}
	if err != nil {
		return nil, err
	}

	items := make([]StepRecord, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		var step StepRecord
		if err := readRunnerJSONFile(filepath.Join(dir, file.Name()), &step); err != nil {
			return nil, err
		}
		if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
			return nil, err
		}
		if step.RunID != runID || file.Name() != step.StepID+".json" {
			return nil, fmt.Errorf("step %q metadata identity mismatch", step.StepID)
		}
		items = append(items, step)
	}

	sort.Slice(items, func(i, j int) bool {
		left := items[i].StartedAt
		right := items[j].StartedAt
		if left.Equal(right) {
			return items[i].StepID < items[j].StepID
		}
		return left.Before(right)
	})
	return items, nil
}

func (s *FileExecutionStore) DeleteRun(_ context.Context, runID string) error {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.runPath(runID)); err != nil {
		if os.IsNotExist(err) {
			return ErrRunnerRecordNotFound
		}
		return err
	}
	if err := os.RemoveAll(s.stepsDir(runID)); err != nil {
		return err
	}
	if err := os.Remove(s.runPath(runID)); err != nil {
		if os.IsNotExist(err) {
			return ErrRunnerRecordNotFound
		}
		return err
	}
	return nil
}

func (s *FileExecutionStore) listRunsLocked(filter RunFilter) ([]RunRecord, error) {
	dir := s.runsDir()
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []RunRecord{}, nil
	}
	if err != nil {
		return nil, err
	}

	statusFilter := make(map[RunStatus]struct{}, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statusFilter[status] = struct{}{}
	}

	items := make([]RunRecord, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		var run RunRecord
		if err := readRunnerJSONFile(filepath.Join(dir, file.Name()), &run); err != nil {
			return nil, err
		}
		if err := validateRunnerStorageID("run ID", run.RunID); err != nil {
			return nil, err
		}
		if file.Name() != run.RunID+".json" {
			return nil, fmt.Errorf("run %q metadata identity mismatch", run.RunID)
		}
		if len(statusFilter) > 0 {
			if _, ok := statusFilter[run.Status]; !ok {
				continue
			}
		}
		items = append(items, run)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].RunID < items[j].RunID
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items, nil
}

func (s *FileExecutionStore) runPath(runID string) string {
	return filepath.Join(s.runsDir(), runID+".json")
}

func (s *FileExecutionStore) runsDir() string {
	return filepath.Join(s.baseDir, "runs")
}

func (s *FileExecutionStore) stepPath(runID, stepID string) string {
	return filepath.Join(s.stepsDir(runID), stepID+".json")
}

func (s *FileExecutionStore) stepsDir(runID string) string {
	return filepath.Join(s.baseDir, "steps", runID)
}

func (s *FileCheckpointStore) Save(_ context.Context, record CheckpointRecord, payload []byte) error {
	if err := validateRunnerStorageID("run ID", record.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("checkpoint ID", record.CheckpointID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	metadataPath := s.metadataPath(record.RunID, record.CheckpointID)
	if err := ensureRunnerRecordDoesNotExist(metadataPath, "checkpoint", record.CheckpointID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.checkpointsDir(record.RunID), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.payloadDir(record.RunID), 0o755); err != nil {
		return err
	}
	record.PayloadRef = s.payloadPath(record.RunID, record.CheckpointID)
	metadata, err := marshalRunnerJSONFile(record)
	if err != nil {
		return err
	}
	if err := writeRunnerBinaryFile(record.PayloadRef, payload); err != nil {
		return err
	}
	return writeRunnerBinaryFile(metadataPath, metadata)
}

func (s *FileCheckpointStore) Load(_ context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
	if err := validateRunnerStorageID("checkpoint ID", checkpointID); err != nil {
		return CheckpointRecord{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	runDirs, err := os.ReadDir(s.baseDir)
	if os.IsNotExist(err) {
		return CheckpointRecord{}, nil, ErrRunnerRecordNotFound
	}
	if err != nil {
		return CheckpointRecord{}, nil, err
	}

	for _, runDir := range runDirs {
		if !runDir.IsDir() {
			continue
		}
		metaPath := filepath.Join(s.baseDir, runDir.Name(), checkpointID+".json")
		var record CheckpointRecord
		if err := readRunnerJSONFile(metaPath, &record); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return CheckpointRecord{}, nil, err
		}
		if record.RunID != runDir.Name() || record.CheckpointID != checkpointID {
			return CheckpointRecord{}, nil, fmt.Errorf("checkpoint %q metadata identity mismatch", checkpointID)
		}
		payloadPath := s.payloadPath(record.RunID, record.CheckpointID)
		payload, err := os.ReadFile(payloadPath)
		if err != nil {
			return CheckpointRecord{}, nil, err
		}
		record.PayloadRef = payloadPath
		return record, payload, nil
	}

	return CheckpointRecord{}, nil, ErrRunnerRecordNotFound
}

func (s *FileCheckpointStore) List(_ context.Context, runID string) ([]CheckpointRecord, error) {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.checkpointsDir(runID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []CheckpointRecord{}, nil
	}
	if err != nil {
		return nil, err
	}

	items := make([]CheckpointRecord, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		var record CheckpointRecord
		if err := readRunnerJSONFile(filepath.Join(dir, file.Name()), &record); err != nil {
			return nil, err
		}
		if err := validateRunnerStorageID("checkpoint ID", record.CheckpointID); err != nil {
			return nil, err
		}
		if record.RunID != runID || file.Name() != record.CheckpointID+".json" {
			return nil, fmt.Errorf("checkpoint %q metadata identity mismatch", record.CheckpointID)
		}
		record.PayloadRef = s.payloadPath(runID, record.CheckpointID)
		items = append(items, record)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CheckpointID < items[j].CheckpointID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileCheckpointStore) DeleteRun(_ context.Context, runID string) error {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.checkpointsDir(runID))
}

func (s *FileCheckpointStore) checkpointsDir(runID string) string {
	return filepath.Join(s.baseDir, runID)
}

func (s *FileCheckpointStore) payloadDir(runID string) string {
	return filepath.Join(s.baseDir, runID, "payloads")
}

func (s *FileCheckpointStore) metadataPath(runID, checkpointID string) string {
	return filepath.Join(s.checkpointsDir(runID), checkpointID+".json")
}

func (s *FileCheckpointStore) payloadPath(runID, checkpointID string) string {
	return filepath.Join(s.payloadDir(runID), checkpointID+".bin")
}

func (s *FileEventSink) Publish(_ context.Context, event Event) error {
	if event.Type == EventLLMReasoningChunk || event.Type == EventLLMContentChunk {
		return nil
	}
	if err := validateRunnerStorageID("run ID", event.RunID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendRunnerJSONLine(s.eventsPath(event.RunID), event)
}

func (s *FileEventSink) PublishBatch(_ context.Context, events []Event) error {
	type pendingEventLine struct {
		path string
		data []byte
	}
	pending := make([]pendingEventLine, 0, len(events))
	for _, event := range events {
		if event.Type == EventLLMReasoningChunk || event.Type == EventLLMContentChunk {
			continue
		}
		if err := validateRunnerStorageID("run ID", event.RunID); err != nil {
			return err
		}
		data, err := marshalRunnerJSONLine(event)
		if err != nil {
			return err
		}
		pending = append(pending, pendingEventLine{path: s.eventsPath(event.RunID), data: data})
	}
	if len(pending) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range pending {
		if err := appendRunnerJSONLineData(event.path, event.data); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileEventSink) ListEvents(runID string) ([]Event, error) {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.eventsPath(runID)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	items := make([]Event, 0)
	decoder := json.NewDecoder(bufio.NewReader(f))
	for {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if event.RunID != runID {
			return nil, fmt.Errorf("event %q metadata identity mismatch", event.ID)
		}
		items = append(items, event)
	}
	return items, nil
}

func (s *FileEventSink) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	if limit <= 0 {
		return EventPage{}, fmt.Errorf("event page limit must be positive")
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return EventPage{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.eventsPath(runID))
	if os.IsNotExist(err) {
		return EventPage{Items: []Event{}}, nil
	}
	if err != nil {
		return EventPage{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return EventPage{}, err
	}
	offset, err := eventFileCursorOffset(f, info.Size(), cursor)
	if err != nil {
		return EventPage{}, err
	}

	items := make([]Event, 0, limit)
	for offset > 0 && len(items) < limit {
		line, lineStart, err := readPreviousJSONLine(f, offset)
		if err != nil {
			if err == io.EOF {
				break
			}
			return EventPage{}, err
		}
		offset = lineStart
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return EventPage{}, err
		}
		if event.RunID != runID {
			return EventPage{}, fmt.Errorf("event %q metadata identity mismatch", event.ID)
		}
		items = append(items, event)
	}

	nextCursor := ""
	if offset > 0 {
		nextCursor = strconv.FormatInt(offset, 10)
	}
	return EventPage{Items: items, NextCursor: nextCursor}, nil
}

func PaginateEventsNewestFirst(events []Event, cursor string, limit int) (EventPage, error) {
	if limit <= 0 {
		return EventPage{}, fmt.Errorf("event page limit must be positive")
	}
	if len(events) == 0 {
		return EventPage{Items: []Event{}}, nil
	}

	start := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 || parsed > len(events) {
			return EventPage{}, fmt.Errorf("%w: %q", ErrInvalidEventCursor, cursor)
		}
		start = parsed
	}

	end := min(start+limit, len(events))
	items := make([]Event, 0, end-start)
	for index := start; index < end; index++ {
		items = append(items, cloneEvent(events[len(events)-1-index]))
	}
	nextCursor := ""
	if end < len(events) {
		nextCursor = strconv.Itoa(end)
	}
	return EventPage{Items: items, NextCursor: nextCursor}, nil
}

func eventFileCursorOffset(file *os.File, size int64, cursor string) (int64, error) {
	if cursor == "" {
		return size, nil
	}
	offset, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || offset < 0 || offset > size {
		return 0, fmt.Errorf("%w: %q", ErrInvalidEventCursor, cursor)
	}
	if offset == 0 || offset == size {
		return offset, nil
	}
	var previous [1]byte
	if _, err := file.ReadAt(previous[:], offset-1); err != nil {
		return 0, err
	}
	if previous[0] != '\n' {
		return 0, fmt.Errorf("%w: %q", ErrInvalidEventCursor, cursor)
	}
	return offset, nil
}

func readPreviousJSONLine(file *os.File, end int64) ([]byte, int64, error) {
	if end <= 0 {
		return nil, 0, io.EOF
	}

	lineEnd := end
	var value [1]byte
	for lineEnd > 0 {
		if _, err := file.ReadAt(value[:], lineEnd-1); err != nil {
			return nil, 0, err
		}
		if value[0] != '\n' && value[0] != '\r' {
			break
		}
		lineEnd--
	}
	if lineEnd == 0 {
		return nil, 0, io.EOF
	}

	const blockSize int64 = 64 * 1024
	parts := make([][]byte, 0, 1)
	total := 0
	scanEnd := lineEnd
	lineStart := int64(0)
	for scanEnd > 0 {
		scanStart := max(int64(0), scanEnd-blockSize)
		part := make([]byte, scanEnd-scanStart)
		if _, err := file.ReadAt(part, scanStart); err != nil {
			return nil, 0, err
		}
		if separator := bytes.LastIndexByte(part, '\n'); separator >= 0 {
			lineStart = scanStart + int64(separator) + 1
			part = part[separator+1:]
			parts = append(parts, part)
			total += len(part)
			break
		}
		parts = append(parts, part)
		total += len(part)
		scanEnd = scanStart
	}

	line := make([]byte, 0, total)
	for index := len(parts) - 1; index >= 0; index-- {
		line = append(line, parts[index]...)
	}
	return line, lineStart, nil
}

func (s *FileEventSink) DeleteRun(_ context.Context, runID string) error {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.eventsPath(runID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileEventSink) eventsPath(runID string) string {
	return filepath.Join(s.baseDir, runID+".jsonl")
}

func writeRunnerJSONFile(path string, value any) error {
	data, err := marshalRunnerJSONFile(value)
	if err != nil {
		return err
	}
	return writeRunnerBinaryFile(path, data)
}

func ensureRunnerRecordDoesNotExist(path, recordType, recordID string) error {
	_, err := os.Stat(path)
	if err == nil {
		return fmt.Errorf("%s %q already exists", recordType, recordID)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s %q: %w", recordType, recordID, err)
	}
	return nil
}

func ensureRunnerRecordExists(path, recordType, recordID string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("%s %q: %w", recordType, recordID, ErrRunnerRecordNotFound)
	}
	return fmt.Errorf("inspect %s %q: %w", recordType, recordID, err)
}

func marshalRunnerJSONFile(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readRunnerJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func writeRunnerBinaryFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func appendRunnerJSONLine(path string, value any) error {
	data, err := marshalRunnerJSONLine(value)
	if err != nil {
		return err
	}
	return appendRunnerJSONLineData(path, data)
}

func marshalRunnerJSONLine(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func appendRunnerJSONLineData(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}
	return nil
}

type NoopExecutionStore struct{}

func NewNoopExecutionStore() *NoopExecutionStore { return &NoopExecutionStore{} }

func (*NoopExecutionStore) CreateRun(context.Context, RunRecord) error { return nil }
func (*NoopExecutionStore) UpdateRun(context.Context, RunRecord) error { return nil }
func (*NoopExecutionStore) GetRun(context.Context, string) (RunRecord, error) {
	return RunRecord{}, ErrRunnerRecordNotFound
}
func (*NoopExecutionStore) ListRuns(context.Context, RunFilter) ([]RunRecord, error) {
	return []RunRecord{}, nil
}
func (*NoopExecutionStore) AppendStep(context.Context, StepRecord) error { return nil }
func (*NoopExecutionStore) UpdateStep(context.Context, StepRecord) error { return nil }
func (*NoopExecutionStore) GetStep(context.Context, string) (StepRecord, error) {
	return StepRecord{}, ErrRunnerRecordNotFound
}
func (*NoopExecutionStore) ListSteps(context.Context, string) ([]StepRecord, error) {
	return []StepRecord{}, nil
}

type NoopCheckpointStore struct{}

func NewNoopCheckpointStore() *NoopCheckpointStore { return &NoopCheckpointStore{} }

func (*NoopCheckpointStore) Save(context.Context, CheckpointRecord, []byte) error { return nil }
func (*NoopCheckpointStore) Load(context.Context, string) (CheckpointRecord, []byte, error) {
	return CheckpointRecord{}, nil, ErrRunnerRecordNotFound
}
func (*NoopCheckpointStore) List(context.Context, string) ([]CheckpointRecord, error) {
	return []CheckpointRecord{}, nil
}

type NoopArtifactStore struct{}

func NewNoopArtifactStore() *NoopArtifactStore { return &NoopArtifactStore{} }

func (*NoopArtifactStore) Save(context.Context, Artifact) (state.ArtifactRef, error) {
	return state.ArtifactRef{}, nil
}
func (*NoopArtifactStore) Load(context.Context, state.ArtifactRef) (Artifact, error) {
	return Artifact{}, ErrRunnerRecordNotFound
}
func (*NoopArtifactStore) List(context.Context, string) ([]state.ArtifactRef, error) {
	return []state.ArtifactRef{}, nil
}

type NoopEventSink struct{}

func (NoopEventSink) Publish(_ context.Context, _ Event) error        { return nil }
func (NoopEventSink) PublishBatch(_ context.Context, _ []Event) error { return nil }
