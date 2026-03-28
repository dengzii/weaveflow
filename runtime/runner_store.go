package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type FileExecutionStore struct {
	baseDir string
	mu      sync.Mutex
}

type FileCheckpointStore struct {
	baseDir string
	mu      sync.Mutex
}

type FileEventSink struct {
	baseDir string
	mu      sync.Mutex
}

func NewFileExecutionStore(baseDir string) *FileExecutionStore {
	return &FileExecutionStore{baseDir: strings.TrimSpace(baseDir)}
}

func NewFileCheckpointStore(baseDir string) *FileCheckpointStore {
	return &FileCheckpointStore{baseDir: strings.TrimSpace(baseDir)}
}

func NewFileEventSink(baseDir string) *FileEventSink {
	return &FileEventSink{baseDir: strings.TrimSpace(baseDir)}
}

func (s *FileExecutionStore) CreateRun(_ context.Context, run RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.runPath(run.RunID)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("create run %q: already exists", run.RunID)
	}
	return writeRunnerJSONFile(path, run)
}

func (s *FileExecutionStore) UpdateRun(_ context.Context, run RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeRunnerJSONFile(s.runPath(run.RunID), run)
}

func (s *FileExecutionStore) GetRun(_ context.Context, runID string) (RunRecord, error) {
	var run RunRecord
	if err := readRunnerJSONFile(s.runPath(runID), &run); err != nil {
		if os.IsNotExist(err) {
			return RunRecord{}, ErrRunnerRecordNotFound
		}
		return RunRecord{}, err
	}
	return run, nil
}

func (s *FileExecutionStore) ListRuns(_ context.Context, filter RunFilter) ([]RunRecord, error) {
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
		if len(statusFilter) > 0 {
			if _, ok := statusFilter[run.Status]; !ok {
				continue
			}
		}
		items = append(items, run)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items, nil
}

func (s *FileExecutionStore) AppendStep(_ context.Context, step StepRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.stepPath(step.RunID, step.StepID)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("append step %q: already exists", step.StepID)
	}
	return writeRunnerJSONFile(path, step)
}

func (s *FileExecutionStore) UpdateStep(_ context.Context, step StepRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeRunnerJSONFile(s.stepPath(step.RunID, step.StepID), step)
}

func (s *FileExecutionStore) GetStep(_ context.Context, stepID string) (StepRecord, error) {
	runs, err := s.ListRuns(context.Background(), RunFilter{})
	if err != nil {
		return StepRecord{}, err
	}
	for _, run := range runs {
		path := s.stepPath(run.RunID, stepID)
		var step StepRecord
		if err := readRunnerJSONFile(path, &step); err == nil {
			return step, nil
		}
	}
	return StepRecord{}, ErrRunnerRecordNotFound
}

func (s *FileExecutionStore) ListSteps(_ context.Context, runID string) ([]StepRecord, error) {
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.checkpointsDir(record.RunID), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.payloadDir(record.RunID), 0o755); err != nil {
		return err
	}
	record.PayloadRef = s.payloadPath(record.RunID, record.CheckpointID)
	if err := writeRunnerJSONFile(s.metadataPath(record.RunID, record.CheckpointID), record); err != nil {
		return err
	}
	return writeRunnerBinaryFile(record.PayloadRef, payload)
}

func (s *FileCheckpointStore) Load(_ context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
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
		if err := readRunnerJSONFile(metaPath, &record); err == nil {
			payload, err := os.ReadFile(record.PayloadRef)
			if err != nil {
				return CheckpointRecord{}, nil, err
			}
			return record, payload, nil
		}
	}

	return CheckpointRecord{}, nil, ErrRunnerRecordNotFound
}

func (s *FileCheckpointStore) List(_ context.Context, runID string) ([]CheckpointRecord, error) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendRunnerJSONLine(s.eventsPath(event.RunID), event)
}

func (s *FileEventSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := s.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileEventSink) ListEvents(runID string) ([]Event, error) {
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
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *FileEventSink) eventsPath(runID string) string {
	return filepath.Join(s.baseDir, "events", runID+".jsonl")
}

func writeRunnerJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeRunnerBinaryFile(path, data)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}
