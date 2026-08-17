package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dengzii/weaveflow/state"
	"go.uber.org/zap"
)

type CombineEventSink struct {
	sinks []EventSink
}

func NewCombineEventSink(sinks ...EventSink) EventSink {
	return &CombineEventSink{sinks: sinks}
}

func (sink *CombineEventSink) Publish(ctx context.Context, event Event) error {
	for _, nested := range sink.sinks {
		if err := nested.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (sink *CombineEventSink) PublishBatch(ctx context.Context, events []Event) error {
	for _, nested := range sink.sinks {
		if err := nested.PublishBatch(ctx, events); err != nil {
			return err
		}
	}
	return nil
}

func (sink *CombineEventSink) ListEvents(runID string) ([]Event, error) {
	for _, nested := range sink.sinks {
		reader, ok := nested.(EventReader)
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

func (sink *CombineEventSink) ListEventPage(runID, cursor string, limit int) (EventPage, error) {
	for _, nested := range sink.sinks {
		reader, ok := nested.(EventPageReader)
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

	events, err := sink.ListEvents(runID)
	if err != nil {
		return EventPage{}, err
	}
	return PaginateEventsNewestFirst(events, cursor, limit)
}

type LoggerEventSink struct {
	logger *zap.Logger
}

func NewLoggerEventSink(logger *zap.Logger) EventSink {
	return &LoggerEventSink{logger: logger}
}

func (sink *LoggerEventSink) Publish(ctx context.Context, event Event) error {
	if IsStreamingEvent(event.Type) {
		return nil
	}
	event = sanitizeEventPayload(ctx, event)
	sink.logger.Info("Publish",
		zap.Any("type", event.Type),
		zap.String("node_id", event.NodeID),
		zap.ByteString("payload", event.Payload),
		zap.Any("event", event),
	)
	return nil
}

func (sink *LoggerEventSink) PublishBatch(ctx context.Context, events []Event) error {
	sink.logger.Info("EventBatch", zap.Any("events", sanitizeEvents(ctx, events)))
	return nil
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

func fileStoreContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func requireRunDeletionMutation(ctx context.Context, runID, deletionID string) error {
	if err := validateRunnerStorageID("deletion ID", deletionID); err != nil {
		return err
	}
	mutationID := ""
	if ctx != nil {
		mutationID, _ = ctx.Value(runDeletionMutationKey{}).(string)
	}
	if mutationID != deletionID {
		return fmt.Errorf("%w: deletion %q is not authorized for run %q", ErrRunControlNotAllowed, mutationID, runID)
	}
	return nil
}

func RequireRunDeletionMutation(ctx context.Context, runID, deletionID string) error {
	return requireRunDeletionMutation(ctx, runID, deletionID)
}

type NoopExecutionStore struct{}

func NewNoopExecutionStore() *NoopExecutionStore { return &NoopExecutionStore{} }

func (*NoopExecutionStore) CreateRun(context.Context, RunRecord) error { return nil }
func (*NoopExecutionStore) CompareAndSwapRun(_ context.Context, expectedRevision uint64, run RunRecord) (RunRecord, error) {
	run.Revision = expectedRevision + 1
	return run, nil
}
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

var _ ArtifactStore = (*NoopArtifactStore)(nil)
var _ RunDeleter = (*NoopArtifactStore)(nil)
var _ RunDeletionFencer = (*NoopArtifactStore)(nil)

func (*NoopArtifactStore) Save(context.Context, Artifact) (state.ArtifactRef, error) {
	return state.ArtifactRef{}, nil
}
func (*NoopArtifactStore) Load(context.Context, state.ArtifactRef) (Artifact, error) {
	return Artifact{}, ErrRunnerRecordNotFound
}
func (*NoopArtifactStore) List(context.Context, string) ([]state.ArtifactRef, error) {
	return []state.ArtifactRef{}, nil
}
func (*NoopArtifactStore) DeleteRun(ctx context.Context, runID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	return validateRunnerStorageID("run ID", runID)
}
func (*NoopArtifactStore) FenceRunDeletion(ctx context.Context, runID, deletionID string) error {
	if err := fileStoreContextErr(ctx); err != nil {
		return err
	}
	if err := validateRunnerStorageID("run ID", runID); err != nil {
		return err
	}
	return validateRunnerStorageID("deletion ID", deletionID)
}

type NoopEventSink struct{}

func (NoopEventSink) Publish(_ context.Context, _ Event) error        { return nil }
func (NoopEventSink) PublishBatch(_ context.Context, _ []Event) error { return nil }
