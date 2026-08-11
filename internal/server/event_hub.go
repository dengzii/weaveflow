package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

const (
	defaultEventBuffer               = 256
	defaultEventHistoryBytes         = 1 << 20
	defaultStreamingHistoryBytes     = 512 << 10
	defaultMaxEventHistoryPartitions = 64
	defaultStreamingHistoryTTL       = 2 * time.Minute
)

type EventHub struct {
	mu               sync.Mutex
	subscribers      map[int]*eventSubscriber
	nextSubscriberID int
	nextSequence     uint64
	partitions       map[eventPartitionKey]*eventHistoryPartition
	options          eventHubOptions
	metrics          EventHubMetrics
	closed           bool
}

type eventHubOptions struct {
	subscriberBuffer   int
	eventHistoryLimit  int
	eventHistoryBytes  int
	streamHistoryLimit int
	streamHistoryBytes int
	streamHistoryTTL   time.Duration
	maxReplay          int
	maxPartitions      int
	now                func() time.Time
	logger             *slog.Logger
}

type EventHubMetrics struct {
	PublishedEvents       uint64
	ReplayedEvents        uint64
	ReplayGaps            uint64
	OverflowedSubscribers uint64
	OversizedEvents       uint64
	CurrentHistoryEvents  int
	CurrentHistoryBytes   int
	CurrentSubscribers    int
}

type eventSubscriber struct {
	id         int
	ch         chan runtime.Event
	closed     chan eventSubscriptionClose
	filter     eventFilter
	lastCursor string
}

type eventSubscriptionClose struct {
	Reason string
}

type eventSubscription struct {
	Events       <-chan runtime.Event
	Closed       <-chan eventSubscriptionClose
	Replay       eventReplay
	SubscriberID int
	Unsubscribe  func()
}

type eventReplay struct {
	Gap             bool
	Reason          string
	RequestedCursor string
	OldestEventID   string
	ResumeCursor    string
}

type eventPartitionKey struct {
	GraphID        string
	GraphSessionID string
}

type eventHistoryPartition struct {
	regular                eventRing
	streaming              eventRing
	evictedThroughSequence uint64
	lastPublishedAt        time.Time
}

type eventRing struct {
	entries []eventHistoryEntry
	head    int
	count   int
	bytes   int
}

type eventHistoryEntry struct {
	event       runtime.Event
	sequence    uint64
	size        int
	publishedAt time.Time
}

type eventFilter struct {
	GraphID        string
	GraphSessionID string
	RunID          string
	NodeID         string
	Types          map[runtime.EventType]struct{}
}

func NewEventHub(buffer int) *EventHub {
	if buffer <= 0 {
		buffer = defaultEventBuffer
	}
	historyLimit := buffer * 8
	if historyLimit < 1024 {
		historyLimit = 1024
	}
	streamHistoryLimit := buffer * 2
	if streamHistoryLimit < 256 {
		streamHistoryLimit = 256
	}
	return newEventHub(eventHubOptions{
		subscriberBuffer:   buffer,
		eventHistoryLimit:  historyLimit,
		eventHistoryBytes:  defaultEventHistoryBytes,
		streamHistoryLimit: streamHistoryLimit,
		streamHistoryBytes: defaultStreamingHistoryBytes,
		streamHistoryTTL:   defaultStreamingHistoryTTL,
		maxReplay:          buffer * 4,
		maxPartitions:      defaultMaxEventHistoryPartitions,
		now:                time.Now,
		logger:             slog.Default(),
	})
}

func newEventHub(options eventHubOptions) *EventHub {
	if options.subscriberBuffer <= 0 {
		options.subscriberBuffer = 1
	}
	if options.eventHistoryLimit <= 0 {
		options.eventHistoryLimit = 1
	}
	if options.eventHistoryBytes <= 0 {
		options.eventHistoryBytes = defaultEventHistoryBytes
	}
	if options.streamHistoryLimit <= 0 {
		options.streamHistoryLimit = 1
	}
	if options.streamHistoryBytes <= 0 {
		options.streamHistoryBytes = defaultStreamingHistoryBytes
	}
	if options.streamHistoryTTL <= 0 {
		options.streamHistoryTTL = defaultStreamingHistoryTTL
	}
	if options.maxReplay <= 0 {
		options.maxReplay = options.subscriberBuffer
	}
	if options.maxPartitions <= 0 {
		options.maxPartitions = defaultMaxEventHistoryPartitions
	}
	if options.now == nil {
		options.now = time.Now
	}
	if options.logger == nil {
		options.logger = slog.Default()
	}
	return &EventHub{
		subscribers: map[int]*eventSubscriber{},
		partitions:  map[eventPartitionKey]*eventHistoryPartition{},
		options:     options,
	}
}

func (h *EventHub) Publish(_ context.Context, event runtime.Event) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.publishLocked(event)
	return nil
}

func (h *EventHub) PublishBatch(_ context.Context, events []runtime.Event) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	for _, event := range events {
		h.publishLocked(event)
	}
	return nil
}

func (h *EventHub) publishLocked(event runtime.Event) {
	now := h.options.now()
	h.nextSequence++
	h.metrics.PublishedEvents++
	entry := eventHistoryEntry{
		event:       h.boundedEventLocked(event),
		sequence:    h.nextSequence,
		publishedAt: now,
	}
	entry.size = estimateRuntimeEventBytes(entry.event)
	partition := h.partitionLocked(eventPartitionKey{
		GraphID:        event.GraphID,
		GraphSessionID: event.GraphSessionID,
	}, now)
	partition.lastPublishedAt = now
	if runtime.IsStreamingEvent(entry.event.Type) {
		partition.evictExpiredStreaming(now.Add(-h.options.streamHistoryTTL), h.recordEvictionLocked)
		if entry.size <= h.options.streamHistoryBytes {
			partition.streaming.append(entry, h.options.streamHistoryLimit, h.options.streamHistoryBytes, h.recordEvictionLocked)
		} else {
			partition.evictedThroughSequence = entry.sequence
		}
	} else {
		if entry.size <= h.options.eventHistoryBytes {
			partition.regular.append(entry, h.options.eventHistoryLimit, h.options.eventHistoryBytes, h.recordEvictionLocked)
		} else {
			partition.evictedThroughSequence = entry.sequence
		}
	}
	h.recalculateHistoryMetricsLocked()

	for id, subscriber := range h.subscribers {
		if !subscriber.filter.Match(entry.event) {
			continue
		}
		select {
		case subscriber.ch <- entry.event:
			subscriber.lastCursor = entry.event.ID
		default:
			h.metrics.OverflowedSubscribers++
			h.options.logger.Warn("runtime event subscriber overflow",
				"graph_id", subscriber.filter.GraphID,
				"graph_session_id", subscriber.filter.GraphSessionID,
				"subscriber_id", subscriber.id,
				"last_cursor", subscriber.lastCursor,
				"dropped_count", 1,
			)
			h.closeSubscriberLocked(id, "overflow")
		}
	}
}

func (h *EventHub) Subscribe(filter eventFilter, cursor string) eventSubscription {
	if h == nil {
		events := make(chan runtime.Event)
		closed := make(chan eventSubscriptionClose, 1)
		close(events)
		closed <- eventSubscriptionClose{Reason: "hub_unavailable"}
		close(closed)
		return eventSubscription{Events: events, Closed: closed, Unsubscribe: func() {}}
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.options.now()
	for key, partition := range h.partitions {
		if filter.matchesPartition(key) {
			partition.evictExpiredStreaming(now.Add(-h.options.streamHistoryTTL), h.recordEvictionLocked)
		}
	}
	replay, replayState := h.eventsAfterLocked(filter, cursor)
	if replayState.Gap {
		h.metrics.ReplayGaps++
		replay = nil
	} else {
		h.metrics.ReplayedEvents += uint64(len(replay))
	}
	capacity := h.options.subscriberBuffer
	if len(replay) > capacity {
		capacity = len(replay)
	}
	events := make(chan runtime.Event, capacity)
	for _, entry := range replay {
		events <- entry.event
	}
	closed := make(chan eventSubscriptionClose, 1)
	id := h.nextSubscriberID
	h.nextSubscriberID++
	subscriber := &eventSubscriber{
		id:         id,
		ch:         events,
		closed:     closed,
		filter:     filter,
		lastCursor: cursor,
	}
	h.subscribers[id] = subscriber
	h.metrics.CurrentSubscribers = len(h.subscribers)
	h.recalculateHistoryMetricsLocked()

	return eventSubscription{
		Events:       events,
		Closed:       closed,
		Replay:       replayState,
		SubscriberID: id,
		Unsubscribe: func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.closeSubscriberLocked(id, "unsubscribed")
		},
	}
}

func (h *EventHub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id := range h.subscribers {
		h.closeSubscriberLocked(id, "server_shutdown")
	}
}

func (h *EventHub) Metrics() EventHubMetrics {
	if h == nil {
		return EventHubMetrics{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recalculateHistoryMetricsLocked()
	return h.metrics
}

func (h *EventHub) eventsAfterLocked(filter eventFilter, cursor string) ([]eventHistoryEntry, eventReplay) {
	state := eventReplay{RequestedCursor: cursor}
	entries := h.matchingEntriesLocked(filter)
	if len(entries) > 0 {
		state.OldestEventID = entries[0].event.ID
		state.ResumeCursor = entries[len(entries)-1].event.ID
	}
	if cursor == "" {
		return nil, state
	}

	cursorSequence := uint64(0)
	for _, entry := range entries {
		if entry.event.ID == cursor {
			cursorSequence = entry.sequence
			break
		}
	}
	if cursorSequence == 0 {
		state.Gap = true
		state.Reason = "cursor_not_retained"
		return nil, state
	}
	for key, partition := range h.partitions {
		if filter.matchesPartition(key) && partition.evictedThroughSequence > cursorSequence {
			state.Gap = true
			state.Reason = "cursor_expired"
			return nil, state
		}
	}
	start := sort.Search(len(entries), func(index int) bool {
		return entries[index].sequence > cursorSequence
	})
	replay := entries[start:]
	if len(replay) > h.options.maxReplay {
		state.Gap = true
		state.Reason = "replay_limit_exceeded"
		return nil, state
	}
	return replay, state
}

func (h *EventHub) matchingEntriesLocked(filter eventFilter) []eventHistoryEntry {
	var entries []eventHistoryEntry
	for key, partition := range h.partitions {
		if !filter.matchesPartition(key) {
			continue
		}
		partition.regular.forEach(func(entry eventHistoryEntry) {
			if filter.Match(entry.event) {
				entries = append(entries, entry)
			}
		})
		partition.streaming.forEach(func(entry eventHistoryEntry) {
			if filter.Match(entry.event) {
				entries = append(entries, entry)
			}
		})
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].sequence < entries[right].sequence
	})
	return entries
}

func (h *EventHub) partitionLocked(key eventPartitionKey, now time.Time) *eventHistoryPartition {
	if partition := h.partitions[key]; partition != nil {
		return partition
	}
	if len(h.partitions) >= h.options.maxPartitions {
		var oldestKey eventPartitionKey
		var oldestTime time.Time
		for candidateKey, candidate := range h.partitions {
			if oldestTime.IsZero() || candidate.lastPublishedAt.Before(oldestTime) {
				oldestKey = candidateKey
				oldestTime = candidate.lastPublishedAt
			}
		}
		delete(h.partitions, oldestKey)
	}
	partition := &eventHistoryPartition{lastPublishedAt: now}
	h.partitions[key] = partition
	return partition
}

func (h *EventHub) boundedEventLocked(event runtime.Event) runtime.Event {
	limit := h.options.eventHistoryBytes
	if runtime.IsStreamingEvent(event.Type) {
		limit = h.options.streamHistoryBytes
	}
	maxEventBytes := limit / 4
	if maxEventBytes < 1024 {
		maxEventBytes = 1024
	}
	estimated := estimateRuntimeEventBytes(event)
	if estimated <= maxEventBytes {
		return event
	}
	h.metrics.OversizedEvents++
	payload, _ := json.Marshal(map[string]any{
		"omitted":        true,
		"original_bytes": estimated,
		"reason":         "event_exceeds_live_history_limit",
	})
	event.Payload = payload
	return event
}

func (h *EventHub) recordEvictionLocked(entry eventHistoryEntry) {
	key := eventPartitionKey{GraphID: entry.event.GraphID, GraphSessionID: entry.event.GraphSessionID}
	partition := h.partitions[key]
	if partition != nil && entry.sequence > partition.evictedThroughSequence {
		partition.evictedThroughSequence = entry.sequence
	}
}

func (h *EventHub) closeSubscriberLocked(id int, reason string) {
	subscriber, ok := h.subscribers[id]
	if !ok {
		return
	}
	delete(h.subscribers, id)
	close(subscriber.ch)
	subscriber.closed <- eventSubscriptionClose{Reason: reason}
	close(subscriber.closed)
	h.metrics.CurrentSubscribers = len(h.subscribers)
}

func (h *EventHub) recalculateHistoryMetricsLocked() {
	events := 0
	bytes := 0
	for _, partition := range h.partitions {
		events += partition.regular.count + partition.streaming.count
		bytes += partition.regular.bytes + partition.streaming.bytes
	}
	h.metrics.CurrentHistoryEvents = events
	h.metrics.CurrentHistoryBytes = bytes
	h.metrics.CurrentSubscribers = len(h.subscribers)
}

func (p *eventHistoryPartition) evictExpiredStreaming(cutoff time.Time, onEvict func(eventHistoryEntry)) {
	for p.streaming.count > 0 {
		oldest, _ := p.streaming.oldest()
		if !oldest.publishedAt.Before(cutoff) {
			return
		}
		p.streaming.removeOldest(onEvict)
	}
}

func (r *eventRing) append(entry eventHistoryEntry, eventLimit, byteLimit int, onEvict func(eventHistoryEntry)) {
	if len(r.entries) != eventLimit {
		r.entries = make([]eventHistoryEntry, eventLimit)
		r.head = 0
		r.count = 0
		r.bytes = 0
	}
	for r.count > 0 && (r.count >= eventLimit || r.bytes+entry.size > byteLimit) {
		r.removeOldest(onEvict)
	}
	index := (r.head + r.count) % len(r.entries)
	r.entries[index] = entry
	r.count++
	r.bytes += entry.size
}

func (r *eventRing) oldest() (eventHistoryEntry, bool) {
	if r.count == 0 {
		return eventHistoryEntry{}, false
	}
	return r.entries[r.head], true
}

func (r *eventRing) removeOldest(onEvict func(eventHistoryEntry)) {
	if r.count == 0 {
		return
	}
	entry := r.entries[r.head]
	r.entries[r.head] = eventHistoryEntry{}
	r.head = (r.head + 1) % len(r.entries)
	r.count--
	r.bytes -= entry.size
	if onEvict != nil {
		onEvict(entry)
	}
}

func (r *eventRing) forEach(visit func(eventHistoryEntry)) {
	for index := 0; index < r.count; index++ {
		visit(r.entries[(r.head+index)%len(r.entries)])
	}
}

func (f eventFilter) matchesPartition(key eventPartitionKey) bool {
	if f.GraphID != "" && key.GraphID != f.GraphID {
		return false
	}
	return f.GraphSessionID == "" || key.GraphSessionID == f.GraphSessionID
}

func (f eventFilter) Match(event runtime.Event) bool {
	if f.GraphID != "" && event.GraphID != f.GraphID {
		return false
	}
	if f.GraphSessionID != "" && event.GraphSessionID != f.GraphSessionID {
		return false
	}
	if f.RunID != "" && event.RunID != f.RunID {
		return false
	}
	if f.NodeID != "" && event.NodeID != f.NodeID {
		return false
	}
	if len(f.Types) > 0 {
		if _, ok := f.Types[event.Type]; !ok {
			return false
		}
	}
	return true
}

func estimateRuntimeEventBytes(event runtime.Event) int {
	return 96 + len(event.ID) + len(event.GraphID) + len(event.GraphSessionID) + len(event.RunID) +
		len(event.StepID) + len(event.NodeID) + len(event.Type) + len(event.Payload)
}
