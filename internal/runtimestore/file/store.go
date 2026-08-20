package file

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type executionStore struct {
	baseDir     string
	manifestDir string
	mu          storeMutex
	writer      *writerState
}

type checkpointStore struct {
	baseDir string
	mu      storeMutex
	writer  *writerState
}

type eventSink struct {
	baseDir string
	mu      storeMutex
	writer  *writerState
}

type storeMutex struct {
	shared *sync.Mutex
}

const (
	maxRunnerJSONFileBytes = 64 << 20
	maxEventPageLimit      = 10_000
)

func newExecutionStore(baseDir string, shared *sync.Mutex) *executionStore {
	baseDir = strings.TrimSpace(baseDir)
	return &executionStore{
		baseDir:     baseDir,
		manifestDir: filepath.Join(filepath.Dir(baseDir), ".deletions", "manifests"),
		mu:          storeMutex{shared: shared},
	}
}

func newCheckpointStore(baseDir string, shared *sync.Mutex) *checkpointStore {
	baseDir = strings.TrimSpace(baseDir)
	baseDir = namespacedFileStoreBase(baseDir, "checkpoints")
	return &checkpointStore{baseDir: baseDir, mu: storeMutex{shared: shared}}
}

func newEventSink(baseDir string, shared *sync.Mutex) *eventSink {
	baseDir = strings.TrimSpace(baseDir)
	return &eventSink{baseDir: baseDir, mu: storeMutex{shared: shared}}
}

func namespacedFileStoreBase(baseDir, namespace string) string {
	if baseDir == "" {
		return baseDir
	}
	if strings.EqualFold(filepath.Base(filepath.Clean(baseDir)), namespace) {
		return baseDir
	}
	return filepath.Join(baseDir, namespace)
}

func (m *storeMutex) Lock() {
	m.shared.Lock()
}

func (m *storeMutex) Unlock() {
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

func storeContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

const runDeletionDirName = ".deletions"

type runDeletionFence struct {
	RunID      string `json:"run_id"`
	DeletionID string `json:"deletion_id"`
}

func validateRunID(runID string) error {
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	if strings.EqualFold(runID, runDeletionDirName) {
		return fmt.Errorf("run ID %q is reserved by file storage", runID)
	}
	return nil
}

func fenceRunDeletionLocked(ctx context.Context, baseDir, runID, deletionID string) error {
	if err := requireRunDeletionMutation(ctx, runID, deletionID); err != nil {
		return err
	}
	path := runDeletionPath(baseDir, runID)
	var existing runDeletionFence
	if err := readRunnerJSONFile(path, &existing); err == nil {
		if err := validateRunDeletionFence(existing, runID); err != nil {
			return err
		}
		if existing.DeletionID != deletionID {
			return fmt.Errorf("%w: run %q is fenced by deletion %q", ErrRunControlNotAllowed, runID, existing.DeletionID)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeRunnerJSONFile(path, runDeletionFence{RunID: runID, DeletionID: deletionID})
}

func ensureRunNotDeletingLocked(baseDir, runID, action string) error {
	var fence runDeletionFence
	if err := readRunnerJSONFile(runDeletionPath(baseDir, runID), &fence); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := validateRunDeletionFence(fence, runID); err != nil {
		return err
	}
	return fmt.Errorf("%w: run %q is fenced for deletion and cannot %s", ErrRunControlNotAllowed, runID, action)
}

func requireRunDeletionLocked(ctx context.Context, baseDir, runID string) error {
	var fence runDeletionFence
	if err := readRunnerJSONFile(runDeletionPath(baseDir, runID), &fence); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: run %q has no durable deletion fence", ErrRunControlNotAllowed, runID)
		}
		return err
	}
	if err := validateRunDeletionFence(fence, runID); err != nil {
		return err
	}
	return requireRunDeletionMutation(ctx, runID, fence.DeletionID)
}

func validateRunDeletionFence(fence runDeletionFence, runID string) error {
	if fence.RunID != runID {
		return fmt.Errorf("run %q deletion fence identity mismatch", runID)
	}
	if err := validateRunnerStorageID("deletion ID", fence.DeletionID); err != nil {
		return fmt.Errorf("run %q deletion fence: %w", runID, err)
	}
	return nil
}

func requireRunDeletionMutation(ctx context.Context, runID, deletionID string) error {
	return requireRuntimeRunDeletionMutation(ctx, runID, deletionID)
}

func runDeletionPath(baseDir, runID string) string {
	return safeRunnerPath(baseDir, runDeletionDirName, runID+".json")
}

func (s *executionStore) CreateRun(ctx context.Context, run RunRecord) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(run.RunID); err != nil {
		return err
	}
	if err := validateNewRunDeletion(run); err != nil {
		return err
	}
	run = sanitizeRunRecord(ctx, run)
	if err := validateRunChildState(run); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, run.RunID, "create a run"); err != nil {
		return err
	}

	path := s.runPath(run.RunID)
	if err := ensureRunnerRecordDoesNotExist(path, "run", run.RunID); err != nil {
		return err
	}
	if run.ParentRunID != "" {
		if err := validateRunID(run.ParentRunID); err != nil {
			return err
		}
		if err := ensureRunNotDeletingLocked(s.baseDir, run.ParentRunID, "create a child run"); err != nil {
			return err
		}
		parent, err := s.readRunLocked(run.ParentRunID)
		if err != nil {
			return fmt.Errorf("load parent run %q: %w", run.ParentRunID, err)
		}
		if err := validateNewRunParent(run, parent); err != nil {
			return err
		}
	}
	return writeRunnerJSONFile(path, run)
}

func (s *executionStore) CompareAndSwapRun(ctx context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	if err := storeContextErr(ctx); err != nil {
		return RunRecord{}, err
	}
	if err := validateRunID(run.RunID); err != nil {
		return RunRecord{}, err
	}
	run = sanitizeRunRecord(ctx, run)
	if err := validateRunChildState(run); err != nil {
		return RunRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return RunRecord{}, err
	}
	path := s.runPath(run.RunID)
	if err := ensureRunnerRecordExists(path, "run", run.RunID); err != nil {
		return RunRecord{}, err
	}
	var existing RunRecord
	if err := readRunnerJSONFile(path, &existing); err != nil {
		return RunRecord{}, err
	}
	if existing.Revision != expectedRevision || run.Revision != expectedRevision {
		return RunRecord{}, &RunRevisionConflictError{RunID: run.RunID, Expected: expectedRevision, Actual: existing.Revision}
	}
	if err := validateRunDeletionTransition(ctx, existing, run); err != nil {
		return RunRecord{}, err
	}
	if err := validateRunExecutionLeaseTransition(ctx, existing, run); err != nil {
		return RunRecord{}, err
	}
	run.Revision = expectedRevision + 1
	if err := writeRunnerJSONFile(path, run); err != nil {
		return RunRecord{}, err
	}
	return run, nil
}

func (s *executionStore) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	if err := storeContextErr(ctx); err != nil {
		return RunRecord{}, err
	}
	if err := validateRunID(runID); err != nil {
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

func (s *executionStore) ListRuns(ctx context.Context, filter RunFilter) ([]RunRecord, error) {
	if err := storeContextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listRunsLocked(filter)
}

func (s *executionStore) AppendStep(ctx context.Context, step StepRecord) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(step.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
		return err
	}
	step = sanitizeStepRecord(ctx, step)
	if err := validateStepEffect(step); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}

	run, err := s.readRunLocked(step.RunID)
	if err != nil {
		return err
	}
	if err := ensureRunNotDeleting(run, "append a step"); err != nil {
		return err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, step.RunID, "append a step"); err != nil {
		return err
	}
	path := s.stepPath(step.RunID, step.StepID)
	if err := ensureRunnerRecordDoesNotExist(path, "step", step.StepID); err != nil {
		return err
	}
	return writeRunnerJSONFile(path, step)
}

func (s *executionStore) UpdateStep(ctx context.Context, step StepRecord) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(step.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("step ID", step.StepID); err != nil {
		return err
	}
	step = sanitizeStepRecord(ctx, step)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	run, err := s.readRunLocked(step.RunID)
	if err != nil {
		return err
	}
	if err := ensureRunNotDeleting(run, "update a step"); err != nil {
		return err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, step.RunID, "update a step"); err != nil {
		return err
	}
	path := s.stepPath(step.RunID, step.StepID)
	if err := ensureRunnerRecordExists(path, "step", step.StepID); err != nil {
		return err
	}
	var existing StepRecord
	if err := readRunnerJSONFile(path, &existing); err != nil {
		return err
	}
	if err := validateStepEffectTransition(existing, step); err != nil {
		return err
	}
	return writeRunnerJSONFile(path, step)
}

func (s *executionStore) GetStep(ctx context.Context, stepID string) (StepRecord, error) {
	if err := storeContextErr(ctx); err != nil {
		return StepRecord{}, err
	}
	if err := validateRunnerStorageID("step ID", stepID); err != nil {
		return StepRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	runs, err := s.listRunsLocked(RunFilter{})
	if err != nil {
		return StepRecord{}, err
	}
	var found *StepRecord
	for _, run := range runs {
		path := s.stepPath(run.RunID, stepID)
		var step StepRecord
		err := readRunnerJSONFile(path, &step)
		if err == nil {
			if step.RunID != run.RunID || step.StepID != stepID {
				return StepRecord{}, fmt.Errorf("step %q metadata identity mismatch", stepID)
			}
			if found != nil {
				return StepRecord{}, fmt.Errorf("step %q is ambiguous across runs", stepID)
			}
			copy := cloneStepRecord(step)
			found = &copy
			continue
		}
		if !os.IsNotExist(err) {
			return StepRecord{}, err
		}
	}
	if found != nil {
		return *found, nil
	}
	return StepRecord{}, ErrRunnerRecordNotFound
}

func (s *executionStore) ListSteps(ctx context.Context, runID string) ([]StepRecord, error) {
	if err := storeContextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
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
		if err := readRunnerJSONFile(safeRunnerPath(dir, file.Name()), &step); err != nil {
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

func (s *executionStore) DeleteRun(ctx context.Context, runID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	run, err := s.readRunLocked(runID)
	if errors.Is(err, ErrRunnerRecordNotFound) {
		return requireRunDeletionLocked(ctx, s.baseDir, runID)
	}
	if err != nil {
		return err
	}
	if run.Deletion == nil {
		return fmt.Errorf("%w: run %q is not reserved for deletion", ErrRunControlNotAllowed, runID)
	}
	if err := validateRunDeletionState(run.Deletion); err != nil {
		return fmt.Errorf("run %q deletion state: %w", runID, err)
	}
	if err := requireRunDeletionMutation(ctx, runID, run.Deletion.ID); err != nil {
		return err
	}
	if err := requireRunDeletionLocked(ctx, s.baseDir, runID); err != nil {
		return err
	}
	if err := removeRunnerDirectory(s.stepsDir(runID)); err != nil {
		return err
	}
	if err := removeRunnerFile(s.runPath(runID)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

func (s *executionStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	run, err := s.readRunLocked(runID)
	if err == nil {
		if run.Deletion == nil {
			return fmt.Errorf("%w: run %q is not reserved for deletion", ErrRunControlNotAllowed, runID)
		}
		if err := validateRunDeletionState(run.Deletion); err != nil {
			return fmt.Errorf("run %q deletion state: %w", runID, err)
		}
		if run.Deletion.ID != deletionID {
			return fmt.Errorf("%w: run %q is reserved by deletion %q", ErrRunControlNotAllowed, runID, run.Deletion.ID)
		}
	} else if !errors.Is(err, ErrRunnerRecordNotFound) {
		return err
	}
	return fenceRunDeletionLocked(ctx, s.baseDir, runID, deletionID)
}

func (s *executionStore) readRunLocked(runID string) (RunRecord, error) {
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

func (s *executionStore) listRunsLocked(filter RunFilter) ([]RunRecord, error) {
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
		if err := readRunnerJSONFile(safeRunnerPath(dir, file.Name()), &run); err != nil {
			return nil, err
		}
		if err := validateRunID(run.RunID); err != nil {
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
		if filter.ParentRunID != "" && run.ParentRunID != filter.ParentRunID {
			continue
		}
		if filter.ParentTaskID != "" && run.ParentTaskID != filter.ParentTaskID {
			continue
		}
		if filter.RootRunID != "" && run.RootRunID != filter.RootRunID {
			continue
		}
		if filter.Namespace != "" && run.Namespace != filter.Namespace {
			continue
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

func (s *executionStore) runPath(runID string) string {
	return safeRunnerPath(s.runsDir(), runID+".json")
}

func (s *executionStore) runsDir() string {
	return safeRunnerPath(s.baseDir, "runs")
}

func (s *executionStore) stepPath(runID, stepID string) string {
	return safeRunnerPath(s.stepsDir(runID), stepID+".json")
}

func (s *executionStore) stepsDir(runID string) string {
	return safeRunnerPath(s.baseDir, "steps", runID)
}

func (s *executionStore) deletionPath(runID string) string {
	return runDeletionPath(s.baseDir, runID)
}

func (s *checkpointStore) Save(ctx context.Context, record CheckpointRecord, payload []byte) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(record.RunID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("checkpoint ID", record.CheckpointID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, record.RunID, "save a checkpoint"); err != nil {
		return err
	}

	metadataPath := s.metadataPath(record.RunID, record.CheckpointID)
	if err := ensureRunnerRecordDoesNotExist(metadataPath, "checkpoint", record.CheckpointID); err != nil {
		return err
	}
	if err := ensureRunnerDirectory(s.checkpointsDir(record.RunID)); err != nil {
		return err
	}
	if err := ensureRunnerDirectory(s.payloadDir(record.RunID)); err != nil {
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
	if err := writeRunnerBinaryFile(metadataPath, metadata); err != nil {
		if cleanupErr := removeRunnerFile(record.PayloadRef); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("cleanup checkpoint payload: %w", cleanupErr))
		}
		return err
	}
	return nil
}

func (s *checkpointStore) Load(ctx context.Context, checkpointID string) (CheckpointRecord, []byte, error) {
	if err := storeContextErr(ctx); err != nil {
		return CheckpointRecord{}, nil, err
	}
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

	var foundRecord *CheckpointRecord
	var foundPayload []byte
	for _, runDir := range runDirs {
		if !runDir.IsDir() || runDir.Name() == runDeletionDirName {
			continue
		}
		metaPath := safeRunnerPath(s.baseDir, runDir.Name(), checkpointID+".json")
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
		if foundRecord != nil {
			return CheckpointRecord{}, nil, fmt.Errorf("checkpoint %q is ambiguous across runs", checkpointID)
		}
		record.PayloadRef = payloadPath
		copy := record
		foundRecord = &copy
		foundPayload = append([]byte(nil), payload...)
	}
	if foundRecord != nil {
		return *foundRecord, foundPayload, nil
	}

	return CheckpointRecord{}, nil, ErrRunnerRecordNotFound
}

func (s *checkpointStore) List(ctx context.Context, runID string) ([]CheckpointRecord, error) {
	if err := storeContextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
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
		if err := readRunnerJSONFile(safeRunnerPath(dir, file.Name()), &record); err != nil {
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

func (s *checkpointStore) DeleteRun(ctx context.Context, runID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	if err := requireRunDeletionLocked(ctx, s.baseDir, runID); err != nil {
		return err
	}
	return removeRunnerDirectory(s.checkpointsDir(runID))
}

func (s *checkpointStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	return fenceRunDeletionLocked(ctx, s.baseDir, runID, deletionID)
}

func (s *checkpointStore) checkpointsDir(runID string) string {
	return safeRunnerPath(s.baseDir, runID)
}

func (s *checkpointStore) payloadDir(runID string) string {
	return safeRunnerPath(s.baseDir, runID, "payloads")
}

func (s *checkpointStore) metadataPath(runID, checkpointID string) string {
	return safeRunnerPath(s.checkpointsDir(runID), checkpointID+".json")
}

func (s *checkpointStore) payloadPath(runID, checkpointID string) string {
	return safeRunnerPath(s.payloadDir(runID), checkpointID+".bin")
}

func (s *checkpointStore) deletionPath(runID string) string {
	return runDeletionPath(s.baseDir, runID)
}

func (s *eventSink) Publish(ctx context.Context, event Event) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if event.Type == EventLLMReasoningChunk || event.Type == EventLLMContentChunk {
		return nil
	}
	event = sanitizeEventPayload(ctx, event)
	if err := validateRunID(event.RunID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	if err := ensureRunNotDeletingLocked(s.baseDir, event.RunID, "publish an event"); err != nil {
		return err
	}
	return appendRunnerJSONLine(s.eventsPath(event.RunID), event)
}

func (s *eventSink) PublishBatch(ctx context.Context, events []Event) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	type pendingEventLine struct {
		runID string
		path  string
		data  []byte
	}
	events = sanitizeEvents(ctx, events)
	pending := make([]pendingEventLine, 0, len(events))
	for _, event := range events {
		if event.Type == EventLLMReasoningChunk || event.Type == EventLLMContentChunk {
			continue
		}
		if err := validateRunID(event.RunID); err != nil {
			return err
		}
		data, err := marshalRunnerJSONLine(event)
		if err != nil {
			return err
		}
		pending = append(pending, pendingEventLine{runID: event.RunID, path: s.eventsPath(event.RunID), data: data})
	}
	if len(pending) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	checkedRuns := make(map[string]struct{}, len(pending))
	for _, event := range pending {
		if _, checked := checkedRuns[event.runID]; checked {
			continue
		}
		if err := ensureRunNotDeletingLocked(s.baseDir, event.runID, "publish events"); err != nil {
			return err
		}
		checkedRuns[event.runID] = struct{}{}
	}
	for _, event := range pending {
		if err := appendRunnerJSONLineData(event.path, event.data); err != nil {
			return err
		}
	}
	return nil
}

func (s *eventSink) ListEvents(runID string) ([]Event, error) {
	if err := validateRunID(runID); err != nil {
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
	defer func() { _ = f.Close() }()

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

func (s *eventSink) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	if limit <= 0 || limit > maxEventPageLimit {
		return EventPage{}, fmt.Errorf("event page limit must be between 1 and %d", maxEventPageLimit)
	}
	if err := validateRunID(runID); err != nil {
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
	defer func() { _ = f.Close() }()

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

func (s *eventSink) DeleteRun(ctx context.Context, runID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	if err := requireRunDeletionLocked(ctx, s.baseDir, runID); err != nil {
		return err
	}
	if err := removeRunnerFile(s.eventsPath(runID)); err != nil {
		return err
	}
	return nil
}

func (s *eventSink) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := storeContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWritable(s.writer); err != nil {
		return err
	}
	return fenceRunDeletionLocked(ctx, s.baseDir, runID, deletionID)
}

func (s *eventSink) eventsPath(runID string) string {
	return safeRunnerPath(s.baseDir, runID+".jsonl")
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
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func readRunnerBinaryFile(path string) ([]byte, error) {
	if err := validateRunnerPath(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxRunnerJSONFileBytes {
		return nil, fmt.Errorf("runner binary file is too large")
	}
	return os.ReadFile(path)
}

func writeRunnerBinaryFile(path string, data []byte) error {
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := ensureRunnerDirectory(directory); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, "tmp-*")
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
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func ensureRunnerDirectory(path string) error {
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	path = filepath.Clean(path)
	missing := make([]string, 0, 2)
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("runtime store path is not a directory: %s", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := syncDirectory(filepath.Dir(missing[index])); err != nil {
			return err
		}
	}
	return nil
}

func removeRunnerFile(path string) error {
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func removeRunnerDirectory(path string) error {
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		return nil
	}
	return syncDirectory(parent)
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
	if err := validateRunnerPath(path); err != nil {
		return err
	}
	if len(data) > maxRunnerJSONFileBytes {
		return fmt.Errorf("runner JSON line is too large")
	}
	info, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if statErr == nil && info.Size() > maxRunnerJSONFileBytes-int64(len(data)) {
		return fmt.Errorf("runner JSON file is too large")
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(existing) > maxRunnerJSONFileBytes-len(data) {
		return fmt.Errorf("runner JSON file is too large")
	}
	combined := make([]byte, 0)
	combined = append(combined, existing...)
	combined = append(combined, data...)
	return writeRunnerBinaryFile(path, combined)
}

func validateRunnerPath(path string) error {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return fmt.Errorf("runner storage path is invalid")
	}
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return fmt.Errorf("runner storage path escapes its root")
		}
	}
	return nil
}

func safeRunnerPath(base string, components ...string) string {
	if strings.TrimSpace(base) == "" {
		return ""
	}
	safeComponents := make([]string, len(components))
	for index, component := range components {
		baseComponent := filepath.Base(component)
		if component == "" || component == ".." || strings.Contains(component, "../") || strings.Contains(component, `..\`) || strings.Contains(component, "/") || strings.Contains(component, "\\") || baseComponent != component {
			return ""
		}
		safeComponents[index] = baseComponent
	}
	return filepath.Join(append([]string{base}, safeComponents...)...)
}
