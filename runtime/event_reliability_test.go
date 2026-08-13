package runtime

import (
	"errors"
	"testing"
	"time"
)

func TestRuntimeEventReliabilityCoversEveryDeclaredType(t *testing.T) {
	for _, eventType := range EventTypes() {
		if reliability := EventReliabilityOf(eventType); reliability == "" {
			t.Fatalf("event %q has no reliability classification", eventType)
		}
	}
	if EventReliabilityOf(EventRunFinished) != EventReliabilityState {
		t.Fatalf("run.finished reliability = %q", EventReliabilityOf(EventRunFinished))
	}
	if EventReliabilityOf(EventLLMContentChunk) != EventReliabilityLive {
		t.Fatalf("llm.content_chunk reliability = %q", EventReliabilityOf(EventLLMContentChunk))
	}
	if EventReliabilityOf(EventContractViolation) != EventReliabilityDiagnostic {
		t.Fatalf("contract.violation reliability = %q", EventReliabilityOf(EventContractViolation))
	}
}

func TestBestEffortEventFailureDiagnosticsTrackCountAndLastError(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	runner := &GraphRunner{
		now: func() time.Time { return now },
		eventDiagnostics: EventPublicationDiagnostics{
			BestEffortFailures: map[EventType]EventPublicationFailure{},
		},
	}
	runner.recordBestEffortEventFailure(EventArtifactCreated, errors.New("first"))
	now = now.Add(time.Second)
	runner.recordBestEffortEventFailure(EventArtifactCreated, errors.New("second"))

	diagnostics := runner.EventPublicationDiagnostics()
	failure := diagnostics.BestEffortFailures[EventArtifactCreated]
	if failure.Count != 2 || failure.LastError != "second" || !failure.LastOccurredAt.Equal(now) {
		t.Fatalf("failure diagnostics = %#v", failure)
	}
	delete(diagnostics.BestEffortFailures, EventArtifactCreated)
	if runner.EventPublicationDiagnostics().BestEffortFailures[EventArtifactCreated].Count != 2 {
		t.Fatal("EventPublicationDiagnostics returned a mutable internal map")
	}
}
