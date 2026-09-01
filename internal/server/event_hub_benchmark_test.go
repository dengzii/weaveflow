package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

var eventHubBenchmarkMetrics EventHubMetrics

func BenchmarkEventHubBroadcastBacklogged(b *testing.B) {
	for _, subscriberCount := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("Subscribers_%d", subscriberCount), func(b *testing.B) {
			hub, subscriptions := benchmarkEventHubSubscriptions(subscriberCount, 2, 1)
			defer hub.Close()
			event := benchmarkRuntimeEvent("broadcast")

			b.ReportAllocs()
			b.ResetTimer()
			b.ReportMetric(float64(subscriberCount), "subscribers/op")
			for iteration := 0; iteration < b.N; iteration++ {
				if err := hub.Publish(context.Background(), event); err != nil {
					b.Fatalf("publish event: %v", err)
				}
				b.StopTimer()
				for _, subscription := range subscriptions {
					if _, open := <-subscription.Events; !open {
						b.Fatal("backlogged subscriber overflowed")
					}
				}
				b.StartTimer()
			}
			b.StopTimer()
			eventHubBenchmarkMetrics = hub.Metrics()
		})
	}
}

func BenchmarkEventHubBroadcastOverflow(b *testing.B) {
	for _, subscriberCount := range []int{100, 1_000} {
		b.Run(fmt.Sprintf("Subscribers_%d", subscriberCount), func(b *testing.B) {
			event := benchmarkRuntimeEvent("overflow")
			b.ReportAllocs()
			b.ReportMetric(float64(subscriberCount), "subscribers/op")
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				hub, _ := benchmarkEventHubSubscriptions(subscriberCount, 1, 1)
				b.StartTimer()
				if err := hub.Publish(context.Background(), event); err != nil {
					b.Fatalf("publish overflowing event: %v", err)
				}
				b.StopTimer()
				eventHubBenchmarkMetrics = hub.Metrics()
				if eventHubBenchmarkMetrics.OverflowedSubscribers != uint64(subscriberCount) {
					b.Fatalf("overflowed subscribers = %d, want %d", eventHubBenchmarkMetrics.OverflowedSubscribers, subscriberCount)
				}
				b.StartTimer()
			}
		})
	}
}

func benchmarkEventHubSubscriptions(subscriberCount, buffer, backlog int) (*EventHub, []eventSubscription) {
	fixedNow := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	hub := newEventHub(eventHubOptions{
		subscriberBuffer:   buffer,
		eventHistoryLimit:  16,
		eventHistoryBytes:  1 << 20,
		streamHistoryLimit: 16,
		streamHistoryBytes: 1 << 20,
		streamHistoryTTL:   time.Minute,
		maxReplay:          16,
		maxPartitions:      1,
		now:                func() time.Time { return fixedNow },
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	subscriptions := make([]eventSubscription, subscriberCount)
	for index := range subscriptions {
		subscriptions[index] = hub.Subscribe(eventFilter{GraphID: "graph"}, "")
	}
	for index := 0; index < backlog; index++ {
		_ = hub.Publish(context.Background(), benchmarkRuntimeEvent(fmt.Sprintf("backlog-%d", index)))
	}
	return hub, subscriptions
}

func benchmarkRuntimeEvent(id string) runtime.Event {
	return runtime.Event{
		ID:             id,
		GraphID:        "graph",
		GraphSessionID: "session",
		RunID:          "run",
		StepID:         "step",
		NodeID:         "node",
		Type:           runtime.EventNodeCustom,
		Payload:        []byte(`{"kind":"benchmark","status":"queued"}`),
	}
}
