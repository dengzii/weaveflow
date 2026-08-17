package trigger

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/internal/chatchannel"
)

type GraphReplacement struct {
	service     *Service
	graphID     string
	previous    []Trigger
	next        []Trigger
	schedules   map[string]*scheduleEntry
	channels    map[string]chatchannel.Instance
	previousIDs map[string]struct{}
	nextIDs     map[string]struct{}
	persisted   bool
	finished    bool
}

func (s *Service) PrepareGraphReplacement(
	ctx context.Context,
	graphID string,
	items []Trigger,
	remapConflicts bool,
) (*GraphReplacement, map[string]string, error) {
	if s == nil {
		return nil, nil, fmt.Errorf("trigger service is nil")
	}
	graphID = strings.TrimSpace(graphID)
	if graphID == "" {
		return nil, nil, fmt.Errorf("%w: graph_id is required", ErrInvalidTarget)
	}
	s.operationMu.Lock()
	replacement, idMapping, err := s.prepareGraphReplacementLocked(ctx, graphID, items, remapConflicts)
	if err != nil {
		s.operationMu.Unlock()
		return nil, nil, err
	}
	return replacement, idMapping, nil
}

func (s *Service) prepareGraphReplacementLocked(
	ctx context.Context,
	graphID string,
	items []Trigger,
	remapConflicts bool,
) (*GraphReplacement, map[string]string, error) {
	existing, err := s.triggerStore.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	existingByID := make(map[string]Trigger, len(existing))
	usedIDs := make(map[string]struct{}, len(existing)+len(items))
	previous := make([]Trigger, 0)
	previousIDs := make(map[string]struct{})
	for _, item := range existing {
		existingByID[item.ID] = item
		usedIDs[item.ID] = struct{}{}
		if item.Target.GraphID == graphID {
			previous = append(previous, item)
			previousIDs[item.ID] = struct{}{}
		}
	}

	now := s.now()
	next := make([]Trigger, 0, len(items))
	schedules := make(map[string]*scheduleEntry, len(items))
	channels := make(map[string]chatchannel.Instance, len(items))
	seenSourceIDs := make(map[string]struct{}, len(items))
	nextIDs := make(map[string]struct{}, len(items))
	idMapping := make(map[string]string)
	for _, candidate := range items {
		sourceID := strings.TrimSpace(candidate.ID)
		if _, duplicate := seenSourceIDs[sourceID]; duplicate {
			return nil, nil, ErrExists
		}
		seenSourceIDs[sourceID] = struct{}{}
		previousItem, exists := existingByID[sourceID]
		if exists && previousItem.Target.GraphID != graphID {
			if !remapConflicts {
				return nil, nil, ErrExists
			}
			candidate.ID = nextTriggerID(sourceID, usedIDs)
			idMapping[sourceID] = candidate.ID
			exists = false
		} else {
			candidate.ID = sourceID
		}
		usedIDs[candidate.ID] = struct{}{}
		candidate.Target.GraphID = graphID
		if exists {
			if candidate.Chat != nil && previousItem.Chat != nil && strings.TrimSpace(candidate.Chat.Channel) == strings.TrimSpace(previousItem.Chat.Channel) {
				candidate.Chat.ChannelConfig = s.chatRegistry.MergeWriteOnlyConfig(
					candidate.Chat.Channel,
					previousItem.Chat.ChannelConfig,
					candidate.Chat.ChannelConfig,
				)
			}
			candidate = candidate.Normalize(previousItem.CreatedAt)
			candidate.CreatedAt = previousItem.CreatedAt
			candidate.UpdatedAt = now
		} else {
			candidate = candidate.Normalize(now)
		}
		if err := candidate.Validate(); err != nil {
			return nil, nil, err
		}
		if err := s.validateChatChannelSecretRefs(candidate); err != nil {
			return nil, nil, err
		}
		if err := validateScheduleExpression(candidate); err != nil {
			return nil, nil, err
		}
		schedule, err := s.buildSchedule(candidate)
		if err != nil {
			return nil, nil, err
		}
		channel, err := s.buildChatChannel(ctx, candidate)
		if err != nil {
			return nil, nil, err
		}
		if schedule != nil {
			schedules[candidate.ID] = schedule
		}
		if channel != nil {
			channels[candidate.ID] = channel
		}
		nextIDs[candidate.ID] = struct{}{}
		next = append(next, candidate)
	}

	return &GraphReplacement{
		service:     s,
		graphID:     graphID,
		previous:    previous,
		next:        next,
		schedules:   schedules,
		channels:    channels,
		previousIDs: previousIDs,
		nextIDs:     nextIDs,
	}, idMapping, nil
}

func (replacement *GraphReplacement) Items() []Trigger {
	if replacement == nil {
		return nil
	}
	return append([]Trigger(nil), replacement.next...)
}

func (replacement *GraphReplacement) PreviousItems() []Trigger {
	if replacement == nil {
		return nil
	}
	return append([]Trigger(nil), replacement.previous...)
}

func (replacement *GraphReplacement) SetGraphSessionID(sessionID string) {
	if replacement == nil || replacement.finished {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	for index := range replacement.next {
		replacement.next[index].Target.GraphSessionID = sessionID
	}
}

func (replacement *GraphReplacement) Persist(ctx context.Context) error {
	if replacement == nil || replacement.service == nil {
		return fmt.Errorf("graph replacement is nil")
	}
	if replacement.finished {
		return fmt.Errorf("graph replacement is already finished")
	}
	if replacement.persisted {
		return nil
	}
	if err := replacement.service.triggerStore.ReplaceGraph(ctx, replacement.graphID, replacement.next); err != nil {
		return err
	}
	replacement.persisted = true
	return nil
}

func (replacement *GraphReplacement) Commit() {
	if replacement == nil || replacement.service == nil || replacement.finished {
		return
	}
	for id := range replacement.previousIDs {
		if _, keep := replacement.nextIDs[id]; keep {
			continue
		}
		replacement.service.replaceSchedule(id, nil)
		replacement.service.replaceChatChannel(id, nil, nil)
	}
	for _, item := range replacement.next {
		replacement.service.replaceSchedule(item.ID, replacement.schedules[item.ID])
		replacement.service.replaceChatChannel(item.ID, item.Chat, replacement.channels[item.ID])
	}
	replacement.finished = true
	replacement.service.operationMu.Unlock()
}

func (replacement *GraphReplacement) Rollback(ctx context.Context) error {
	if replacement == nil || replacement.service == nil || replacement.finished {
		return nil
	}
	replacement.finished = true
	defer replacement.service.operationMu.Unlock()
	if !replacement.persisted {
		return nil
	}
	return replacement.service.triggerStore.ReplaceGraph(ctx, replacement.graphID, replacement.previous)
}

func nextTriggerID(sourceID string, usedIDs map[string]struct{}) string {
	for suffix := 1; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", sourceID, suffix)
		if _, exists := usedIDs[candidate]; !exists {
			return candidate
		}
	}
}
