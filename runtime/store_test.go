package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/dengzii/weaveflow/state"
	"go.uber.org/zap"
)

type failingEventStoreSink struct {
	err error
}

func (sink failingEventStoreSink) Publish(context.Context, Event) error {
	return sink.err
}

func (sink failingEventStoreSink) PublishBatch(context.Context, []Event) error {
	return sink.err
}

type fallbackPageEventSink struct {
	events []Event
}

func (*fallbackPageEventSink) Publish(context.Context, Event) error { return nil }

func (*fallbackPageEventSink) PublishBatch(context.Context, []Event) error { return nil }

func (sink *fallbackPageEventSink) ListEvents(string) ([]Event, error) {
	return cloneEvents(sink.events), nil
}

func (*fallbackPageEventSink) ListEventPage(string, string, int) (EventPage, error) {
	return EventPage{Items: []Event{}}, nil
}

func TestCombineEventSinkPublishesReadsAndPropagatesFailures(t *testing.T) {
	analyzer := NewEventAnalyzer()
	combined := NewCombineEventSink(analyzer, NoopEventSink{})
	if err := combined.Publish(context.Background(), Event{ID: "event-1", RunID: "run", Type: EventRunStarted}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := combined.PublishBatch(context.Background(), []Event{{ID: "event-2", RunID: "run", Type: EventRunFinished}}); err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	events, err := combined.(EventReader).ListEvents("run")
	if err != nil || len(events) != 2 || events[0].ID != "event-1" || events[1].ID != "event-2" {
		t.Fatalf("ListEvents() = %#v, %v", events, err)
	}
	page, err := combined.(EventPageReader).ListEventPage("run", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "event-2" || page.NextCursor != "1" {
		t.Fatalf("ListEventPage() = %#v, %v", page, err)
	}

	fallback := NewCombineEventSink(&fallbackPageEventSink{events: events})
	page, err = fallback.(EventPageReader).ListEventPage("run", "1", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "event-1" || page.NextCursor != "" {
		t.Fatalf("fallback ListEventPage() = %#v, %v", page, err)
	}
	unsupported := NewCombineEventSink(NoopEventSink{})
	if _, err := unsupported.(EventReader).ListEvents("run"); err == nil {
		t.Fatal("ListEvents() succeeded without an EventReader")
	}

	wantErr := errors.New("sink failed")
	failing := NewCombineEventSink(failingEventStoreSink{err: wantErr}, analyzer)
	if err := failing.Publish(context.Background(), Event{}); !errors.Is(err, wantErr) {
		t.Fatalf("failing Publish() error = %v", err)
	}
	if err := failing.PublishBatch(context.Background(), []Event{{}}); !errors.Is(err, wantErr) {
		t.Fatalf("failing PublishBatch() error = %v", err)
	}
}

func TestLoggerEventSinkAndPaginationContracts(t *testing.T) {
	loggerSink := NewLoggerEventSink(zap.NewNop())
	if err := loggerSink.Publish(context.Background(), Event{Type: EventLLMContentChunk}); err != nil {
		t.Fatalf("streaming Publish() error = %v", err)
	}
	if err := loggerSink.Publish(context.Background(), Event{Type: EventRunStarted}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := loggerSink.PublishBatch(context.Background(), []Event{{Type: EventRunFinished}}); err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}

	events := []Event{{ID: "oldest", Payload: []byte(`{"value":1}`)}, {ID: "middle"}, {ID: "newest"}}
	page, err := PaginateEventsNewestFirst(events, "", 2)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "newest" || page.Items[1].ID != "middle" || page.NextCursor != "2" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	page.Items[1].ID = "changed"
	if events[1].ID != "middle" {
		t.Fatalf("pagination aliased source events: %#v", events)
	}
	page, err = PaginateEventsNewestFirst(events, "2", 2)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "oldest" || page.NextCursor != "" {
		t.Fatalf("second page = %#v, %v", page, err)
	}
	empty, err := PaginateEventsNewestFirst(nil, "", 1)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty page = %#v, %v", empty, err)
	}
	for _, cursor := range []string{"bad", "-1", "4"} {
		if _, err := PaginateEventsNewestFirst(events, cursor, 1); !errors.Is(err, ErrInvalidEventCursor) {
			t.Fatalf("cursor %q error = %v", cursor, err)
		}
	}
	if _, err := PaginateEventsNewestFirst(events, "", 0); err == nil {
		t.Fatal("zero page limit was accepted")
	}
}

func TestNoopStoresImplementStableEmptyContracts(t *testing.T) {
	ctx := context.Background()
	execution := NewNoopExecutionStore()
	if err := execution.CreateRun(ctx, RunRecord{RunID: "run"}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	updated, err := execution.CompareAndSwapRun(ctx, 4, RunRecord{RunID: "run"})
	if err != nil || updated.Revision != 5 {
		t.Fatalf("CompareAndSwapRun() = %#v, %v", updated, err)
	}
	if _, err := execution.GetRun(ctx, "run"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetRun() error = %v", err)
	}
	if runs, err := execution.ListRuns(ctx, RunFilter{}); err != nil || runs == nil || len(runs) != 0 {
		t.Fatalf("ListRuns() = %#v, %v", runs, err)
	}
	if err := execution.AppendStep(ctx, StepRecord{}); err != nil {
		t.Fatalf("AppendStep() error = %v", err)
	}
	if err := execution.UpdateStep(ctx, StepRecord{}); err != nil {
		t.Fatalf("UpdateStep() error = %v", err)
	}
	if _, err := execution.GetStep(ctx, "step"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("GetStep() error = %v", err)
	}
	if steps, err := execution.ListSteps(ctx, "run"); err != nil || steps == nil || len(steps) != 0 {
		t.Fatalf("ListSteps() = %#v, %v", steps, err)
	}

	checkpoints := NewNoopCheckpointStore()
	if err := checkpoints.Save(ctx, CheckpointRecord{}, nil); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, _, err := checkpoints.Load(ctx, "checkpoint"); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("Load() error = %v", err)
	}
	if records, err := checkpoints.List(ctx, "run"); err != nil || records == nil || len(records) != 0 {
		t.Fatalf("checkpoint List() = %#v, %v", records, err)
	}

	artifacts := NewNoopArtifactStore()
	stage, err := artifacts.Stage(ctx, "transaction", Artifact{})
	if err != nil || stage.TransactionID != "transaction" {
		t.Fatalf("Stage() = %#v, %v", stage, err)
	}
	if err := artifacts.Finalize(ctx, "transaction", []ArtifactStage{stage}); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if err := artifacts.Discard(ctx, "transaction"); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	if _, err := artifacts.Load(ctx, state.ArtifactRef{ID: "artifact"}); !errors.Is(err, ErrRunnerRecordNotFound) {
		t.Fatalf("artifact Load() error = %v", err)
	}
	if refs, err := artifacts.List(ctx, "run"); err != nil || refs == nil || len(refs) != 0 {
		t.Fatalf("artifact List() = %#v, %v", refs, err)
	}
	if err := artifacts.DeleteRun(ctx, "run-1"); err != nil {
		t.Fatalf("DeleteRun() error = %v", err)
	}
	if err := artifacts.FenceRunDeletion(ctx, "run-1", "deletion-1"); err != nil {
		t.Fatalf("FenceRunDeletion() error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := artifacts.DeleteRun(canceled, "run-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled DeleteRun() error = %v", err)
	}
	if err := artifacts.FenceRunDeletion(ctx, "bad/run", "deletion-1"); err == nil {
		t.Fatal("FenceRunDeletion() accepted invalid run ID")
	}
	if err := artifacts.FenceRunDeletion(ctx, "run-1", "bad/deletion"); err == nil {
		t.Fatal("FenceRunDeletion() accepted invalid deletion ID")
	}
}
